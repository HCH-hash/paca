package statusruledom

import (
	"context"

	"github.com/google/uuid"

	taskdom "github.com/Paca-AI/api/internal/domain/task"
)

// Service defines status-assignment-rule use cases, plus the shared
// automation entrypoint (ApplyMatchingRule) used by both the project-wide
// status-change consumer and the automation-workflow predecessor-done
// cascade.
type Service interface {
	// ListRules returns every rule in projectID, across all statuses,
	// ordered by (status, priority).
	ListRules(ctx context.Context, projectID uuid.UUID) ([]*StatusAssignmentRule, error)
	CreateRule(ctx context.Context, in CreateRuleInput) (*StatusAssignmentRule, error)
	UpdateRule(ctx context.Context, projectID, ruleID uuid.UUID, in UpdateRuleInput) (*StatusAssignmentRule, error)
	DeleteRule(ctx context.Context, projectID, ruleID uuid.UUID) error
	// ReorderRules re-prioritizes every rule targeting (projectID, statusID):
	// ruleIDs must be exactly that status's existing rule IDs, in the new
	// desired order.
	ReorderRules(ctx context.Context, projectID, statusID uuid.UUID, ruleIDs []uuid.UUID) error

	// ApplyMatchingRule finds the highest-priority enabled rule targeting
	// task's current status whose filter matches task and, if its assignee
	// differs from task's current assignee(s), reassigns task through the
	// task service, records a status_rule.assigned activity, and publishes
	// an assignment-changed event. task.AssigneeIDs is mutated in place on a
	// successful reassignment so a caller fanning out across several
	// tasks/nodes in one event sees the update immediately. extra merges
	// additional caller-specific context (e.g. workflow_id, next_status_name
	// for the automation-workflow cascade) into the activity/event payload —
	// pass nil when there is none. A no-op (nil error) when task has no
	// status or no rule matches.
	ApplyMatchingRule(ctx context.Context, projectID uuid.UUID, task *taskdom.Task, reason string, extra map[string]any) error
}

// CreateRuleInput carries the fields required to create a rule. Priority is
// not settable here — a new rule is always appended to the end of its
// status's list (see ReorderRules to change ordering after creation).
type CreateRuleInput struct {
	ProjectID        uuid.UUID
	Name             string
	StatusID         uuid.UUID
	AssigneeMemberID uuid.UUID
	Filter           TaskFilterSpec
	Enabled          *bool // nil defaults to true
	// CreatedBy is the authenticated actor's user UUID (not a
	// project_members.id) — the service resolves it to the actor's
	// project_members.id for this project before persisting.
	CreatedBy *uuid.UUID
	// AgentID is set when the request was authenticated as an AI agent (via
	// an agent API key), so CreatedBy resolves to the agent's own
	// project_members row instead of being looked up as a human user.
	AgentID *uuid.UUID
}

// UpdateRuleInput carries the mutable fields of a rule. StatusID is
// intentionally not included — immutable after creation.
type UpdateRuleInput struct {
	Name             *string
	AssigneeMemberID *uuid.UUID
	Filter           *TaskFilterSpec
	Enabled          *bool
}
