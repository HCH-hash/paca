# Status Assignment Rules

This document explains the status-assignment-rule feature: project-wide,
filterable automation that assigns a task to a member whenever it reaches a
configured status — for *any* task in the project, not just tasks wired
into an [automation workflow](./automation-workflows.md).

## Why this exists

This feature used to be bundled into automation workflows as a per-workflow
`StatusRule` (status → assignee, scoped to one workflow's nodes only). It has
been split out into its own, standalone concept because:

1. Reassignment-by-status is a useful automation on its own, independent of
   whether a task happens to be part of a dependency graph.
2. Scoping it to "only tasks in this workflow" meant a task not wired into
   any workflow could never benefit from it.
3. There was no way to narrow a rule to a subset of tasks (e.g. "only bugs,"
   "only tasks with no assignee yet") — a rule applied uniformly to every
   task reaching that status.

A workflow still exists for the dependency-graph part of automation (nodes,
edges, "what status comes next"); it now delegates all actual reassignment
to this engine — see
[automation-workflows.md](./automation-workflows.md#the-predecessor-done-cascade).

## Core model

- **Rule** (`statusruledom.StatusAssignmentRule`) — belongs to a project, not
  a workflow. Fields: `Name`, `StatusID` (the trigger status — immutable
  after creation), `AssigneeMemberID`, `Filter`, `Priority`, `Enabled`.
- **Filter** (`TaskFilterSpec`) — an optional set of criteria (task type,
  sprint/backlog, current assignee, tags, importance, story points, start/
  due date range, and custom fields) a task must satisfy for the rule to
  apply. An empty filter matches every task at that status — the
  "whole project, unfiltered" case. All set criteria are AND'd together;
  criteria that are naturally multi-valued (e.g. `task_type_ids`) are
  OR'd within themselves.
- **Priority** — multiple rules may target the same status. They're
  evaluated in ascending priority order (a new rule is appended to the end
  of its status's list); the **first enabled rule whose filter matches**
  the task wins. A disabled rule is skipped but still occupies a priority
  slot.

There is no uniqueness constraint on `(project, status)` — that's the whole
point of priority-ordered, filtered rules: several rules can target the same
status, each scoped to a different subset of tasks (or one deliberately
broader fallback with no filter, placed last).

## Matching, reusing the existing task-filter SQL

Rather than re-implementing filter evaluation, a rule's filter is translated
into a `taskdom.TaskFilter` — the same struct `ListTasks`/`CountTasks` use —
with an added `TaskID` field restricting it to exactly one task. Checking
whether a rule matches a task is then just `CountTasks(...) > 0` against
that single-task filter. This means custom-field type resolution, date
range semantics, and every other filter nuance is defined in exactly one
place (`repository/postgres/task_repository.go`'s `applyTaskFilter`/
`applyCustomFieldFilters`), not duplicated for the rule engine.

## Execution engine

Two independent Valkey-stream consumers apply this engine, both reading
`events.StreamTaskActivities` under their own consumer group so neither
blocks or double-processes for the other:

- **`worker.StatusRuleConsumer`** (group `api.status_rule_engine`) — on
  every `task.updated` activity with a status field change, for *any* task
  in *any* project, calls `ApplyMatchingRule` for that task directly. No
  workflow/node lookup at all — this is what makes the feature apply
  project-wide.
- **`worker.WorkflowConsumer`**'s predecessor-done cascade (group
  `api.workflow_engine`) — calls the same `ApplyMatchingRule` for a
  downstream task once its predecessor(s) in a workflow finish, using the
  downstream task's own current status. See
  [automation-workflows.md](./automation-workflows.md#the-predecessor-done-cascade).

Both paths converge on `statusruledom.Service.ApplyMatchingRule(ctx,
projectID, task, reason, extra)`:

1. List enabled rules for `(projectID, *task.StatusID)`, ordered by
   priority (`ListEnabledRulesByProjectAndStatus`, cached — see
   [Caching](#caching)).
2. For each, in order, check if its filter matches the task; stop at the
   first match.
3. If no rule matches, or the task is already assigned to exactly that
   rule's member, no-op.
4. Otherwise, reassign via the ordinary task service `UpdateTask` (same
   validation/side effects as a human PATCH), record a
   `status_rule.assigned` activity (`taskdom.ActivityTypeStatusRuleAssigned`)
   with `{rule_id, rule_name, reason, old_assignees, new_assignee}` merged
   with the caller's `extra` (e.g. `workflow_id`/`workflow_name`/
   `next_status_name` when called from the workflow cascade), and publish to
   `events.StreamTaskAssignments` so the existing `NotificationConsumer`
   creates the in-app notification / triggers the agent conversation
   uniformly regardless of which path triggered the reassignment.

## Caching

`ListEnabledRulesByProjectAndStatus` is the hot read on every status-change
event, project-wide (not gated by workflow membership, so it runs far more
often than the old per-workflow rule lookup did). It's wrapped by
`statusrulesvc.CachedRepository`, a Redis-backed decorator keyed by
`(projectID, statusID)`, invalidated on every create/update/delete/reorder
through the same decorator instance — shared between the HTTP-write service
and both consumers, so an edit is visible to the very next event, not just
once some TTL expires.

## Data model

```sql
status_assignment_rules   -- id, project_id, name, status_id, assignee_member_id,
                           -- filter (jsonb), priority, enabled, created_by
```

See `services/api/migrations/000027_add_status_assignment_rules.sql`. The
`filter` column stores `TaskFilterSpec` as a single JSONB blob (mirroring
`sprint_views.config`), not a normalized filter-row table. That migration
also best-effort carries forward any existing `workflow_status_rules` rows
as unfiltered project-wide rules (one per distinct
`(project, status, assignee)` combo across all of that project's
workflows), then drops the old table.

Index: `(project_id, status_id, priority)` — serves both the full
per-project list (`ListRulesByProject`, used by the Automation → Status
Rules page) and the hot per-status lookup as a prefix scan.

## API

All endpoints are under `/api/v1/projects/:projectId/status-assignment-rules`,
independent of any workflow. Gated on the existing `tasks.read`/`tasks.write`
permission keys — no dedicated permission key of its own, matching how
task-statuses and custom-fields (other project-settings-shaped features) are
gated.

```
GET    /status-assignment-rules              list every rule in the project, across all statuses
POST   /status-assignment-rules              create a rule (appended to the end of its status's priority list)
PATCH  /status-assignment-rules/:ruleId       update name/assignee/filter/enabled (status_id is immutable)
DELETE /status-assignment-rules/:ruleId       delete a rule
PUT    /status-assignment-rules/positions     reorder every rule for one status: { status_id, rule_ids }
```

## AI agent integration

Exposed as MCP tools in `apps/mcp/src/tools/status-rule-tools.ts`:
`list_status_assignment_rules`, `create_status_assignment_rule`,
`update_status_assignment_rule`, `delete_status_assignment_rule`,
`reorder_status_assignment_rules` — a simple CRUD tool set (mirroring
`task-type-tools.ts`'s shape), separate from the workflow tools' graph-diffing
complexity. Gated on `tasks.read`/`tasks.write` in `apps/mcp/src/permissions.ts`.

## Frontend

Configured from its own top-level **Automation → Status Rules** sidebar
entry (`apps/web/src/routes/_authenticated/projects/$projectId/automation/status-rules.tsx`,
rendering `StatusAssignmentRulesPanel` from
`apps/web/src/components/projects/automation/status-assignment-rules-panel.tsx`),
a sibling of the Workflow item rather than a tab under project Settings or
a panel embedded in the workflow canvas — reinforcing that this is a
project-wide setting, not a per-workflow one. The "Automation" sidebar
section (`apps/web/src/components/app-shell/app-sidebar.tsx`) is a
top-level group, styled and positioned like the Interactions/Documentation
sections, listing the Workflow and Status Rules destinations. Rules are
grouped by trigger status and reorderable within each group (native
drag-and-drop, mirroring `TaskStatusesSettings.tsx`). The create/edit
dialog (`StatusAssignmentRuleFormDialog.tsx`) includes a filter builder
covering every `TaskFilterSpec` dimension, including one section per
project custom field (control shape driven by the field's type —
checkboxes for select/multi_select/boolean, min/max for number,
after/before for date, contains for text/url).

`apps/web/src/lib/status-rule-api.ts` is the API client.
