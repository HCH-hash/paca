package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Paca-AI/api/internal/apierr"
	workflowdom "github.com/Paca-AI/api/internal/domain/workflow"
	"github.com/Paca-AI/api/internal/transport/http/dto"
	"github.com/Paca-AI/api/internal/transport/http/middleware"
	"github.com/Paca-AI/api/internal/transport/http/presenter"
)

// WorkflowHandler handles automation-workflow management endpoints.
type WorkflowHandler struct {
	svc workflowdom.Service
}

// NewWorkflowHandler returns a WorkflowHandler wired to the workflow service.
func NewWorkflowHandler(svc workflowdom.Service) *WorkflowHandler {
	return &WorkflowHandler{svc: svc}
}

// --- Workflows ----------------------------------------------------------------

// ListWorkflows handles GET /projects/:projectId/workflows.
func (h *WorkflowHandler) ListWorkflows(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}

	var status *workflowdom.Status
	if raw := r.URL.Query().Get("status"); raw != "" {
		s := workflowdom.Status(raw)
		if !workflowdom.ValidStatuses[s] {
			presenter.Error(w, r, apierr.New(apierr.CodeBadRequest, "invalid status filter"))
			return
		}
		status = &s
	}

	workflows, err := h.svc.ListWorkflows(r.Context(), projectID, status)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	resp := make([]dto.WorkflowResponse, 0, len(workflows))
	for _, wf := range workflows {
		resp = append(resp, dto.WorkflowFromEntity(wf))
	}
	presenter.OK(w, r, map[string]any{"items": resp})
}

// ListWorkflowsForTask handles GET /projects/:projectId/tasks/:taskId/workflows.
func (h *WorkflowHandler) ListWorkflowsForTask(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	taskID, err := parseTaskID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}

	workflows, err := h.svc.ListWorkflowsForTask(r.Context(), projectID, taskID)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	resp := make([]dto.WorkflowResponse, 0, len(workflows))
	for _, wf := range workflows {
		resp = append(resp, dto.WorkflowFromEntity(wf))
	}
	presenter.OK(w, r, map[string]any{"items": resp})
}

// CreateWorkflow handles POST /projects/:projectId/workflows.
func (h *WorkflowHandler) CreateWorkflow(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}

	var req dto.CreateWorkflowRequest
	if !middleware.BindJSON(w, r, &req) {
		return
	}

	var createdBy *uuid.UUID
	if actorID, ok := middleware.ActorIDFromContext(r.Context()); ok && actorID != uuid.Nil {
		createdBy = &actorID
	}
	var agentID *uuid.UUID
	if id, ok := middleware.AgentIDFromContext(r.Context()); ok && id != uuid.Nil {
		agentID = &id
	}

	wf, err := h.svc.CreateWorkflow(r.Context(), workflowdom.CreateWorkflowInput{
		ProjectID:   projectID,
		Name:        req.Name,
		Description: req.Description,
		CreatedBy:   createdBy,
		AgentID:     agentID,
	})
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	presenter.Created(w, r, dto.WorkflowFromEntity(wf))
}

// GetWorkflow handles GET /projects/:projectId/workflows/:workflowId.
func (h *WorkflowHandler) GetWorkflow(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	workflowID, err := parseWorkflowID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}

	graph, err := h.svc.GetWorkflow(r.Context(), projectID, workflowID)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	presenter.OK(w, r, dto.WorkflowGraphFromEntity(graph))
}

// UpdateWorkflow handles PATCH /projects/:projectId/workflows/:workflowId.
func (h *WorkflowHandler) UpdateWorkflow(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	workflowID, err := parseWorkflowID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}

	var req dto.UpdateWorkflowRequest
	if !middleware.BindJSON(w, r, &req) {
		return
	}

	wf, err := h.svc.UpdateWorkflow(r.Context(), projectID, workflowID, workflowdom.UpdateWorkflowInput{
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	presenter.OK(w, r, dto.WorkflowFromEntity(wf))
}

// DeleteWorkflow handles DELETE /projects/:projectId/workflows/:workflowId.
func (h *WorkflowHandler) DeleteWorkflow(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	workflowID, err := parseWorkflowID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	if err := h.svc.DeleteWorkflow(r.Context(), projectID, workflowID); err != nil {
		presenter.Error(w, r, err)
		return
	}
	presenter.NoContent(w)
}

// ActivateWorkflow handles POST /projects/:projectId/workflows/:workflowId/activate.
func (h *WorkflowHandler) ActivateWorkflow(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	workflowID, err := parseWorkflowID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	wf, err := h.svc.Activate(r.Context(), projectID, workflowID)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	presenter.OK(w, r, dto.WorkflowFromEntity(wf))
}

// ArchiveWorkflow handles POST /projects/:projectId/workflows/:workflowId/archive.
func (h *WorkflowHandler) ArchiveWorkflow(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	workflowID, err := parseWorkflowID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	wf, err := h.svc.Archive(r.Context(), projectID, workflowID)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	presenter.OK(w, r, dto.WorkflowFromEntity(wf))
}

// RevertWorkflowToDraft handles POST /projects/:projectId/workflows/:workflowId/revert-to-draft.
func (h *WorkflowHandler) RevertWorkflowToDraft(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	workflowID, err := parseWorkflowID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	wf, err := h.svc.RevertToDraft(r.Context(), projectID, workflowID)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	presenter.OK(w, r, dto.WorkflowFromEntity(wf))
}

// --- Nodes --------------------------------------------------------------------

// AddWorkflowNode handles POST /projects/:projectId/workflows/:workflowId/nodes.
func (h *WorkflowHandler) AddWorkflowNode(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	workflowID, err := parseWorkflowID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}

	var req dto.AddWorkflowNodeRequest
	if !middleware.BindJSON(w, r, &req) {
		return
	}
	if req.TaskID == uuid.Nil {
		presenter.Error(w, r, apierr.New(apierr.CodeBadRequest, "task_id is required"))
		return
	}

	n, err := h.svc.AddNode(r.Context(), projectID, workflowID, workflowdom.AddNodeInput{
		TaskID: req.TaskID,
		PosX:   req.PosX,
		PosY:   req.PosY,
	})
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	presenter.Created(w, r, dto.NodeFromEntity(n))
}

