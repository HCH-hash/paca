package dto

import (
	"time"

	workflowdom "github.com/Paca-AI/api/internal/domain/workflow"
	"github.com/google/uuid"
)

// --- Workflow DTOs ------------------------------------------------------------

// CreateWorkflowRequest is the body for POST /projects/:projectId/workflows.
type CreateWorkflowRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// UpdateWorkflowRequest is the body for PATCH /projects/:projectId/workflows/:workflowId.
type UpdateWorkflowRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// WorkflowResponse is the public representation of a workflow.
type WorkflowResponse struct {
	ID          uuid.UUID  `json:"id"`
	ProjectID   uuid.UUID  `json:"project_id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
	CreatedBy   *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// WorkflowFromEntity maps a domain Workflow to a WorkflowResponse DTO.
func WorkflowFromEntity(w *workflowdom.Workflow) WorkflowResponse {
	return WorkflowResponse{
		ID:          w.ID,
		ProjectID:   w.ProjectID,
		Name:        w.Name,
		Description: w.Description,
		Status:      string(w.Status),
		CreatedBy:   w.CreatedBy,
		CreatedAt:   w.CreatedAt,
		UpdatedAt:   w.UpdatedAt,
	}
}

// NodeResponse is the public representation of a workflow node.
type NodeResponse struct {
	ID         uuid.UUID `json:"id"`
	WorkflowID uuid.UUID `json:"workflow_id"`
	TaskID     uuid.UUID `json:"task_id"`
	PosX       float64   `json:"pos_x"`
	PosY       float64   `json:"pos_y"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// NodeFromEntity maps a domain Node to a NodeResponse DTO.
func NodeFromEntity(n *workflowdom.Node) NodeResponse {
	return NodeResponse{
		ID:         n.ID,
		WorkflowID: n.WorkflowID,
		TaskID:     n.TaskID,
		PosX:       n.PosX,
		PosY:       n.PosY,
		CreatedAt:  n.CreatedAt,
		UpdatedAt:  n.UpdatedAt,
	}
}

// StatusTransitionResponse is the public representation of one entry in the
// workflow's status-transition chain ("status workflow").
type StatusTransitionResponse struct {
	ID           uuid.UUID  `json:"id"`
	WorkflowID   uuid.UUID  `json:"workflow_id"`
	StatusID     uuid.UUID  `json:"status_id"`
	NextStatusID *uuid.UUID `json:"next_status_id,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// StatusTransitionFromEntity maps a domain StatusTransition to a
// StatusTransitionResponse DTO.
func StatusTransitionFromEntity(t *workflowdom.StatusTransition) StatusTransitionResponse {
	return StatusTransitionResponse{
		ID:           t.ID,
		WorkflowID:   t.WorkflowID,
		StatusID:     t.StatusID,
		NextStatusID: t.NextStatusID,
		CreatedAt:    t.CreatedAt,
		UpdatedAt:    t.UpdatedAt,
	}
}

// EdgeResponse is the public representation of a workflow edge.
type EdgeResponse struct {
	ID           uuid.UUID `json:"id"`
	WorkflowID   uuid.UUID `json:"workflow_id"`
	SourceNodeID uuid.UUID `json:"source_node_id"`
	TargetNodeID uuid.UUID `json:"target_node_id"`
	CreatedAt    time.Time `json:"created_at"`
}

// EdgeFromEntity maps a domain Edge to an EdgeResponse DTO.
func EdgeFromEntity(e *workflowdom.Edge) EdgeResponse {
	return EdgeResponse{
		ID:           e.ID,
		WorkflowID:   e.WorkflowID,
		SourceNodeID: e.SourceNodeID,
		TargetNodeID: e.TargetNodeID,
		CreatedAt:    e.CreatedAt,
	}
}

// WorkflowGraphResponse is the single-fetch response used to hydrate the
// canvas builder: the workflow plus all of its nodes, edges, and its
// status-transition chain.
type WorkflowGraphResponse struct {
	Workflow          WorkflowResponse           `json:"workflow"`
	Nodes             []NodeResponse             `json:"nodes"`
	Edges             []EdgeResponse             `json:"edges"`
	StatusTransitions []StatusTransitionResponse `json:"status_transitions"`
}

// WorkflowGraphFromEntity maps a domain Graph to a WorkflowGraphResponse DTO.
func WorkflowGraphFromEntity(g *workflowdom.Graph) WorkflowGraphResponse {
	nodes := make([]NodeResponse, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		nodes = append(nodes, NodeFromEntity(n))
	}
	edges := make([]EdgeResponse, 0, len(g.Edges))
	for _, e := range g.Edges {
		edges = append(edges, EdgeFromEntity(e))
	}
	transitions := make([]StatusTransitionResponse, 0, len(g.StatusTransitions))
	for _, t := range g.StatusTransitions {
		transitions = append(transitions, StatusTransitionFromEntity(t))
	}

	return WorkflowGraphResponse{
		Workflow:          WorkflowFromEntity(g.Workflow),
		Nodes:             nodes,
		Edges:             edges,
		StatusTransitions: transitions,
	}
}

// --- Node DTOs ------------------------------------------------------------

// AddWorkflowNodeRequest is the body for POST /projects/:projectId/workflows/:workflowId/nodes.
type AddWorkflowNodeRequest struct {
	TaskID uuid.UUID `json:"task_id"`
	PosX   float64   `json:"pos_x"`
	PosY   float64   `json:"pos_y"`
}

// UpdateWorkflowNodeRequest is the body for PATCH .../nodes/:nodeId.
type UpdateWorkflowNodeRequest struct {
	PosX *float64 `json:"pos_x"`
	PosY *float64 `json:"pos_y"`
}

// --- Status transition DTOs -------------------------------------------------

// SetWorkflowStatusTransitionRequest is the body for
// POST /projects/:projectId/workflows/:workflowId/status-transitions. This
// always fully specifies the desired entry, so NextStatusID is a plain
// nullable pointer: omitted or explicit null both mean "no next status" —
// StatusID becomes the workflow's terminal/done status.
type SetWorkflowStatusTransitionRequest struct {
	StatusID     uuid.UUID  `json:"status_id"`
	NextStatusID *uuid.UUID `json:"next_status_id,omitempty"`
}

// --- Edge DTOs --------------------------------------------------------------

// AddWorkflowEdgeRequest is the body for POST /projects/:projectId/workflows/:workflowId/edges.
type AddWorkflowEdgeRequest struct {
	SourceNodeID uuid.UUID `json:"source_node_id"`
	TargetNodeID uuid.UUID `json:"target_node_id"`
}
