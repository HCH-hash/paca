# Automation Workflows

This document explains the automation-workflow feature: a project-scoped
dependency graph over *existing* tasks that unlocks downstream tasks (and
re-triggers their assignment) as their predecessors finish.

> Status→assignee reassignment itself — "whenever a task's status becomes X,
> assign it to member M," optionally filtered by task fields — is a separate,
> project-wide feature. See
> [status-assignment-rules.md](./status-assignment-rules.md). This document
> covers only the dependency graph: nodes, edges, and the status-transition
> chain, plus how a workflow's edges trigger the rule engine for downstream
> tasks.

## Why this exists

A workflow lets a project define, once, "when this task is done, re-evaluate
the next one" — a dependency graph over tasks, independent of whichever
status-assignment rules happen to be configured at the time. The actual
"assign to whom" decision is delegated entirely to the project-wide rule
engine (see the linked doc above); the workflow just knows *when* to ask it
again for a downstream task.

## Core model

A workflow is a directed graph, plus one shared, workflow-level lookup table:

- **Node** — wraps one existing task. A task can be a node in zero, one, or
  many workflows.
- **Status transition** (on the workflow) — the "status workflow": for each
  project status, an optional "next status" — what a task at that status
  should move to once work there is done. A status with no next status
  configured is **terminal**; the workflow's single **done status** is
  *derived* as whichever status is terminal (see
  [Done status resolution](#done-status-resolution)). This drives both the
  AND-join in the predecessor-done cascade and the hint given to an
  AI-agent assignee (see
  [below](#telling-an-assigned-agent-what-to-do-next)).
- **Edge** — a plain directed link `source node → target node`. It carries no
  configuration of its own; it only means "once source is done, re-evaluate
  target's assignment against the project-wide rule engine."

There is no way for an edge itself to change a task's status; only a human,
an agent, or a status-assignment rule ever changes a task's actual status.

## The predecessor-done cascade

Once a node's task reaches the workflow's **derived done status**, for every
outgoing edge from that node to a target node: check whether *all* of the
target's incoming edges now have a done source (see
[AND-join](#and-join) below). If so, ask the project-wide
status-assignment-rule engine to re-evaluate the target task's assignment
using the target's **own current status** (unchanged) — see
`statusruledom.Service.ApplyMatchingRule`.

This never changes a task's status — it only decides *whether* to re-run the
rule engine for a downstream task, using that task's status as it already
stands. This is why a project-wide rule should exist for whatever status a
downstream task naturally sits in while waiting (e.g. "Ready" or "To Do"),
not just a terminal status — otherwise there's nothing to reassign it to
when it unlocks.

Reassignment triggered directly by a task's *own* status change (independent
of any workflow) is a separate concern, handled by a different consumer —
see [status-assignment-rules.md](./status-assignment-rules.md#execution-engine).

### Done status resolution

There is no per-node `done_status_id` field and no implicit "project's
single done-category status" fallback. The workflow's done status is always
**derived** from its status-transition chain: it's whichever status has
`next_status_id = NULL` (see `workflowdom.DeriveDoneStatusID`). This must be
exactly one status — `Activate` (see [Lifecycle](#lifecycle)) rejects the
workflow if the chain has zero or more than one such entry.

A default chain is auto-generated when a workflow is created:
`CreateWorkflow` lists the project's task statuses ordered by board
`position` and chains them sequentially (status at position *N*'s next
status is the one at position *N+1*), leaving the last (highest-position)
status terminal. Users and agents can customize this afterward via
`SetStatusTransition`/`set_workflow_status_transition`. This auto-seed step
is best-effort — failure doesn't block workflow creation, since the
resulting empty-chain draft is still perfectly usable and fixable via the
inline, always-visible chain editor.

### AND-join

If a target node has more than one incoming edge, the cascade only fires
once **every** predecessor has reached the workflow's derived done status —
not on the first one.

This is evaluated **statelessly**: there is no persisted "join progress"
counter. On every status-change event, the engine re-derives "has predecessor
P finished?" by reading P's task's *current* status live and comparing it to
the workflow's derived done status. If not all predecessors currently
qualify, nothing happens; the same check naturally re-runs (and can pass) the
next time a remaining predecessor also reaches the done status. This makes
the join idempotent and safe under at-least-once stream redelivery —
replaying the same event twice just recomputes the same booleans.

### Loop safety

The graph is validated as a DAG at edge-creation time (same reachability
check used for task parent/child cycles). Because the cascade only ever
propagates forward along edges, and the graph cannot contain a cycle, any
chain of cascades is guaranteed to terminate in at most *N* node-hops. The
rule engine's own idempotency check (only reassign when the assignee
actually changes) stops redundant re-fires even if the same event is
processed more than once.

## Lifecycle

A workflow is always in one of three states:

| State      | Meaning                                                        |
|------------|-----------------------------------------------------------------|
| `draft`    | Freely editable (nodes, edges, status transitions). Engine ignores it. |
| `active`   | Engine evaluates it on every relevant task status change. Graph is still freely editable — the engine reads it fresh per event with graceful fallbacks for an edge/transition that's missing or mid-edit, so a concurrent change just takes effect on the next event. |
| `archived` | Engine ignores it. Graph is locked — no node/edge/status-transition mutations. Terminal-ish; can be reverted to draft. |

Transitions: `draft → active` (`Activate`, validated — see below),
`active → archived` (`Archive`), `active → draft` (`RevertToDraft`, pauses
the engine; archived workflows cannot be reverted). Renaming/describing a
workflow, and all graph mutations, are allowed in `draft` and `active`; only
`archived` locks editing.

**Activation validation**: at least one node; the graph is still a DAG
(defensive re-check); every node's task still exists in the project (hasn't
been deleted since the node was added); the workflow's status-transition
chain has exactly one derivable done status (see
[Done status resolution](#done-status-resolution)).

## Data model

```sql
workflows                    -- id, project_id, name, description, status, created_by
workflow_nodes                -- id, workflow_id, task_id, pos_x, pos_y
workflow_status_transitions   -- id, workflow_id, status_id, next_status_id (nullable)
workflow_edges                -- id, workflow_id, source_node_id, target_node_id
```

See `services/api/migrations/000018_add_automation_workflows.sql` for the
original DDL and `000027_add_status_assignment_rules.sql` for the migration
that split `workflow_status_rules` out into the project-wide
`status_assignment_rules` table (see the linked doc). Key constraints:

- `workflow_nodes` has a unique `(workflow_id, task_id)` — a task appears at
  most once *per workflow*, but can belong to many different workflows.
- `workflow_edges` has `CHECK (source_node_id <> target_node_id)` (no
  self-loops) and a unique `(source_node_id, target_node_id)` (no duplicate
  edges).
- `workflow_status_transitions` has a unique `(workflow_id, status_id)` and
  `CHECK (next_status_id IS NULL OR next_status_id <> status_id)` (no
  self-transitions); `next_status_id = NULL` marks that status as the
  workflow's terminal/done status.

## Execution engine

`internal/worker/workflow_consumer.go` (`WorkflowConsumer`) subscribes to the
same Valkey stream the task-activity pipeline already writes to,
`paca.task_activities` (`events.StreamTaskActivities`), under its own
consumer group `api.workflow_engine` — it is a sibling reader, not a special
case wired into the HTTP handler. It runs independently of, and alongside,
the project-wide `StatusRuleConsumer` (see the linked doc) — the two
consumer groups read the same stream without interfering with each other.

On every `task.updated` activity whose `FieldChange[]` includes a `status`
entry:

1. `ListActiveNodesByTaskID(taskID)` — nodes across *active* workflows only
   referencing this task. If none, ack and return (cheap no-op for the
   overwhelming majority of ordinary task updates, and for every task that
   isn't wired into any workflow at all).
2. Re-fetch the task fresh from the repository to get its authoritative
   current `StatusID` — the activity payload's `FieldChange` carries resolved
   status *names*, not IDs, so it can't be used directly.
3. For each matching node: check `isNodeDone` — whether the task's status
   equals the workflow's derived done status
   (`workflowdom.DeriveDoneStatusID` over `ListStatusTransitionsByWorkflow`).
   If so, walk the node's outgoing edges and apply the AND-join check; for
   each qualifying target, call `statusruledom.Service.ApplyMatchingRule` for
   the target's task (its own current status, unchanged).

Reassignment itself — including the `UpdateTask` call, the activity record,
and the notification/agent-trigger publish — is entirely the rule engine's
responsibility now (`ApplyMatchingRule`); `WorkflowConsumer` only decides
*when* to invoke it, and additionally passes `workflow_id`/`workflow_name`/
`next_status_name` as `extra` context for the activity/notification payload.

### Telling an assigned agent what to do next

When the newly-assigned member is an AI agent, `NotificationConsumer` folds
the workflow's name and a **next-status name** into a note appended to the
agent's initial prompt via `TriggerTaskAssigned`'s trailing `note` parameter
— but only when the reassignment came from this workflow's predecessor-done
cascade (`extra.next_status_name`), since that's the only case with a
workflow status-transition chain to look the next status up in. A
reassignment triggered by a task's own status change (the project-wide
consumer) carries no workflow context and so no next-status hint.

The next-status name is looked up from the workflow's status-transition
chain for the task's *current* status (not necessarily the workflow's
terminal done status) — e.g. if the agent is assigned when a task hits
"In Progress" and the chain says "In Progress" → "Review", the note reads
*"This task was automatically assigned to you by the automation workflow
'Release Pipeline'. When you finish your part, set the task status to
'Review' to continue the workflow."* If the task is already at the
terminal/done status (no configured next), the second sentence is omitted.

## API

All endpoints are under `/api/v1/projects/:projectId/workflows`. Read routes
require `workflows.read`; everything else requires `workflows.write`. Node
and status-transition mutations are allowed in `draft` and `active`; once a
workflow is `archived` they're rejected with 409 `WORKFLOW_ARCHIVED`.

```
GET    /workflows                                  list (optional ?status=draft|active|archived)
POST   /workflows                                   create (starts in draft; auto-seeds a default status-transition chain)
GET    /workflows/:workflowId                        get full graph (workflow + nodes + edges + status transitions)
PATCH  /workflows/:workflowId                        rename / re-describe
DELETE /workflows/:workflowId                        soft-delete
POST   /workflows/:workflowId/activate               draft → active
POST   /workflows/:workflowId/archive                active → archived
POST   /workflows/:workflowId/revert-to-draft        active → draft

POST   /workflows/:workflowId/nodes                                    add a task as a node
PATCH  /workflows/:workflowId/nodes/:nodeId                             move (pos_x/pos_y)
DELETE /workflows/:workflowId/nodes/:nodeId                             remove (cascades its edges)

POST   /workflows/:workflowId/status-transitions                       create/update a status → next-status entry
DELETE /workflows/:workflowId/status-transitions/:transitionId         remove an entry

POST   /workflows/:workflowId/edges                                     link two nodes (runs the DAG/cycle check)
DELETE /workflows/:workflowId/edges/:edgeId                              remove a link
```

Status-assignment-rule endpoints (`/projects/:projectId/status-assignment-rules/*`)
are entirely separate — see [status-assignment-rules.md](./status-assignment-rules.md#api).

## Permissions

Two permission keys, following the same `<domain>.read` / `<domain>.write`
convention as the rest of the project permission model:

- `workflows.read` — view workflows and their graphs.
- `workflows.write` — create/edit/activate/archive/delete workflows and their
  nodes, edges, and status transitions.

Granted by default to: `PROJECT_OWNER` / `PROJECT_MANAGER` (via
`workflows.*`), `PROJECT_MEMBER` (both keys), `PROJECT_VIEWER` (read only) —
see `authz.DefaultProjectRoles()`. The per-project roles actually seeded on
project creation (`Admin`/`Editor`/`Viewer`, in `projectsvc.CreateProject`)
carry the same grants. The tail of `000018_add_automation_workflows.sql`
backfills these two keys onto every `project_roles` row already using one of
these role names, so projects created before this feature shipped don't need
manual reconfiguration. (Status-assignment rules are gated on the existing
`tasks.read`/`tasks.write` keys instead — no dedicated permission key of
their own; see the linked doc.)

## AI agent integration

Workflow management is exposed to AI agents as ordinary MCP tools in the
existing Paca MCP server (`apps/mcp/src/tools/workflow-tools.ts`) — the same
mechanism used for every other Paca resource (tasks, sprints, docs, etc.),
not a special-cased sandbox tool or a separate server. Tool availability is
gated the same way every other tool in that server is: by the calling
agent's own `workflows.read` / `workflows.write` project permissions
(`apps/mcp/src/permissions.ts`), resolved at MCP-session startup — there is
no separate per-agent capability flag.

Tools: `get_workflow`, `create_workflow`, `update_workflow`,
`delete_workflow`. Status-assignment rules have their own, separate tool set
(`list_status_assignment_rules`, `create_status_assignment_rule`, etc. — see
the linked doc) since they're a project-wide concern, not part of a
workflow's graph.

- `get_workflow` — pass `workflowId` for one workflow's full graph, or omit
  it to list workflows in the project (optionally filtered by `status`).
- `create_workflow` — creates the workflow and, in the same call, can build
  out its whole graph: `nodes`, `statusTransitions`, `edges`, plus an
  `activate` convenience flag.
- `update_workflow` — renames/describes, changes lifecycle `status`
  (`draft`/`active`/`archived`), and edits the graph via
  `nodes`/`statusTransitions`/`edges`, each taking `set` (or `add`, for
  edges) and `remove`.
- `delete_workflow` — unchanged.

Internal node/transition/edge UUIDs are not agent-facing at all. Nodes are
addressed by `taskId`, status transitions by `statusId`, and edges by a
`(sourceTaskId, targetTaskId)` pair — all values the agent already holds
from `list_tasks` / `list_task_statuses`, so it never needs a `get_workflow`
round-trip just to learn an ID before it can write. The MCP layer resolves
these to the real node/transition/edge IDs the REST API needs via one
`getWorkflow` fetch, safe because of the same DB uniqueness constraints
noted under [Data model](#data-model) (one node per task, one transition per
status, one edge per node pair). `create_workflow` still auto-seeds the
default status-transition chain the same way the HTTP API does;
`update_workflow`'s `statusTransitions.set` customizes it afterward.

Unlike the REST API (where `pos_x`/`pos_y` are optional and default to
`(0, 0)`), the MCP `nodes`/`nodes.set` item schema makes `posX`/`posY`
required — the agent, not the MCP layer, is the one who knows where a node
should sit, so it must always choose a position rather than relying on a
fallback. The description also leads with an "every entry needs posX AND
posY" callout at the top of `create_workflow`/`update_workflow`'s own
description text (not just inside the nested node-item schema), since an
agent that doesn't happen to inspect the nested schema closely would
otherwise omit them on its first attempt and only add them after a
validation-error retry.

The tool description asks for a specific layered layout, not just "don't
overlap": `posY` is the row/stage, matching the dependency order implied by
the `edges` the agent is declaring — tasks with no predecessors (done first)
go in the top row (`posY = 0`), and a task goes in a lower row than
everything it depends on, so the graph reads top-to-bottom in execution
order; `posX` is the lane *within* a row, so independent/parallel tasks at
the same stage share one `posY` but get different `posX` values, reading as
side-by-side columns. Minimum spacing (`RECOMMENDED_NODE_GAP_X`/`_Y` in
`workflow-orchestration.ts`) is 300px horizontally / 200px vertically
(canvas cards are a fixed 256px wide) — tuned by live feedback on the actual
rendered canvas. The two constants are defined once and interpolated into
both the tool-description text and `checkNodeSpacing`, so a future
adjustment is a single number, not a hunt across strings. The description
also tells the agent not to place a node's position on the straight line
between two other edge-connected nodes, so it doesn't visually sit on top
of an unrelated edge. `get_workflow` lists every existing node's
`(pos_x, pos_y)` in its text response (not just internally on the JSON
graph) specifically so an agent extending an existing workflow can see and
continue the established layout instead of guessing.

Because prose-only guidance kept being ignored (agents chose small,
evenly-spaced values like 200px regardless of the stated minimum),
`create_workflow`/`update_workflow` also run `checkNodeSpacing` after
applying a call's node positions: if any two nodes end up closer than the
minimum on BOTH axes at once, the tool response includes an explicit
warning naming the pair and the actual gap (e.g. "t1 (200, 0) and t2 (400,
0) — 200px apart horizontally, 0px apart vertically"). This is advisory
only — positions are still applied exactly as the agent requested, and nothing
is auto-corrected; it exists to give the agent concrete, hard-to-ignore
feedback instead of relying entirely on the tool description being read
carefully.

Within `create_workflow`/`update_workflow`, every entry in every list is
applied independently — one bad node/transition/edge doesn't block its
siblings; the response reports per-item outcomes so the agent can retry
just the failed piece. `update_workflow`'s `remove` operations are
deliberately more lenient than the REST layer: removing a taskId/statusId/
edge pair that doesn't currently resolve to anything is a no-op, not an
error (the REST endpoints themselves still 404 on an unknown ID) — this
makes it safe for an agent to resend the same `remove` list after a partial
failure. Graph edits work in both `draft` and `active`; only `archived`
locks them (see [Lifecycle](#lifecycle)) — an agent can reposition/add/
remove nodes, edges, and status transitions on a running workflow in a
single `update_workflow` call with no lifecycle juggling. `update_workflow`
still applies a requested `status: "draft"` revert *before* any graph edits
in the same call (e.g. to pause the engine while editing) and a requested
`status: "active"`/`"archived"` transition *after* them, and only if
nothing else in the call failed.

## Frontend

The visual builder lives at `apps/web/src/routes/_authenticated/projects/$projectId/automation/`
— a list page and a canvas builder page (`@xyflow/react`), reachable from the
"Workflow" item in the project sidebar's top-level "Automation" section
(a sibling of "Status Rules", not nested under project Settings).
`apps/web/src/lib/workflow-api.ts` is the API client.

The builder page layout is a persistent left sidebar (collapsible via a
toolbar toggle) next to the canvas, holding only
`workflow-status-transitions-panel.tsx` (the status-workflow chain editor:
next-status picker per project status, with a warning banner if the chain
doesn't have exactly one terminal/done status) — status-assignment rules
are configured entirely separately, from their own Automation → Status
Rules page, with no cross-reference from this builder. See
`apps/web/src/components/projects/automation/` for these, the canvas, the
per-node panel ("remove from workflow" — done status is not per-node), and
the task-picker components. Status-assignment rules have their own page
— see [status-assignment-rules.md](./status-assignment-rules.md#frontend).
