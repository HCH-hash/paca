package statusruledom

import (
	"context"

	"github.com/google/uuid"
)

// Repository defines persistence operations for the status-assignment-rule
// aggregate.
type Repository interface {
	CreateRule(ctx context.Context, r *StatusAssignmentRule) error
	FindRuleByID(ctx context.Context, id uuid.UUID) (*StatusAssignmentRule, error)
	// ListRulesByProject returns every rule in projectID, across all
	// statuses, ordered by (status_id, priority) — used by the settings UI.
	ListRulesByProject(ctx context.Context, projectID uuid.UUID) ([]*StatusAssignmentRule, error)
	// ListEnabledRulesByProjectAndStatus returns projectID's enabled rules
	// whose StatusID is statusID, ordered by Priority ascending — the hot
	// read on every status-change event.
	ListEnabledRulesByProjectAndStatus(ctx context.Context, projectID, statusID uuid.UUID) ([]*StatusAssignmentRule, error)
	UpdateRule(ctx context.Context, r *StatusAssignmentRule) error
	DeleteRule(ctx context.Context, id uuid.UUID) error
	// ReorderRules sets Priority to each ruleID's index in ruleIDs, which
	// must be exactly the current set of rule IDs for (projectID, statusID)
	// in some order (see ErrReorderInvalid).
	ReorderRules(ctx context.Context, projectID, statusID uuid.UUID, ruleIDs []uuid.UUID) error

	// StatusUsedByStatusRule reports whether statusID is the trigger status
	// of any rule, used to guard task-status deletion.
	StatusUsedByStatusRule(ctx context.Context, statusID uuid.UUID) (bool, error)
}