// UpdateWorkflowNode handles PATCH .../workflows/:workflowId/nodes/:nodeId.
func (h *WorkflowHandler) UpdateWorkflowNode(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	workflowID, err := parseWorkflowID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	nodeID, err := parseWorkflowNodeID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}

	var req dto.UpdateWorkflowNodeRequest
	if !middleware.BindJSON(w, r, &req) {
		return
	}

	n, err := h.svc.UpdateNode(r.Context(), projectID, workflowID, nodeID, workflowdom.UpdateNodeInput{
		PosX: req.PosX,
		PosY: req.PosY,
	})
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	presenter.OK(w, r, dto.NodeFromEntity(n))
}

// RemoveWorkflowNode handles DELETE .../workflows/:workflowId/nodes/:nodeId.
func (h *WorkflowHandler) RemoveWorkflowNode(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	workflowID, err := parseWorkflowID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	nodeID, err := parseWorkflowNodeID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	if err := h.svc.RemoveNode(r.Context(), projectID, workflowID, nodeID); err != nil {
		presenter.Error(w, r, err)
		return
	}
	presenter.NoContent(w)
}

// --- Status transitions ("status workflow") --------------------------------

// SetWorkflowStatusTransition handles POST .../workflows/:workflowId/status-transitions.
func (h *WorkflowHandler) SetWorkflowStatusTransition(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	workflowID, err := parseWorkflowID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}

	var req dto.SetWorkflowStatusTransitionRequest
	if !middleware.BindJSON(w, r, &req) {
		return
	}
	if req.StatusID == uuid.Nil {
		presenter.Error(w, r, apierr.New(apierr.CodeBadRequest, "status_id is required"))
		return
	}

	t, err := h.svc.SetStatusTransition(r.Context(), projectID, workflowID, workflowdom.SetStatusTransitionInput{
		StatusID:     req.StatusID,
		NextStatusID: req.NextStatusID,
	})
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	presenter.Created(w, r, dto.StatusTransitionFromEntity(t))
}

// RemoveWorkflowStatusTransition handles DELETE .../workflows/:workflowId/status-transitions/:transitionId.
func (h *WorkflowHandler) RemoveWorkflowStatusTransition(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	workflowID, err := parseWorkflowID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	transitionID, err := parseWorkflowStatusTransitionID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	if err := h.svc.RemoveStatusTransition(r.Context(), projectID, workflowID, transitionID); err != nil {
		presenter.Error(w, r, err)
		return
	}
	presenter.NoContent(w)
}

// --- Edges ----------------------------------------------------------------

// AddWorkflowEdge handles POST /projects/:projectId/workflows/:workflowId/edges.
func (h *WorkflowHandler) AddWorkflowEdge(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	workflowID, err := parseWorkflowID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}

	var req dto.AddWorkflowEdgeRequest
	if !middleware.BindJSON(w, r, &req) {
		return
	}
	if req.SourceNodeID == uuid.Nil || req.TargetNodeID == uuid.Nil {
		presenter.Error(w, r, apierr.New(apierr.CodeBadRequest, "source_node_id and target_node_id are required"))
		return
	}

	e, err := h.svc.AddEdge(r.Context(), projectID, workflowID, workflowdom.AddEdgeInput{
		SourceNodeID: req.SourceNodeID,
		TargetNodeID: req.TargetNodeID,
	})
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	presenter.Created(w, r, dto.EdgeFromEntity(e))
}

// RemoveWorkflowEdge handles DELETE .../workflows/:workflowId/edges/:edgeId.
func (h *WorkflowHandler) RemoveWorkflowEdge(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	workflowID, err := parseWorkflowID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	edgeID, err := parseWorkflowEdgeID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	if err := h.svc.RemoveEdge(r.Context(), projectID, workflowID, edgeID); err != nil {
		presenter.Error(w, r, err)
		return
	}
	presenter.NoContent(w)
}

// --- helpers ----------------------------------------------------------------

func parseWorkflowID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "workflowId"))
	if err != nil {
		return uuid.Nil, apierr.New(apierr.CodeBadRequest, "invalid workflow id")
	}
	return id, nil
}

func parseWorkflowNodeID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "nodeId"))
	if err != nil {
		return uuid.Nil, apierr.New(apierr.CodeBadRequest, "invalid node id")
	}
	return id, nil
}

func parseWorkflowStatusTransitionID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "transitionId"))
	if err != nil {
		return uuid.Nil, apierr.New(apierr.CodeBadRequest, "invalid status transition id")
	}
	return id, nil
}

func parseWorkflowEdgeID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "edgeId"))
	if err != nil {
		return uuid.Nil, apierr.New(apierr.CodeBadRequest, "invalid edge id")
	}
	return id, nil
}
