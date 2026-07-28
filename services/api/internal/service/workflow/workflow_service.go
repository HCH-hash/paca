// Package workflowsvc implements workflowdom.Service.
package workflowsvc

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	projectdom "github.com/Paca-AI/api/internal/domain/project"
	taskdom "github.com/Paca-AI/api/internal/domain/task"
	workflowdom "github.com/Paca-AI/api/internal/domain/workflow"
	"github.com/Paca-AI/api/internal/events"
	"github.com/Paca-AI/api/internal/platform/messaging"
	"github.com/google/uuid"
)

// taskLookup is the minimal task-domain surface the workflow service needs.
type taskLookup interface {
	FindTaskByID(ctx context.Context, id uuid.UUID) (*taskdom.Task, error)
	FindTaskStatusByID(ctx context.Context, id uuid.UUID) (*taskdom.TaskStatus, error)
	ListTaskStatuses(ctx context.Context, projectID uuid.UUID) ([]*taskdom.TaskStatus, error)
}

// memberLookup is the minimal project-domain surface the workflow service needs.
type memberLookup interface {
	FindMemberByID(ctx context.Context, memberID uuid.UUID) (*projectdom.ProjectMember, error)
	// FindMemberByActor resolves an authenticated actor (user, or agent when
	// agentID is non-nil) to their project_members.id.
	FindMemberByActor(ctx context.Context, projectID, actorID uuid.UUID, agentID *uuid.UUID) (*projectdom.ProjectMember, error)
	// ListMembers returns all active members of a project.
	ListMembers(ctx context.Context, projectID uuid.UUID) ([]*projectdom.ProjectMember, error)
}

// Service implements workflowdom.Service.
type Service struct {
	repo       workflowdom.Repository
	taskRepo   taskLookup
	memberRepo memberLookup
	publisher  *messaging.Publisher
}

// New returns a Service backed by repo, taskRepo, and memberRepo.
// publisher may be nil; real-time events are then skipped silently.
func New(repo workflowdom.Repository, taskRepo taskLookup, memberRepo memberLookup, publisher *messaging.Publisher) *Service {
	return &Service{repo: repo, taskRepo: taskRepo, memberRepo: memberRepo, publisher: publisher}
}

// publish sends a real-time pub/sub notification for a workflow-graph
// change. Errors are silently swallowed so a messaging failure never blocks
// the primary HTTP response.
func (s *Service) publish(ctx context.Context, topic string, payload map[string]any) {
	if s.publisher == nil {
		return
	}
	_ = s.publisher.Publish(ctx, events.ChannelRealtime, map[string]any{
		"type":    topic,
		"payload": payload,
	})
}

// --- Workflow lifecycle -------------------------------------------------------

// ListWorkflows returns all workflows for a project, optionally filtered by status.
func (s *Service) ListWorkflows(ctx context.Context, projectID uuid.UUID, status *workflowdom.Status) ([]*workflowdom.Workflow, error) {
	return s.repo.ListWorkflows(ctx, projectID, status)
}

// ListWorkflowsForTask returns every workflow in projectID that has a node
// wrapping taskID, regardless of workflow status.
func (s *Service) ListWorkflowsForTask(ctx context.Context, projectID, taskID uuid.UUID) ([]*workflowdom.Workflow, error) {
	return s.repo.ListWorkflowsByTaskID(ctx, projectID, taskID)
}

// GetWorkflow returns the full graph (nodes/edges/rules) for a workflow.
func (s *Service) GetWorkflow(ctx context.Context, projectID, workflowID uuid.UUID) (*workflowdom.Graph, error) {
	w, err := s.findOwnedWorkflow(ctx, projectID, workflowID)
	if err != nil {
		return nil, err
	}
	graph, err := s.repo.LoadGraph(ctx, w.ID)
	if err != nil {
		return nil, err
	}
	graph.Workflow = w
	return graph, nil
}

