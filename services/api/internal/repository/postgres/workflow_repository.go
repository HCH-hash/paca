package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	workflowdom "github.com/Paca-AI/api/internal/domain/workflow"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// --- sqlx models -------------------------------------------------------------

type workflowRecord struct {
	ID          string     `db:"id"`
	ProjectID   string     `db:"project_id"`
	Name        string     `db:"name"`
	Description *string    `db:"description"`
	Status      string     `db:"status"`
	CreatedBy   *string    `db:"created_by"`
	CreatedAt   time.Time  `db:"created_at"`
	UpdatedAt   time.Time  `db:"updated_at"`
	DeletedAt   *time.Time `db:"deleted_at"`
}

type workflowNodeRecord struct {
	ID         string    `db:"id"`
	WorkflowID string    `db:"workflow_id"`
	TaskID     string    `db:"task_id"`
	PosX       float64   `db:"pos_x"`
	PosY       float64   `db:"pos_y"`
	CreatedAt  time.Time `db:"created_at"`
	UpdatedAt  time.Time `db:"updated_at"`
}

type workflowStatusTransitionRecord struct {
	ID           string    `db:"id"`
	WorkflowID   string    `db:"workflow_id"`
	StatusID     string    `db:"status_id"`
	NextStatusID *string   `db:"next_status_id"`
	CreatedAt    time.Time `db:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"`
}

type workflowEdgeRecord struct {
	ID           string    `db:"id"`
	WorkflowID   string    `db:"workflow_id"`
	SourceNodeID string    `db:"source_node_id"`
	TargetNodeID string    `db:"target_node_id"`
	CreatedAt    time.Time `db:"created_at"`
}

// WorkflowRepository is the sqlx implementation of workflowdom.Repository.
type WorkflowRepository struct {
	db *sqlx.DB
}

// NewWorkflowRepository returns a new WorkflowRepository.
func NewWorkflowRepository(db *sqlx.DB) *WorkflowRepository {
	return &WorkflowRepository{db: db}
}

// --- Workflow -----------------------------------------------------------------

// CreateWorkflow persists a new draft workflow.
func (r *WorkflowRepository) CreateWorkflow(ctx context.Context, w *workflowdom.Workflow) error {
	var createdBy *string
	if w.CreatedBy != nil {
		s := w.CreatedBy.String()
		createdBy = &s
	}
	var description *string
	if w.Description != "" {
		description = &w.Description
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO workflows (id, project_id, name, description, status, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		w.ID.String(), w.ProjectID.String(), w.Name, description, string(w.Status), createdBy, w.CreatedAt, w.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("workflow repo: create: %w", err)
	}
	return nil
}

// FindWorkflowByID fetches a workflow by its ID.
func (r *WorkflowRepository) FindWorkflowByID(ctx context.Context, id uuid.UUID) (*workflowdom.Workflow, error) {
	const q = `
		SELECT id, project_id, name, description, status, created_by, created_at, updated_at, deleted_at
		FROM workflows WHERE id = $1 AND deleted_at IS NULL`
	var rec workflowRecord
	if err := r.db.GetContext(ctx, &rec, q, id.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, workflowdom.ErrNotFound
		}
		return nil, err
	}
	return rec.toDomain()
}

