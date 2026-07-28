package workflowdom

import (
	"context"

	"github.com/google/uuid"
)

// Service defines automation-workflow use cases. All methods that take a
// projectID verify the workflow (or its node/edge/rule ancestor) actually
// belongs to that project before mutating anything.
type Service interface {
	ListWorkflows(ctx context.Context, projectID uuid.UUID, status *Status) ([]*Workflow, error)
	GetWorkflow(ctx context.Context, projectID, workflowID uuid.UUID) (*Graph, error)
	CreateWorkflow(ctx context.Context, in CreateWorkflowInput) (*Workflow, error)
	UpdateWorkflow(ctx context.Context, projectID, workflowID uuid.UUID, in UpdateWorkflowInput) (*Workflow, error)
	DeleteWorkflow(ctx context.Context, projectID, workflowID uuid.UUID) error

	Activate(ctx context.Context, projectID, workflowID uuid.UUID) (*Workflow, error)
	Archive(ctx context.Context, projectID, workflowID uuid.UUID) (*Workflow, error)
	RevertToDraft(ctx context.Context, projectID, workflowID uuid.UUID) (*Workflow, error)

	AddNode(ctx context.Context, projectID, workflowID uuid.UUID, in AddNodeInput) (*Node, error)
	UpdateNode(ctx context.Context, projectID, workflowID, nodeID uuid.UUID, in UpdateNodeInput) (*Node, error)
	RemoveNode(ctx context.Context, projectID, workflowID, nodeID uuid.UUID) error

	SetStatusTransition(ctx context.Context, projectID, workflowID uuid.UUID, in SetStatusTransitionInput) (*StatusTransition, error)
	RemoveStatusTransition(ctx context.Context, projectID, workflowID, transitionID uuid.UUID) error

	AddEdge(ctx context.Context, projectID, workflowID uuid.UUID, in AddEdgeInput) (*Edge, error)
	RemoveEdge(ctx context.Context, projectID, workflowID, edgeID uuid.UUID) error

	// ListWorkflowsForTask returns every workflow in projectID (any status)
	// that has a node wrapping taskID — used by the task detail view to show
	// which workflows a task belongs to.
	ListWorkflowsForTask(ctx context.Context, projectID, taskID uuid.UUID) ([]*Workflow, error)
}

// CreateWorkflowInput carries the fields required to create a draft workflow.
type CreateWorkflowInput struct {
	ProjectID   uuid.UUID
	Name        string
	Description string
	// CreatedBy is the authenticated actor's user UUID (not a
	// project_members.id) — the service resolves it to the actor's
	// project_members.id for this project before persisting.
	CreatedBy *uuid.UUID
	// AgentID is set when the request was authenticated as an AI agent (via
	// an agent API key), so CreatedBy resolves to the agent's own
	// project_members row instead of being looked up as a human user.
	AgentID *uuid.UUID
}

// UpdateWorkflowInput carries the mutable metadata fields of a workflow.
// Renaming/describing is allowed regardless of lifecycle status; the graph
// itself (nodes/edges/rules) is editable in draft and active, and locked
// only once the workflow is archived.
type UpdateWorkflowInput struct {
	Name        *string
	Description *string
}

// AddNodeInput carries the fields required to add a task as a node.
type AddNodeInput struct {
	TaskID uuid.UUID
	PosX   float64
	PosY   float64
}

// UpdateNodeInput carries the mutable fields of a node. PosX/PosY use plain
// optional pointers (position always has a value, so "provided or not" is
// the only distinction that matters).
type UpdateNodeInput struct {
	PosX *float64
	PosY *float64
}

// SetStatusTransitionInput carries the fields to create or update one of the
// workflow's status-transition ("status workflow") entries. If a transition
// for StatusID already exists on the workflow, its NextStatusID is updated
// in place; otherwise a new entry is created. NextStatusID nil marks
// StatusID as terminal (the workflow's done status).
type SetStatusTransitionInput struct {
	StatusID     uuid.UUID
	NextStatusID *uuid.UUID
}

// AddEdgeInput carries the fields required to link two nodes.
type AddEdgeInput struct {
	SourceNodeID uuid.UUID
	TargetNodeID uuid.UUID
}