// CreateWorkflow creates a new draft workflow and seeds its default
// status-transition chain and default status rules from the project's
// current task statuses and members.
func (s *Service) CreateWorkflow(ctx context.Context, in workflowdom.CreateWorkflowInput) (*workflowdom.Workflow, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, workflowdom.ErrNameInvalid
	}
	now := time.Now()
	w := &workflowdom.Workflow{
		ID:          uuid.New(),
		ProjectID:   in.ProjectID,
		Name:        name,
		Description: strings.TrimSpace(in.Description),
		Status:      workflowdom.StatusDraft,
		CreatedBy:   s.resolveMember(ctx, in.CreatedBy, in.AgentID, in.ProjectID),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.repo.CreateWorkflow(ctx, w); err != nil {
		return nil, err
	}
	s.seedDefaults(ctx, w)
	s.publish(ctx, events.TopicWorkflowCreated, map[string]any{
		"project_id":  w.ProjectID.String(),
		"workflow_id": w.ID.String(),
	})
	return w, nil
}

// seedDefaults auto-populates w's status-transition chain from the
// project's current task statuses. Best-effort: failures are swallowed
// rather than failing the whole CreateWorkflow call, since the workflow is
// still perfectly usable as an empty draft that the user can fix up via the
// inline, always-visible chain editor.
func (s *Service) seedDefaults(ctx context.Context, w *workflowdom.Workflow) {
	statuses, err := s.taskRepo.ListTaskStatuses(ctx, w.ProjectID)
	if err != nil || len(statuses) == 0 {
		return
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Position < statuses[j].Position })

	s.seedDefaultStatusTransitions(ctx, w, statuses)
}

// seedDefaultStatusTransitions chains statuses (already ordered by board
// position) sequentially, leaving the last (highest-position) status
// terminal/done.
func (s *Service) seedDefaultStatusTransitions(ctx context.Context, w *workflowdom.Workflow, statuses []*taskdom.TaskStatus) {
	now := time.Now()
	for i, st := range statuses {
		var next *uuid.UUID
		if i+1 < len(statuses) {
			id := statuses[i+1].ID
			next = &id
		}
		t := &workflowdom.StatusTransition{
			ID:           uuid.New(),
			WorkflowID:   w.ID,
			StatusID:     st.ID,
			NextStatusID: next,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		_ = s.repo.CreateStatusTransition(ctx, t)
	}
}

// UpdateWorkflow renames/describes a workflow. Allowed regardless of lifecycle status.
func (s *Service) UpdateWorkflow(ctx context.Context, projectID, workflowID uuid.UUID, in workflowdom.UpdateWorkflowInput) (*workflowdom.Workflow, error) {
	w, err := s.findOwnedWorkflow(ctx, projectID, workflowID)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, workflowdom.ErrNameInvalid
		}
		w.Name = name
	}
	if in.Description != nil {
		w.Description = strings.TrimSpace(*in.Description)
	}
	w.UpdatedAt = time.Now()
	if err := s.repo.UpdateWorkflow(ctx, w); err != nil {
		return nil, err
	}
	s.publish(ctx, events.TopicWorkflowUpdated, map[string]any{
		"project_id":  projectID.String(),
		"workflow_id": w.ID.String(),
	})
	return w, nil
}

// DeleteWorkflow soft-deletes a workflow.
func (s *Service) DeleteWorkflow(ctx context.Context, projectID, workflowID uuid.UUID) error {
	w, err := s.findOwnedWorkflow(ctx, projectID, workflowID)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteWorkflow(ctx, w.ID); err != nil {
		return err
	}
	s.publish(ctx, events.TopicWorkflowDeleted, map[string]any{
		"project_id":  projectID.String(),
		"workflow_id": w.ID.String(),
	})
	return nil
}