// ListWorkflows returns a project's workflows, optionally filtered by status.
func (r *WorkflowRepository) ListWorkflows(ctx context.Context, projectID uuid.UUID, status *workflowdom.Status) ([]*workflowdom.Workflow, error) {
	q := `
		SELECT id, project_id, name, description, status, created_by, created_at, updated_at, deleted_at
		FROM workflows WHERE project_id = $1 AND deleted_at IS NULL`
	args := []interface{}{projectID.String()}
	if status != nil {
		q += ` AND status = $2`
		args = append(args, string(*status))
	}
	q += ` ORDER BY created_at DESC`

	var recs []workflowRecord
	if err := r.db.SelectContext(ctx, &recs, q, args...); err != nil {
		return nil, err
	}
	out := make([]*workflowdom.Workflow, 0, len(recs))
	for i := range recs {
		w, err := recs[i].toDomain()
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}

// UpdateWorkflow persists changes to a workflow's mutable fields.
func (r *WorkflowRepository) UpdateWorkflow(ctx context.Context, w *workflowdom.Workflow) error {
	var description *string
	if w.Description != "" {
		description = &w.Description
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE workflows SET name = $1, description = $2, status = $3, updated_at = $4
		WHERE id = $5 AND deleted_at IS NULL`,
		w.Name, description, string(w.Status), w.UpdatedAt, w.ID.String(),
	)
	if err != nil {
		return fmt.Errorf("workflow repo: update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return workflowdom.ErrNotFound
	}
	return nil
}

// DeleteWorkflow soft-deletes a workflow.
func (r *WorkflowRepository) DeleteWorkflow(ctx context.Context, id uuid.UUID) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE workflows SET deleted_at = $1 WHERE id = $2 AND deleted_at IS NULL`,
		time.Now(), id.String(),
	)
	if err != nil {
		return fmt.Errorf("workflow repo: delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return workflowdom.ErrNotFound
	}
	return nil
}

// --- Graph ----------------------------------------------------------------

// LoadGraph returns a workflow's full node/transition/edge set.
func (r *WorkflowRepository) LoadGraph(ctx context.Context, workflowID uuid.UUID) (*workflowdom.Graph, error) {
	w, err := r.FindWorkflowByID(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	nodes, err := r.ListNodesByWorkflow(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	edges, err := r.ListEdgesByWorkflow(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	transitions, err := r.ListStatusTransitionsByWorkflow(ctx, workflowID)
	if err != nil {
		return nil, err
	}

	return &workflowdom.Graph{
		Workflow:          w,
		Nodes:             nodes,
		StatusTransitions: transitions,
		Edges:             edges,
	}, nil
}

// --- Nodes ------------------------------------------------------------------

// CreateNode persists a new workflow node.
func (r *WorkflowRepository) CreateNode(ctx context.Context, n *workflowdom.Node) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO workflow_nodes (id, workflow_id, task_id, pos_x, pos_y, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		n.ID.String(), n.WorkflowID.String(), n.TaskID.String(), n.PosX, n.PosY, n.CreatedAt, n.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return workflowdom.ErrNodeDuplicateTask
		}
		return fmt.Errorf("workflow repo: create node: %w", err)
	}
	return nil
}

// FindNodeByID fetches a workflow node by its ID.
func (r *WorkflowRepository) FindNodeByID(ctx context.Context, id uuid.UUID) (*workflowdom.Node, error) {
	const q = `
		SELECT id, workflow_id, task_id, pos_x, pos_y, created_at, updated_at
		FROM workflow_nodes WHERE id = $1`
	var rec workflowNodeRecord
	if err := r.db.GetContext(ctx, &rec, q, id.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, workflowdom.ErrNodeNotFound
		}
		return nil, err
	}
	return rec.toDomain()
}

// ListNodesByWorkflow returns all nodes belonging to a workflow.
func (r *WorkflowRepository) ListNodesByWorkflow(ctx context.Context, workflowID uuid.UUID) ([]*workflowdom.Node, error) {
	const q = `
		SELECT id, workflow_id, task_id, pos_x, pos_y, created_at, updated_at
		FROM workflow_nodes WHERE workflow_id = $1 ORDER BY created_at ASC`
	var recs []workflowNodeRecord
	if err := r.db.SelectContext(ctx, &recs, q, workflowID.String()); err != nil {
		return nil, err
	}
	out := make([]*workflowdom.Node, 0, len(recs))
	for i := range recs {
		n, err := recs[i].toDomain()
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// UpdateNode persists changes to a node's position.
func (r *WorkflowRepository) UpdateNode(ctx context.Context, n *workflowdom.Node) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE workflow_nodes SET pos_x = $1, pos_y = $2, updated_at = $3
		WHERE id = $4`,
		n.PosX, n.PosY, n.UpdatedAt, n.ID.String(),
	)
	if err != nil {
		return fmt.Errorf("workflow repo: update node: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return workflowdom.ErrNodeNotFound
	}
	return nil
}

// DeleteNode removes a workflow node.
func (r *WorkflowRepository) DeleteNode(ctx context.Context, id uuid.UUID) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM workflow_nodes WHERE id = $1`, id.String())
	if err != nil {
		return fmt.Errorf("workflow repo: delete node: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return workflowdom.ErrNodeNotFound
	}
	return nil
}

// --- Status transitions -------------------------------------------------------

// CreateStatusTransition persists a new workflow status transition.
func (r *WorkflowRepository) CreateStatusTransition(ctx context.Context, st *workflowdom.StatusTransition) error {
	var nextStatusID *string
	if st.NextStatusID != nil {
		s := st.NextStatusID.String()
		nextStatusID = &s
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO workflow_status_transitions (id, workflow_id, status_id, next_status_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		st.ID.String(), st.WorkflowID.String(), st.StatusID.String(), nextStatusID, st.CreatedAt, st.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return workflowdom.ErrStatusTransitionConflict
		}
		return fmt.Errorf("workflow repo: create status transition: %w", err)
	}
	return nil
}

// FindStatusTransitionByID fetches a status transition by its ID.
func (r *WorkflowRepository) FindStatusTransitionByID(ctx context.Context, id uuid.UUID) (*workflowdom.StatusTransition, error) {
	const q = `
		SELECT id, workflow_id, status_id, next_status_id, created_at, updated_at
		FROM workflow_status_transitions WHERE id = $1`
	var rec workflowStatusTransitionRecord
	if err := r.db.GetContext(ctx, &rec, q, id.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, workflowdom.ErrStatusTransitionNotFound
		}
		return nil, err
	}
	return rec.toDomain()
}

// ListStatusTransitionsByWorkflow returns all status transitions belonging to a workflow.
func (r *WorkflowRepository) ListStatusTransitionsByWorkflow(ctx context.Context, workflowID uuid.UUID) ([]*workflowdom.StatusTransition, error) {
	const q = `
		SELECT id, workflow_id, status_id, next_status_id, created_at, updated_at
		FROM workflow_status_transitions WHERE workflow_id = $1 ORDER BY created_at ASC`
	var recs []workflowStatusTransitionRecord
	if err := r.db.SelectContext(ctx, &recs, q, workflowID.String()); err != nil {
		return nil, err
	}
	out := make([]*workflowdom.StatusTransition, 0, len(recs))
	for i := range recs {
		st, err := recs[i].toDomain()
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

// UpdateStatusTransition persists changes to a status transition's next status.
func (r *WorkflowRepository) UpdateStatusTransition(ctx context.Context, st *workflowdom.StatusTransition) error {
	var nextStatusID *string
	if st.NextStatusID != nil {
		s := st.NextStatusID.String()
		nextStatusID = &s
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE workflow_status_transitions SET next_status_id = $1, updated_at = $2 WHERE id = $3`,
		nextStatusID, st.UpdatedAt, st.ID.String(),
	)
	if err != nil {
		return fmt.Errorf("workflow repo: update status transition: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return workflowdom.ErrStatusTransitionNotFound
	}
	return nil
}

// DeleteStatusTransition removes a status transition.
func (r *WorkflowRepository) DeleteStatusTransition(ctx context.Context, id uuid.UUID) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM workflow_status_transitions WHERE id = $1`, id.String())
	if err != nil {
		return fmt.Errorf("workflow repo: delete status transition: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return workflowdom.ErrStatusTransitionNotFound
	}
	return nil
}

// --- Edges ----------------------------------------------------------------

// CreateEdge persists a new workflow edge.
func (r *WorkflowRepository) CreateEdge(ctx context.Context, e *workflowdom.Edge) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO workflow_edges (id, workflow_id, source_node_id, target_node_id, created_at)
		VALUES ($1, $2, $3, $4, $5)`,
		e.ID.String(), e.WorkflowID.String(), e.SourceNodeID.String(), e.TargetNodeID.String(), e.CreatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return workflowdom.ErrEdgeDuplicate
		}
		return fmt.Errorf("workflow repo: create edge: %w", err)
	}
	return nil
}

// FindEdgeByID fetches an edge by its ID.
func (r *WorkflowRepository) FindEdgeByID(ctx context.Context, id uuid.UUID) (*workflowdom.Edge, error) {
	const q = `
		SELECT id, workflow_id, source_node_id, target_node_id, created_at
		FROM workflow_edges WHERE id = $1`
	var rec workflowEdgeRecord
	if err := r.db.GetContext(ctx, &rec, q, id.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, workflowdom.ErrEdgeNotFound
		}
		return nil, err
	}
	return rec.toDomain()
}

// ListEdgesByWorkflow returns all edges belonging to a workflow.
func (r *WorkflowRepository) ListEdgesByWorkflow(ctx context.Context, workflowID uuid.UUID) ([]*workflowdom.Edge, error) {
	const q = `
		SELECT id, workflow_id, source_node_id, target_node_id, created_at
		FROM workflow_edges WHERE workflow_id = $1 ORDER BY created_at ASC`
	var recs []workflowEdgeRecord
	if err := r.db.SelectContext(ctx, &recs, q, workflowID.String()); err != nil {
		return nil, err
	}
	out := make([]*workflowdom.Edge, 0, len(recs))
	for i := range recs {
		e, err := recs[i].toDomain()
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// DeleteEdge removes a workflow edge.
func (r *WorkflowRepository) DeleteEdge(ctx context.Context, id uuid.UUID) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM workflow_edges WHERE id = $1`, id.String())
	if err != nil {
		return fmt.Errorf("workflow repo: delete edge: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return workflowdom.ErrEdgeNotFound
	}
	return nil
}

// --- Execution-engine read paths ---------------------------------------------

// ListActiveNodesByTaskID returns every node referencing taskID whose owning workflow is active.
func (r *WorkflowRepository) ListActiveNodesByTaskID(ctx context.Context, taskID uuid.UUID) ([]*workflowdom.Node, error) {
	const q = `
		SELECT n.id, n.workflow_id, n.task_id, n.pos_x, n.pos_y, n.created_at, n.updated_at
		FROM workflow_nodes n
		JOIN workflows w ON w.id = n.workflow_id
		WHERE n.task_id = $1 AND w.status = 'active' AND w.deleted_at IS NULL`
	var recs []workflowNodeRecord
	if err := r.db.SelectContext(ctx, &recs, q, taskID.String()); err != nil {
		return nil, err
	}
	out := make([]*workflowdom.Node, 0, len(recs))
	for i := range recs {
		n, err := recs[i].toDomain()
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// ListWorkflowsByTaskID returns every non-deleted workflow in projectID
// (regardless of status) that has a node wrapping taskID — for the
// task-detail "which workflows is this task in" display, not automation.
func (r *WorkflowRepository) ListWorkflowsByTaskID(ctx context.Context, projectID, taskID uuid.UUID) ([]*workflowdom.Workflow, error) {
	const q = `
		SELECT DISTINCT w.id, w.project_id, w.name, w.description, w.status, w.created_by, w.created_at, w.updated_at, w.deleted_at
		FROM workflows w
		JOIN workflow_nodes n ON n.workflow_id = w.id
		WHERE w.project_id = $1 AND n.task_id = $2 AND w.deleted_at IS NULL
		ORDER BY w.name ASC`
	var recs []workflowRecord
	if err := r.db.SelectContext(ctx, &recs, q, projectID.String(), taskID.String()); err != nil {
		return nil, err
	}
	out := make([]*workflowdom.Workflow, 0, len(recs))
	for i := range recs {
		w, err := recs[i].toDomain()
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}

// ListIncomingEdges returns every edge whose target is targetNodeID.
func (r *WorkflowRepository) ListIncomingEdges(ctx context.Context, targetNodeID uuid.UUID) ([]*workflowdom.Edge, error) {
	const q = `
		SELECT id, workflow_id, source_node_id, target_node_id, created_at
		FROM workflow_edges WHERE target_node_id = $1`
	var recs []workflowEdgeRecord
	if err := r.db.SelectContext(ctx, &recs, q, targetNodeID.String()); err != nil {
		return nil, err
	}
	out := make([]*workflowdom.Edge, 0, len(recs))
	for i := range recs {
		e, err := recs[i].toDomain()
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// StatusUsedByWorkflow reports whether statusID is referenced by any
// workflow's status-transition chain (as either the current or the next
// status), regardless of the workflow's lifecycle status — an archived
// workflow can be reverted to draft and reactivated, so a status it depends
// on must not be deletable out from under it.
func (r *WorkflowRepository) StatusUsedByWorkflow(ctx context.Context, statusID uuid.UUID) (bool, error) {
	const q = `
		SELECT EXISTS (
			SELECT 1 FROM workflow_status_transitions WHERE status_id = $1 OR next_status_id = $1
		)`
	var used bool
	if err := r.db.GetContext(ctx, &used, q, statusID.String()); err != nil {
		return false, fmt.Errorf("workflow repo: status used by workflow: %w", err)
	}
	return used, nil
}

// --- row-to-domain helpers --------------------------------------------------

func (rec *workflowRecord) toDomain() (*workflowdom.Workflow, error) {
	id, err := uuid.Parse(rec.ID)
	if err != nil {
		return nil, err
	}
	projectID, err := uuid.Parse(rec.ProjectID)
	if err != nil {
		return nil, err
	}
	var createdBy *uuid.UUID
	if rec.CreatedBy != nil {
		p, err := uuid.Parse(*rec.CreatedBy)
		if err != nil {
			return nil, err
		}
		createdBy = &p
	}
	description := ""
	if rec.Description != nil {
		description = *rec.Description
	}
	return &workflowdom.Workflow{
		ID:          id,
		ProjectID:   projectID,
		Name:        rec.Name,
		Description: description,
		Status:      workflowdom.Status(rec.Status),
		CreatedBy:   createdBy,
		CreatedAt:   rec.CreatedAt,
		UpdatedAt:   rec.UpdatedAt,
		DeletedAt:   rec.DeletedAt,
	}, nil
}

func (rec *workflowNodeRecord) toDomain() (*workflowdom.Node, error) {
	id, err := uuid.Parse(rec.ID)
	if err != nil {
		return nil, err
	}
	workflowID, err := uuid.Parse(rec.WorkflowID)
	if err != nil {
		return nil, err
	}
	taskID, err := uuid.Parse(rec.TaskID)
	if err != nil {
		return nil, err
	}
	return &workflowdom.Node{
		ID:         id,
		WorkflowID: workflowID,
		TaskID:     taskID,
		PosX:       rec.PosX,
		PosY:       rec.PosY,
		CreatedAt:  rec.CreatedAt,
		UpdatedAt:  rec.UpdatedAt,
	}, nil
}

func (rec *workflowStatusTransitionRecord) toDomain() (*workflowdom.StatusTransition, error) {
	id, err := uuid.Parse(rec.ID)
	if err != nil {
		return nil, err
	}
	workflowID, err := uuid.Parse(rec.WorkflowID)
	if err != nil {
		return nil, err
	}
	statusID, err := uuid.Parse(rec.StatusID)
	if err != nil {
		return nil, err
	}
	var nextStatusID *uuid.UUID
	if rec.NextStatusID != nil {
		p, err := uuid.Parse(*rec.NextStatusID)
		if err != nil {
			return nil, err
		}
		nextStatusID = &p
	}
	return &workflowdom.StatusTransition{
		ID:           id,
		WorkflowID:   workflowID,
		StatusID:     statusID,
		NextStatusID: nextStatusID,
		CreatedAt:    rec.CreatedAt,
		UpdatedAt:    rec.UpdatedAt,
	}, nil
}

func (rec *workflowEdgeRecord) toDomain() (*workflowdom.Edge, error) {
	id, err := uuid.Parse(rec.ID)
	if err != nil {
		return nil, err
	}
	workflowID, err := uuid.Parse(rec.WorkflowID)
	if err != nil {
		return nil, err
	}
	sourceID, err := uuid.Parse(rec.SourceNodeID)
	if err != nil {
		return nil, err
	}
	targetID, err := uuid.Parse(rec.TargetNodeID)
	if err != nil {
		return nil, err
	}
	return &workflowdom.Edge{
		ID:           id,
		WorkflowID:   workflowID,
		SourceNodeID: sourceID,
		TargetNodeID: targetID,
		CreatedAt:    rec.CreatedAt,
	}, nil
}
