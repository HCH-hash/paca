package workflowdom

import (
	"context"

	"github.com/google/uuid"
)

// Repository defines persistence operations for the workflow aggregate.
type Repository interface {
	CreateWorkflow(ctx context.Context, w *Workflow) error
	FindWorkflowByID(ctx context.Context, id uuid.UUID) (*Workflow, error)
	ListWorkflows(ctx context.Context, projectID uuid.UUID, status *Status) ([]*Workflow, error)
	// UpdateWorkflow persists changes to name, description, and/or status.
	UpdateWorkflow(ctx context.Context, w *Workflow) error
	// DeleteWorkflow soft-deletes a workflow (cascades nodes/edges/transitions via FK).
	DeleteWorkflow(ctx context.Context, id uuid.UUID) error

	// LoadGraph returns the full node/rule/transition/edge set for a
	// workflow in as few round trips as practical, for the single-fetch
	// canvas GET.
	LoadGraph(ctx context.Context, workflowID uuid.UUID) (*Graph, error)

	CreateNode(ctx context.Context, n *Node) error
	FindNodeByID(ctx context.Context, id uuid.UUID) (*Node, error)
	ListNodesByWorkflow(ctx context.Context, workflowID uuid.UUID) ([]*Node, error)
	// UpdateNode persists position changes.
	UpdateNode(ctx context.Context, n *Node) error
	DeleteNode(ctx context.Context, id uuid.UUID) error

	CreateStatusTransition(ctx context.Context, st *StatusTransition) error
	FindStatusTransitionByID(ctx context.Context, id uuid.UUID) (*StatusTransition, error)
	ListStatusTransitionsByWorkflow(ctx context.Context, workflowID uuid.UUID) ([]*StatusTransition, error)
	UpdateStatusTransition(ctx context.Context, st *StatusTransition) error
	DeleteStatusTransition(ctx context.Context, id uuid.UUID) error

	CreateEdge(ctx context.Context, e *Edge) error
	FindEdgeByID(ctx context.Context, id uuid.UUID) (*Edge, error)
	ListEdgesByWorkflow(ctx context.Context, workflowID uuid.UUID) ([]*Edge, error)
	DeleteEdge(ctx context.Context, id uuid.UUID) error

	// --- Execution-engine read paths (consumed by the Phase B worker) --------

	// ListActiveNodesByTaskID returns every node referencing taskID whose
	// owning workflow is currently active. This is the hot lookup made on
	// every task status-change event.
	ListActiveNodesByTaskID(ctx context.Context, taskID uuid.UUID) ([]*Node, error)
	// ListIncomingEdges returns every edge whose target is targetNodeID, used
	// to evaluate the AND-join condition before firing a cascade.
	ListIncomingEdges(ctx context.Context, targetNodeID uuid.UUID) ([]*Edge, error)

	// --- Read path for the task-detail "which workflows is this task in" UI --

	// ListWorkflowsByTaskID returns every non-deleted workflow in projectID
	// that has a node wrapping taskID, regardless of workflow status. Unlike
	// ListActiveNodesByTaskID, this is for display purposes, not automation.
	ListWorkflowsByTaskID(ctx context.Context, projectID, taskID uuid.UUID) ([]*Workflow, error)
}
