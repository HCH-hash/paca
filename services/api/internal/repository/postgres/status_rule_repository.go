package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	statusruledom "github.com/Paca-AI/api/internal/domain/statusrule"
)

// --- sqlx models -------------------------------------------------------------

type statusRuleRecord struct {
	ID               string    `db:"id"`
	ProjectID        string    `db:"project_id"`
	Name             string    `db:"name"`
	StatusID         string    `db:"status_id"`
	AssigneeMemberID string    `db:"assignee_member_id"`
	Filter           []byte    `db:"filter"`
	Priority         int       `db:"priority"`
	Enabled          bool      `db:"enabled"`
	CreatedBy        *string   `db:"created_by"`
	CreatedAt        time.Time `db:"created_at"`
	UpdatedAt        time.Time `db:"updated_at"`
}

func (rec *statusRuleRecord) toDomain() (*statusruledom.StatusAssignmentRule, error) {
	id, err := uuid.Parse(rec.ID)
	if err != nil {
		return nil, err
	}
	projectID, err := uuid.Parse(rec.ProjectID)
	if err != nil {
		return nil, err
	}
	statusID, err := uuid.Parse(rec.StatusID)
	if err != nil {
		return nil, err
	}
	assigneeID, err := uuid.Parse(rec.AssigneeMemberID)
	if err != nil {
		return nil, err
	}
	var createdBy *uuid.UUID
	if rec.CreatedBy != nil {
		parsed, err := uuid.Parse(*rec.CreatedBy)
		if err != nil {
			return nil, err
		}
		createdBy = &parsed
	}
	var filter statusruledom.TaskFilterSpec
	if len(rec.Filter) > 0 {
		if err := json.Unmarshal(rec.Filter, &filter); err != nil {
			return nil, fmt.Errorf("status rule repo: unmarshal filter: %w", err)
		}
	}
	return &statusruledom.StatusAssignmentRule{
		ID:               id,
		ProjectID:        projectID,
		Name:             rec.Name,
		StatusID:         statusID,
		AssigneeMemberID: assigneeID,
		Filter:           filter,
		Priority:         rec.Priority,
		Enabled:          rec.Enabled,
		CreatedBy:        createdBy,
		CreatedAt:        rec.CreatedAt,
		UpdatedAt:        rec.UpdatedAt,
	}, nil
}

const statusRuleSelectCols = `id, project_id, name, status_id, assignee_member_id, filter, priority, enabled, created_by, created_at, updated_at`

// StatusRuleRepository is the sqlx implementation of statusruledom.Repository.
type StatusRuleRepository struct {
	db *sqlx.DB
}

// NewStatusRuleRepository returns a new StatusRuleRepository.
func NewStatusRuleRepository(db *sqlx.DB) *StatusRuleRepository {
	return &StatusRuleRepository{db: db}
}

// CreateRule persists a new status assignment rule.
func (r *StatusRuleRepository) CreateRule(ctx context.Context, rule *statusruledom.StatusAssignmentRule) error {
	filterBytes, err := json.Marshal(rule.Filter)
	if err != nil {
		return fmt.Errorf("status rule repo: marshal filter: %w", err)
	}
	var createdBy *string
	if rule.CreatedBy != nil {
		s := rule.CreatedBy.String()
		createdBy = &s
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO status_assignment_rules
			(id, project_id, name, status_id, assignee_member_id, filter, priority, enabled, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		rule.ID.String(), rule.ProjectID.String(), rule.Name, rule.StatusID.String(), rule.AssigneeMemberID.String(),
		filterBytes, rule.Priority, rule.Enabled, createdBy, rule.CreatedAt, rule.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("status rule repo: create: %w", err)
	}
	return nil
}

// FindRuleByID fetches a rule by its ID.
func (r *StatusRuleRepository) FindRuleByID(ctx context.Context, id uuid.UUID) (*statusruledom.StatusAssignmentRule, error) {
	const q = `SELECT ` + statusRuleSelectCols + ` FROM status_assignment_rules WHERE id = $1`
	var rec statusRuleRecord
	if err := r.db.GetContext(ctx, &rec, q, id.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, statusruledom.ErrNotFound
		}
		return nil, err
	}
	return rec.toDomain()
}

// ListRulesByProject returns every rule in projectID, ordered by
// (status, priority) — the shape the settings UI wants to render, grouped
// by trigger status.
func (r *StatusRuleRepository) ListRulesByProject(ctx context.Context, projectID uuid.UUID) ([]*statusruledom.StatusAssignmentRule, error) {
	const q = `
		SELECT ` + statusRuleSelectCols + ` FROM status_assignment_rules
		WHERE project_id = $1 ORDER BY status_id, priority ASC`
	return r.listRules(ctx, q, projectID.String())
}

