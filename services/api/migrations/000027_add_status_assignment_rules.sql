-- 000027_add_status_assignment_rules.sql
-- Splits status->assignee "rules" out of the automation-workflow feature
-- into their own project-scoped, project-WIDE concept: a rule says "when a
-- task's status becomes X (and, optionally, the task matches these field
-- filters), assign it to member M" — for ANY task in the project, not just
-- tasks wired into a workflow canvas. Filters are stored as a single JSONB
-- blob (mirroring sprint_views.config) rather than a normalized filter-row
-- table. Several rules may target the same status; `priority` (lower
-- first) breaks ties when more than one matches the same task, and the
-- first ENABLED match wins.
--
-- Automation workflows keep their dependency-graph purpose
-- (nodes/edges/status-transition chain) but no longer own any assignee
-- logic — see docs/architecture/automation-workflows.md.

BEGIN;

CREATE TABLE IF NOT EXISTS status_assignment_rules (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id          UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name                VARCHAR(255) NOT NULL,
    status_id           UUID NOT NULL REFERENCES task_statuses(id) ON DELETE CASCADE,
    assignee_member_id  UUID NOT NULL REFERENCES project_members(id) ON DELETE CASCADE,
    filter              JSONB NOT NULL DEFAULT '{}',
    priority            INT NOT NULL DEFAULT 0,
    enabled             BOOLEAN NOT NULL DEFAULT TRUE,
    created_by          UUID REFERENCES project_members(id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Serves both ListRulesByProject (WHERE project_id = $1 ORDER BY status_id,
-- priority) and the hot ListEnabledRulesByProjectAndStatus read (WHERE
-- project_id = $1 AND status_id = $2 ORDER BY priority) as a prefix scan.
CREATE INDEX IF NOT EXISTS idx_status_assignment_rules_project_status
    ON status_assignment_rules (project_id, status_id, priority);

-- Best-effort carry-forward of existing per-workflow rules as unfiltered
-- project-wide rules: one per distinct (project, status, assignee) combo
-- (collapsing duplicates across sibling workflows in the same project),
-- filter = '{}' (applies to every task at that status, matching the old
-- rule's actual scope — it never had a filter either), priority assigned
-- by earliest creation. Guarded by to_regclass so re-running this file
-- after workflow_status_rules has already been dropped (see below) is a
-- no-op rather than an error — PL/pgSQL only resolves table names when a
-- statement actually executes, so the INSERT/DROP below are never reached
-- once the IF is false.
DO $$
BEGIN
    IF to_regclass('public.workflow_status_rules') IS NOT NULL THEN
        INSERT INTO status_assignment_rules
            (id, project_id, name, status_id, assignee_member_id, filter, priority, enabled, created_by, created_at, updated_at)
        SELECT
            gen_random_uuid(),
            src.project_id,
            'Migrated from workflow rule',
            src.status_id,
            src.assignee_member_id,
            '{}'::jsonb,
            (row_number() OVER (PARTITION BY src.project_id, src.status_id ORDER BY src.first_created_at) - 1)::int,
            TRUE,
            NULL,
            src.first_created_at,
            NOW()
        FROM (
            SELECT w.project_id, wsr.status_id, wsr.assignee_member_id, MIN(wsr.created_at) AS first_created_at
            FROM workflow_status_rules wsr
            JOIN workflows w ON w.id = wsr.workflow_id
            WHERE w.deleted_at IS NULL
            GROUP BY w.project_id, wsr.status_id, wsr.assignee_member_id
        ) src;

        DROP TABLE workflow_status_rules;
    END IF;
END $$;

COMMIT;