// Activate transitions a draft workflow to active, after validating the
// graph is safe to run: it has at least one node, remains a DAG, every
// node's task still exists in the project, and the workflow's
// status-transition chain has exactly one derivable done status.
func (s *Service) Activate(ctx context.Context, projectID, workflowID uuid.UUID) (*workflowdom.Workflow, error) {
	w, err := s.findOwnedWorkflow(ctx, projectID, workflowID)
	if err != nil {
		return nil, err
	}
	if w.Status != workflowdom.StatusDraft {
		return nil, workflowdom.ErrNotDraft
	}

	nodes, err := s.repo.ListNodesByWorkflow(ctx, w.ID)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, workflowdom.ErrActivateNoNodes
	}
	edges, err := s.repo.ListEdgesByWorkflow(ctx, w.ID)
	if err != nil {
		return nil, err
	}
	if hasCycle(nodes, edges) {
		return nil, workflowdom.ErrEdgeCycle
	}

	for _, n := range nodes {
		task, err := s.taskRepo.FindTaskByID(ctx, n.TaskID)
		if err != nil || task.ProjectID != projectID || task.DeletedAt != nil {
			return nil, workflowdom.ErrActivateTaskMissing
		}
	}

	transitions, err := s.repo.ListStatusTransitionsByWorkflow(ctx, w.ID)
	if err != nil {
		return nil, err
	}
	if _, ok := workflowdom.DeriveDoneStatusID(transitions); !ok {
		return nil, workflowdom.ErrActivateDoneStatusUndetermined
	}

	w.Status = workflowdom.StatusActive
	w.UpdatedAt = time.Now()
	if err := s.repo.UpdateWorkflow(ctx, w); err != nil {
		return nil, err
	}
	s.publish(ctx, events.TopicWorkflowActivated, map[string]any{
		"project_id":  projectID.String(),
		"workflow_id": w.ID.String(),
	})
	return w, nil
}

// Archive transitions an active workflow to archived, stopping the engine
// from evaluating it.
func (s *Service) Archive(ctx context.Context, projectID, workflowID uuid.UUID) (*workflowdom.Workflow, error) {
	w, err := s.findOwnedWorkflow(ctx, projectID, workflowID)
	if err != nil {
		return nil, err
	}
	if w.Status != workflowdom.StatusActive {
		return nil, workflowdom.ErrNotActive
	}
	w.Status = workflowdom.StatusArchived
	w.UpdatedAt = time.Now()
	if err := s.repo.UpdateWorkflow(ctx, w); err != nil {
		return nil, err
	}
	s.publish(ctx, events.TopicWorkflowArchived, map[string]any{
		"project_id":  projectID.String(),
		"workflow_id": w.ID.String(),
	})
	return w, nil
}

// RevertToDraft moves an active workflow back to draft, stopping the engine
// from evaluating it. Archived workflows cannot be reverted, since
// silently making an archived workflow editable again would be confusing —
// callers must delete it (or build a new one) instead.
func (s *Service) RevertToDraft(ctx context.Context, projectID, workflowID uuid.UUID) (*workflowdom.Workflow, error) {
	w, err := s.findOwnedWorkflow(ctx, projectID, workflowID)
	if err != nil {
		return nil, err
	}
	if w.Status == workflowdom.StatusDraft {
		return w, nil
	}
	if w.Status != workflowdom.StatusActive {
		return nil, workflowdom.ErrNotActive
	}
	w.Status = workflowdom.StatusDraft
	w.UpdatedAt = time.Now()
	if err := s.repo.UpdateWorkflow(ctx, w); err != nil {
		return nil, err
	}
	s.publish(ctx, events.TopicWorkflowRevertedToDraft, map[string]any{
		"project_id":  projectID.String(),
		"workflow_id": w.ID.String(),
	})
	return w, nil
}

// --- Nodes --------------------------------------------------------------------