// ListEnabledRulesByProjectAndStatus returns projectID's enabled rules
// targeting statusID, ordered by priority ascending.
func (r *StatusRuleRepository) ListEnabledRulesByProjectAndStatus(ctx context.Context, projectID, statusID uuid.UUID) ([]*statusruledom.StatusAssignmentRule, error) {
	const q = `
		SELECT ` + statusRuleSelectCols + ` FROM status_assignment_rules
		WHERE project_id = $1 AND status_id = $2 AND enabled = TRUE ORDER BY priority ASC`
	return r.listRules(ctx, q, projectID.String(), statusID.String())
}

func (r *StatusRuleRepository) listRules(ctx context.Context, q string, args ...any) ([]*statusruledom.StatusAssignmentRule, error) {
	var recs []statusRuleRecord
	if err := r.db.SelectContext(ctx, &recs, q, args...); err != nil {
		return nil, err
	}
	out := make([]*statusruledom.StatusAssignmentRule, 0, len(recs))
	for i := range recs {
		rule, err := recs[i].toDomain()
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, nil
}

// UpdateRule persists changes to a rule's name, assignee, filter, and/or
// enabled flag. StatusID is immutable and never updated.
func (r *StatusRuleRepository) UpdateRule(ctx context.Context, rule *statusruledom.StatusAssignmentRule) error {
	filterBytes, err := json.Marshal(rule.Filter)
	if err != nil {
		return fmt.Errorf("status rule repo: marshal filter: %w", err)
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE status_assignment_rules
		SET name = $1, assignee_member_id = $2, filter = $3, enabled = $4, updated_at = $5
		WHERE id = $6`,
		rule.Name, rule.AssigneeMemberID.String(), filterBytes, rule.Enabled, rule.UpdatedAt, rule.ID.String(),
	)
	if err != nil {
		return fmt.Errorf("status rule repo: update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return statusruledom.ErrNotFound
	}
	return nil
}

// DeleteRule removes a rule.
func (r *StatusRuleRepository) DeleteRule(ctx context.Context, id uuid.UUID) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM status_assignment_rules WHERE id = $1`, id.String())
	if err != nil {
		return fmt.Errorf("status rule repo: delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return statusruledom.ErrNotFound
	}
	return nil
}

// ReorderRules sets priority = index within ruleIDs for every rule
// targeting (projectID, statusID). ruleIDs must be exactly that status's
// current rule set (in any order) — see statusruledom.ErrReorderInvalid.
func (r *StatusRuleRepository) ReorderRules(ctx context.Context, projectID, statusID uuid.UUID, ruleIDs []uuid.UUID) error {
	return WithTx(ctx, r.db, func(tx *sqlx.Tx) error {
		var ids []string
		if err := tx.SelectContext(ctx, &ids, `
			SELECT id FROM status_assignment_rules WHERE project_id = $1 AND status_id = $2 FOR UPDATE`,
			projectID.String(), statusID.String()); err != nil {
			return fmt.Errorf("status rule repo: reorder (lock): %w", err)
		}

		if len(ids) != len(ruleIDs) {
			return statusruledom.ErrReorderInvalid
		}
		existing := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			existing[id] = struct{}{}
		}
		for _, id := range ruleIDs {
			if _, ok := existing[id.String()]; !ok {
				return statusruledom.ErrReorderInvalid
			}
		}

		for i, id := range ruleIDs {
			if _, err := tx.ExecContext(ctx, `
				UPDATE status_assignment_rules SET priority = $1, updated_at = NOW() WHERE id = $2`,
				i, id.String()); err != nil {
				return fmt.Errorf("status rule repo: reorder: %w", err)
			}
		}
		return nil
	})
}

// StatusUsedByStatusRule reports whether statusID is the trigger status of
// any rule, used to guard task-status deletion.
func (r *StatusRuleRepository) StatusUsedByStatusRule(ctx context.Context, statusID uuid.UUID) (bool, error) {
	const q = `SELECT EXISTS (SELECT 1 FROM status_assignment_rules WHERE status_id = $1)`
	var used bool
	if err := r.db.GetContext(ctx, &used, q, statusID.String()); err != nil {
		return false, fmt.Errorf("status rule repo: status used by rule: %w", err)
	}
	return used, nil
}