// AddNode adds an existing task as a node in the workflow.
func (s *Service) AddNode(ctx context.Context, projectID, workflowID uuid.UUID, in workflowdom.AddNodeInput) (*workflowdom.Node, error) {
	w, err := s.requireEditableOwnedWorkflow(ctx, projectID, workflowID)
	if err != nil {
		return nil, err
	}

	task, err := s.taskRepo.FindTaskByID(ctx, in.TaskID)
	if err != nil {
		return nil, err
	}
	if task.ProjectID != projectID {
		return nil, workflowdom.ErrNodeTaskCrossProject
	}

	now := time.Now()
	n := &workflowdom.Node{
		ID:         uuid.New(),
		WorkflowID: w.ID,
		TaskID:     in.TaskID,
		PosX:       in.PosX,
		PosY:       in.PosY,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.repo.CreateNode(ctx, n); err != nil {
		return nil, err
	}
	s.publish(ctx, events.TopicWorkflowNodeAdded, map[string]any{
		"project_id":  projectID.String(),
		"workflow_id": w.ID.String(),
		"node_id":     n.ID.String(),
		"task_id":     n.TaskID.String(),
	})
	return n, nil
}

// UpdateNode updates a node's canvas position.
func (s *Service) UpdateNode(ctx context.Context, projectID, workflowID, nodeID uuid.UUID, in workflowdom.UpdateNodeInput) (*workflowdom.Node, error) {
	w, err := s.requireEditableOwnedWorkflow(ctx, projectID, workflowID)
	if err != nil {
		return nil, err
	}
	n, err := s.findOwnedNode(ctx, w.ID, nodeID)
	if err != nil {
		return nil, err
	}

	if in.PosX != nil {
		n.PosX = *in.PosX
	}
	if in.PosY != nil {
		n.PosY = *in.PosY
	}
	n.UpdatedAt = time.Now()
	if err := s.repo.UpdateNode(ctx, n); err != nil {
		return nil, err
	}
	s.publish(ctx, events.TopicWorkflowNodeUpdated, map[string]any{
		"project_id":  projectID.String(),
		"workflow_id": w.ID.String(),
		"node_id":     n.ID.String(),
	})
	return n, nil
}

// RemoveNode deletes a node (and its edges, via FK cascade).
func (s *Service) RemoveNode(ctx context.Context, projectID, workflowID, nodeID uuid.UUID) error {
	w, err := s.requireEditableOwnedWorkflow(ctx, projectID, workflowID)
	if err != nil {
		return err
	}
	n, err := s.findOwnedNode(ctx, w.ID, nodeID)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteNode(ctx, n.ID); err != nil {
		return err
	}
	s.publish(ctx, events.TopicWorkflowNodeRemoved, map[string]any{
		"project_id":  projectID.String(),
		"workflow_id": w.ID.String(),
		"node_id":     n.ID.String(),
	})
	return nil
}

// --- Status transitions ----------------------------------------------------

// SetStatusTransition creates or updates the workflow's "status workflow"
// entry for a status: which status should come next once a task reaches
// it. NextStatusID nil marks StatusID as terminal (the workflow's done
// status).
func (s *Service) SetStatusTransition(ctx context.Context, projectID, workflowID uuid.UUID, in workflowdom.SetStatusTransitionInput) (*workflowdom.StatusTransition, error) {
	w, err := s.requireEditableOwnedWorkflow(ctx, projectID, workflowID)
	if err != nil {
		return nil, err
	}

	if err := s.assertStatusInProject(ctx, projectID, in.StatusID, workflowdom.ErrStatusTransitionCrossProject); err != nil {
		return nil, err
	}
	if in.NextStatusID != nil {
		if err := s.assertStatusInProject(ctx, projectID, *in.NextStatusID, workflowdom.ErrStatusTransitionCrossProject); err != nil {
			return nil, err
		}
		if *in.NextStatusID == in.StatusID {
			return nil, workflowdom.ErrStatusTransitionSelfLoop
		}
	}

	// At most one retry: a concurrent SetStatusTransition call for the same
	// status can create the row between our list below and our create
	// further down; on that conflict, loop once more so the second pass
	// finds and updates the now-existing row instead of surfacing a raw
	// duplicate-key error from what is meant to behave as an upsert.
	for attempt := 0; attempt < 2; attempt++ {
		existing, err := s.repo.ListStatusTransitionsByWorkflow(ctx, w.ID)
		if err != nil {
			return nil, err
		}
		for _, t := range existing {
			if t.StatusID == in.StatusID {
				t.NextStatusID = in.NextStatusID
				t.UpdatedAt = time.Now()
				if err := s.repo.UpdateStatusTransition(ctx, t); err != nil {
					return nil, err
				}
				s.publish(ctx, events.TopicWorkflowStatusTransitionSet, map[string]any{
					"project_id":    projectID.String(),
					"workflow_id":   w.ID.String(),
					"transition_id": t.ID.String(),
					"status_id":     t.StatusID.String(),
				})
				return t, nil
			}
		}

		now := time.Now()
		t := &workflowdom.StatusTransition{
			ID:           uuid.New(),
			WorkflowID:   w.ID,
			StatusID:     in.StatusID,
			NextStatusID: in.NextStatusID,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := s.repo.CreateStatusTransition(ctx, t); err != nil {
			if errors.Is(err, workflowdom.ErrStatusTransitionConflict) && attempt == 0 {
				continue
			}
			return nil, err
		}
		s.publish(ctx, events.TopicWorkflowStatusTransitionSet, map[string]any{
			"project_id":    projectID.String(),
			"workflow_id":   w.ID.String(),
			"transition_id": t.ID.String(),
			"status_id":     t.StatusID.String(),
		})
		return t, nil
	}
	return nil, workflowdom.ErrStatusTransitionConflict
}

// RemoveStatusTransition deletes a status-transition entry from the workflow.
func (s *Service) RemoveStatusTransition(ctx context.Context, projectID, workflowID, transitionID uuid.UUID) error {
	w, err := s.requireEditableOwnedWorkflow(ctx, projectID, workflowID)
	if err != nil {
		return err
	}
	t, err := s.repo.FindStatusTransitionByID(ctx, transitionID)
	if err != nil {
		return err
	}
	if t.WorkflowID != w.ID {
		return workflowdom.ErrStatusTransitionNotFound
	}
	if err := s.repo.DeleteStatusTransition(ctx, t.ID); err != nil {
		return err
	}
	s.publish(ctx, events.TopicWorkflowStatusTransitionRemoved, map[string]any{
		"project_id":    projectID.String(),
		"workflow_id":   w.ID.String(),
		"transition_id": t.ID.String(),
	})
	return nil
}

// --- Edges ----------------------------------------------------------------

// AddEdge links two nodes, rejecting self-loops and anything that would
// create a cycle in the workflow's dependency graph.
func (s *Service) AddEdge(ctx context.Context, projectID, workflowID uuid.UUID, in workflowdom.AddEdgeInput) (*workflowdom.Edge, error) {
	w, err := s.requireEditableOwnedWorkflow(ctx, projectID, workflowID)
	if err != nil {
		return nil, err
	}
	if in.SourceNodeID == in.TargetNodeID {
		return nil, workflowdom.ErrEdgeSelfLoop
	}
	source, err := s.findOwnedNode(ctx, w.ID, in.SourceNodeID)
	if err != nil {
		return nil, err
	}
	target, err := s.findOwnedNode(ctx, w.ID, in.TargetNodeID)
	if err != nil {
		return nil, err
	}

	edges, err := s.repo.ListEdgesByWorkflow(ctx, w.ID)
	if err != nil {
		return nil, err
	}
	if wouldCreateCycle(edges, source.ID, target.ID) {
		return nil, workflowdom.ErrEdgeCycle
	}

	e := &workflowdom.Edge{
		ID:           uuid.New(),
		WorkflowID:   w.ID,
		SourceNodeID: source.ID,
		TargetNodeID: target.ID,
		CreatedAt:    time.Now(),
	}
	if err := s.repo.CreateEdge(ctx, e); err != nil {
		return nil, err
	}
	s.publish(ctx, events.TopicWorkflowEdgeAdded, map[string]any{
		"project_id":     projectID.String(),
		"workflow_id":    w.ID.String(),
		"edge_id":        e.ID.String(),
		"source_node_id": e.SourceNodeID.String(),
		"target_node_id": e.TargetNodeID.String(),
	})
	return e, nil
}

// RemoveEdge deletes an edge.
func (s *Service) RemoveEdge(ctx context.Context, projectID, workflowID, edgeID uuid.UUID) error {
	w, err := s.requireEditableOwnedWorkflow(ctx, projectID, workflowID)
	if err != nil {
		return err
	}
	e, err := s.repo.FindEdgeByID(ctx, edgeID)
	if err != nil {
		return err
	}
	if e.WorkflowID != w.ID {
		return workflowdom.ErrEdgeNotFound
	}
	if err := s.repo.DeleteEdge(ctx, e.ID); err != nil {
		return err
	}
	s.publish(ctx, events.TopicWorkflowEdgeRemoved, map[string]any{
		"project_id":  projectID.String(),
		"workflow_id": w.ID.String(),
		"edge_id":     e.ID.String(),
	})
	return nil
}

// --- helpers ----------------------------------------------------------------

func (s *Service) findOwnedWorkflow(ctx context.Context, projectID, workflowID uuid.UUID) (*workflowdom.Workflow, error) {
	w, err := s.repo.FindWorkflowByID(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	if w.ProjectID != projectID {
		return nil, workflowdom.ErrNotFound
	}
	return w, nil
}

// requireEditableOwnedWorkflow looks up an owned workflow and rejects graph
// mutations once it's archived. Draft and active workflows are both
// editable; the engine always reads the graph fresh per event with
// graceful fallbacks for missing rules/edges/transitions, so an in-flight
// edit to an active workflow degrades to a stale read on the current event
// rather than crashing or corrupting state.
func (s *Service) requireEditableOwnedWorkflow(ctx context.Context, projectID, workflowID uuid.UUID) (*workflowdom.Workflow, error) {
	w, err := s.findOwnedWorkflow(ctx, projectID, workflowID)
	if err != nil {
		return nil, err
	}
	if w.Status == workflowdom.StatusArchived {
		return nil, workflowdom.ErrArchived
	}
	return w, nil
}

func (s *Service) findOwnedNode(ctx context.Context, workflowID, nodeID uuid.UUID) (*workflowdom.Node, error) {
	n, err := s.repo.FindNodeByID(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if n.WorkflowID != workflowID {
		return nil, workflowdom.ErrNodeNotFound
	}
	return n, nil
}

// assertStatusInProject checks that statusID belongs to projectID, returning
// crossProjectErr (the caller's feature-specific sentinel) if not.
func (s *Service) assertStatusInProject(ctx context.Context, projectID, statusID uuid.UUID, crossProjectErr error) error {
	status, err := s.taskRepo.FindTaskStatusByID(ctx, statusID)
	if err != nil {
		return err
	}
	if status.ProjectID != projectID {
		return crossProjectErr
	}
	return nil
}

// resolveMember resolves an authenticated actor to their project_members.id
// for storage in Workflow.CreatedBy (which references project_members, not
// users/agents directly). When agentID is non-nil, it resolves to the
// agent's own member row instead of userID's (see FindMemberByActor).
// Returns nil (no error) when userID is nil or the actor can't be resolved
// as a member of this project — CreatedBy is purely informational, so a
// resolution failure shouldn't block the write.
func (s *Service) resolveMember(ctx context.Context, userID, agentID *uuid.UUID, projectID uuid.UUID) *uuid.UUID {
	if userID == nil {
		return nil
	}
	member, err := s.memberRepo.FindMemberByActor(ctx, projectID, *userID, agentID)
	if err != nil {
		return nil
	}
	return &member.ID
}

// wouldCreateCycle reports whether adding an edge sourceID -> targetID would
// create a cycle, i.e. whether targetID can already reach sourceID via
// existing edges.
func wouldCreateCycle(edges []*workflowdom.Edge, sourceID, targetID uuid.UUID) bool {
	adjacency := make(map[uuid.UUID][]uuid.UUID, len(edges))
	for _, e := range edges {
		adjacency[e.SourceNodeID] = append(adjacency[e.SourceNodeID], e.TargetNodeID)
	}
	visited := make(map[uuid.UUID]bool)
	var dfs func(uuid.UUID) bool
	dfs = func(node uuid.UUID) bool {
		if node == sourceID {
			return true
		}
		if visited[node] {
			return false
		}
		visited[node] = true
		for _, next := range adjacency[node] {
			if dfs(next) {
				return true
			}
		}
		return false
	}
	return dfs(targetID)
}

// hasCycle reports whether the given node/edge set contains any cycle at
// all, used as a defensive re-check at activation time.
func hasCycle(nodes []*workflowdom.Node, edges []*workflowdom.Edge) bool {
	adjacency := make(map[uuid.UUID][]uuid.UUID, len(edges))
	for _, e := range edges {
		adjacency[e.SourceNodeID] = append(adjacency[e.SourceNodeID], e.TargetNodeID)
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	state := make(map[uuid.UUID]int, len(nodes))
	var dfs func(uuid.UUID) bool
	dfs = func(node uuid.UUID) bool {
		state[node] = gray
		for _, next := range adjacency[node] {
			switch state[next] {
			case gray:
				return true
			case white:
				if dfs(next) {
					return true
				}
			}
		}
		state[node] = black
		return false
	}
	for _, n := range nodes {
		if state[n.ID] == white {
			if dfs(n.ID) {
				return true
			}
		}
	}
	return false
}
