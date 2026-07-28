package worker

import (
	"context"
	"io"
	"log/slog"
	"testing"

	taskdom "github.com/Paca-AI/api/internal/domain/task"
	workflowdom "github.com/Paca-AI/api/internal/domain/workflow"
	"github.com/google/uuid"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// isAssignedOnlyTo reports whether t's assignee set is exactly {member} —
// the shape a status rule's replace-all-assignees reassignment produces.
func isAssignedOnlyTo(t *taskdom.Task, member uuid.UUID) bool {
	return len(t.AssigneeIDs) == 1 && t.AssigneeIDs[0] == member
}

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

type fakeGraphStore struct {
	workflows   map[uuid.UUID]*workflowdom.Workflow
	nodes       map[uuid.UUID]*workflowdom.Node
	transitions map[uuid.UUID][]*workflowdom.StatusTransition // keyed by workflow ID
	edges       []*workflowdom.Edge

	transitionsCalls int // counts real ListStatusTransitionsByWorkflow calls, to assert evalCache hits

	findWorkflowCalls int
	// archiveAfterFindWorkflowCall, if non-zero, flips every workflow to
	// archived right after the Nth FindWorkflowByID call returns — used to
	// simulate a concurrent archive landing mid-way through one event's
	// node/edge fan-out.
	archiveAfterFindWorkflowCall int
}

func newFakeGraphStore() *fakeGraphStore {
	return &fakeGraphStore{
		workflows:   make(map[uuid.UUID]*workflowdom.Workflow),
		nodes:       make(map[uuid.UUID]*workflowdom.Node),
		transitions: make(map[uuid.UUID][]*workflowdom.StatusTransition),
	}
}

func (f *fakeGraphStore) FindWorkflowByID(_ context.Context, id uuid.UUID) (*workflowdom.Workflow, error) {
	w, ok := f.workflows[id]
	if !ok {
		return nil, workflowdom.ErrNotFound
	}
	f.findWorkflowCalls++
	cp := *w // snapshot: later archiving must not retroactively change what this call already returned
	if f.archiveAfterFindWorkflowCall != 0 && f.findWorkflowCalls == f.archiveAfterFindWorkflowCall {
		w.Status = workflowdom.StatusArchived
	}
	return &cp, nil
}

func (f *fakeGraphStore) FindNodeByID(_ context.Context, id uuid.UUID) (*workflowdom.Node, error) {
	n, ok := f.nodes[id]
	if !ok {
		return nil, workflowdom.ErrNodeNotFound
	}
	return n, nil
}

func (f *fakeGraphStore) ListActiveNodesByTaskID(_ context.Context, taskID uuid.UUID) ([]*workflowdom.Node, error) {
	var out []*workflowdom.Node
	for _, n := range f.nodes {
		if n.TaskID != taskID {
			continue
		}
		if w, ok := f.workflows[n.WorkflowID]; ok && w.Status == workflowdom.StatusActive {
			out = append(out, n)
		}
	}
	return out, nil
}

func (f *fakeGraphStore) ListStatusTransitionsByWorkflow(_ context.Context, workflowID uuid.UUID) ([]*workflowdom.StatusTransition, error) {
	f.transitionsCalls++
	return f.transitions[workflowID], nil
}

func (f *fakeGraphStore) ListEdgesByWorkflow(_ context.Context, workflowID uuid.UUID) ([]*workflowdom.Edge, error) {
	var out []*workflowdom.Edge
	for _, e := range f.edges {
		if e.WorkflowID == workflowID {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeGraphStore) ListIncomingEdges(_ context.Context, targetNodeID uuid.UUID) ([]*workflowdom.Edge, error) {
	var out []*workflowdom.Edge
	for _, e := range f.edges {
		if e.TargetNodeID == targetNodeID {
			out = append(out, e)
		}
	}
	return out, nil
}

// fakeTaskStore implements both workflowTaskReader and the task-update
// surface the fakeRuleApplier below delegates to.
type fakeTaskStore struct {
	tasks       map[uuid.UUID]*taskdom.Task
	statuses    map[uuid.UUID]*taskdom.TaskStatus
	updateCalls int
}

func newFakeTaskStore() *fakeTaskStore {
	return &fakeTaskStore{
		tasks:    make(map[uuid.UUID]*taskdom.Task),
		statuses: make(map[uuid.UUID]*taskdom.TaskStatus),
	}
}

func (f *fakeTaskStore) FindTaskByID(_ context.Context, id uuid.UUID) (*taskdom.Task, error) {
	t, ok := f.tasks[id]
	if !ok {
		return nil, taskdom.ErrTaskNotFound
	}
	cp := *t
	return &cp, nil
}

func (f *fakeTaskStore) FindTaskStatusByID(_ context.Context, id uuid.UUID) (*taskdom.TaskStatus, error) {
	s, ok := f.statuses[id]
	if !ok {
		return nil, taskdom.ErrStatusNotFound
	}
	return s, nil
}

func (f *fakeTaskStore) UpdateTask(_ context.Context, _, id uuid.UUID, in taskdom.UpdateTaskInput) (*taskdom.Task, error) {
	f.updateCalls++
	t, ok := f.tasks[id]
	if !ok {
		return nil, taskdom.ErrTaskNotFound
	}
	if in.AssigneeIDs != nil {
		t.AssigneeIDs = *in.AssigneeIDs
	}
	cp := *t
	return &cp, nil
}

// fakeRuleApplier is a minimal stand-in for statusrulesvc.Service, the real
// implementation of the statusRuleApplier interface WorkflowConsumer calls
// once an edge's AND-join is satisfied (event 2: "predecessor done"). Real
// filter-matching/priority resolution is entirely statusrulesvc's concern
// now (tested there) — this fake only needs a single unfiltered
// statusID->memberID mapping to exercise WHEN/WHETHER WorkflowConsumer calls
// it, mirroring the real service's "mutate task in place + persist via
// UpdateTask" contract so existing assertions on f.tasks keep working.
type fakeRuleApplier struct {
	tasks *fakeTaskStore
	rules map[uuid.UUID]uuid.UUID // statusID -> memberID
	calls int
}

func newFakeRuleApplier(tasks *fakeTaskStore) *fakeRuleApplier {
	return &fakeRuleApplier{tasks: tasks, rules: make(map[uuid.UUID]uuid.UUID)}
}

func (f *fakeRuleApplier) ApplyMatchingRule(ctx context.Context, projectID uuid.UUID, task *taskdom.Task, _ string, _ map[string]any) error {
	f.calls++
	if task.StatusID == nil {
		return nil
	}
	member, ok := f.rules[*task.StatusID]
	if !ok {
		return nil
	}
	if len(task.AssigneeIDs) == 1 && task.AssigneeIDs[0] == member {
		return nil // idempotent no-op, mirrors the real service
	}
	newIDs := []uuid.UUID{member}
	if _, err := f.tasks.UpdateTask(ctx, projectID, task.ID, taskdom.UpdateTaskInput{AssigneeIDs: &newIDs}); err != nil {
		return err
	}
	task.AssigneeIDs = newIDs
	return nil
}

// ---------------------------------------------------------------------------
// Test fixture
// ---------------------------------------------------------------------------

type engineFixture struct {
	graph       *fakeGraphStore
	tasks       *fakeTaskStore
	ruleApplier *fakeRuleApplier
	consumer    *WorkflowConsumer

	projectID uuid.UUID
	doneStatus,
	readyStatus *taskdom.TaskStatus
}

func newEngineFixture() *engineFixture {
	graph := newFakeGraphStore()
	tasks := newFakeTaskStore()
	ruleApplier := newFakeRuleApplier(tasks)
	projectID := uuid.New()

	doneStatus := &taskdom.TaskStatus{ID: uuid.New(), ProjectID: projectID, Name: "Done", Category: taskdom.StatusCategoryDone}
	readyStatus := &taskdom.TaskStatus{ID: uuid.New(), ProjectID: projectID, Name: "Ready", Category: taskdom.StatusCategoryTodo}
	tasks.statuses[doneStatus.ID] = doneStatus
	tasks.statuses[readyStatus.ID] = readyStatus

	return &engineFixture{
		graph:       graph,
		tasks:       tasks,
		ruleApplier: ruleApplier,
		projectID:   projectID,
		doneStatus:  doneStatus,
		readyStatus: readyStatus,
		consumer: &WorkflowConsumer{
			workflowRepo: graph,
			taskRepo:     tasks,
			ruleApplier:  ruleApplier,
			log:          discardLogger(),
		},
	}
}

func (f *engineFixture) addWorkflow() *workflowdom.Workflow {
	w := &workflowdom.Workflow{ID: uuid.New(), ProjectID: f.projectID, Status: workflowdom.StatusActive, Name: "wf"}
	f.graph.workflows[w.ID] = w
	return w
}

func (f *engineFixture) addNode(w *workflowdom.Workflow, statusID *uuid.UUID) (*workflowdom.Node, *taskdom.Task) {
	task := &taskdom.Task{ID: uuid.New(), ProjectID: f.projectID, StatusID: statusID}
	f.tasks.tasks[task.ID] = task
	node := &workflowdom.Node{ID: uuid.New(), WorkflowID: w.ID, TaskID: task.ID}
	f.graph.nodes[node.ID] = node
	return node, task
}

// addRule registers the fake rule-engine's stand-in for a single, unfiltered
// project-wide status-assignment rule: statusID -> memberID. Real rules are
// project-scoped, not workflow-scoped, so this takes no workflow argument.
func (f *engineFixture) addRule(statusID, memberID uuid.UUID) {
	f.ruleApplier.rules[statusID] = memberID
}

// addTransition sets, for the workflow as a whole, what status comes next
// after statusID. nextStatusID nil marks statusID as the workflow's
// terminal/done status.
func (f *engineFixture) addTransition(w *workflowdom.Workflow, statusID uuid.UUID, nextStatusID *uuid.UUID) {
	f.graph.transitions[w.ID] = append(f.graph.transitions[w.ID], &workflowdom.StatusTransition{
		ID: uuid.New(), WorkflowID: w.ID, StatusID: statusID, NextStatusID: nextStatusID,
	})
}

func (f *engineFixture) addEdge(w *workflowdom.Workflow, source, target *workflowdom.Node) {
	f.graph.edges = append(f.graph.edges, &workflowdom.Edge{
		ID: uuid.New(), WorkflowID: w.ID, SourceNodeID: source.ID, TargetNodeID: target.ID,
	})
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------
//
// Reassignment triggered directly by a task's own status change ("event 1")
// no longer belongs to WorkflowConsumer at all — it's handled by the
// separate, project-wide StatusRuleConsumer regardless of workflow/node
// membership. What remains here is "event 2" (predecessor done): once a
// node's task reaches its workflow's derived done status, downstream nodes
// across outgoing edges are re-evaluated against the injected
// statusRuleApplier.

func TestChain_SinglePredecessor_AssignsDownstreamOnDone(t *testing.T) {
	f := newEngineFixture()
	ctx := context.Background()
	w := f.addWorkflow()
	downstreamMember := uuid.New()

	nodeA, taskA := f.addNode(w, &f.doneStatus.ID)
	nodeB, taskB := f.addNode(w, &f.readyStatus.ID)
	f.addTransition(w, f.doneStatus.ID, nil) // doneStatus is this workflow's terminal/done status
	f.addRule(f.readyStatus.ID, downstreamMember)
	f.addEdge(w, nodeA, nodeB)

	if err := f.consumer.processTaskStatusChange(ctx, f.projectID, taskA.ID); err != nil {
		t.Fatalf("processTaskStatusChange: %v", err)
	}

	gotB := f.tasks.tasks[taskB.ID]
	if !isAssignedOnlyTo(gotB, downstreamMember) {
		t.Fatalf("expected downstream task assigned to %v, got %+v", downstreamMember, gotB.AssigneeIDs)
	}
}

func TestDiamond_ANDJoin_WaitsForAllPredecessors(t *testing.T) {
	f := newEngineFixture()
	ctx := context.Background()
	w := f.addWorkflow()
	downstreamMember := uuid.New()

	nodeA, taskA := f.addNode(w, &f.readyStatus.ID)
	nodeB, taskB := f.addNode(w, &f.readyStatus.ID)
	nodeC, taskC := f.addNode(w, &f.readyStatus.ID)
	f.addTransition(w, f.doneStatus.ID, nil) // doneStatus is this workflow's terminal/done status
	f.addRule(f.readyStatus.ID, downstreamMember)
	f.addEdge(w, nodeA, nodeC)
	f.addEdge(w, nodeB, nodeC)

	// A finishes, but B has not — C must NOT be assigned yet.
	taskA.StatusID = &f.doneStatus.ID
	if err := f.consumer.processTaskStatusChange(ctx, f.projectID, taskA.ID); err != nil {
		t.Fatalf("processTaskStatusChange(A): %v", err)
	}
	if got := f.tasks.tasks[taskC.ID]; len(got.AssigneeIDs) != 0 {
		t.Fatalf("expected C to remain unassigned while B is not done, got %+v", got.AssigneeIDs)
	}

	// B finishes too — NOW C should be assigned.
	taskB.StatusID = &f.doneStatus.ID
	if err := f.consumer.processTaskStatusChange(ctx, f.projectID, taskB.ID); err != nil {
		t.Fatalf("processTaskStatusChange(B): %v", err)
	}
	gotC := f.tasks.tasks[taskC.ID]
	if !isAssignedOnlyTo(gotC, downstreamMember) {
		t.Fatalf("expected C assigned to %v once both predecessors are done, got %+v", downstreamMember, gotC.AssigneeIDs)
	}
}

func TestIsNodeDone_DerivesFromWorkflowTransitions(t *testing.T) {
	f := newEngineFixture()
	ctx := context.Background()
	w := f.addWorkflow()
	f.addTransition(w, f.readyStatus.ID, &f.doneStatus.ID)
	f.addTransition(w, f.doneStatus.ID, nil) // terminal

	node, task := f.addNode(w, &f.readyStatus.ID)
	task.StatusID = &f.doneStatus.ID

	done, err := f.consumer.isNodeDone(ctx, node, task, newEvalCache())
	if err != nil {
		t.Fatalf("isNodeDone: %v", err)
	}
	if !done {
		t.Fatalf("expected node to be considered done once its task reaches the workflow's derived done status")
	}
}

func TestIsNodeDone_FalseWhenChainHasNoUniqueTerminal(t *testing.T) {
	f := newEngineFixture()
	ctx := context.Background()
	w := f.addWorkflow()
	// No transitions configured at all — nothing to derive a done status from.

	node, task := f.addNode(w, &f.readyStatus.ID)
	task.StatusID = &f.doneStatus.ID

	done, err := f.consumer.isNodeDone(ctx, node, task, newEvalCache())
	if err != nil {
		t.Fatalf("isNodeDone: %v", err)
	}
	if done {
		t.Fatalf("expected node to not be considered done when no unique terminal status is configured")
	}
}

// TestTryFireEdge_SkipsWhenWorkflowNoLongerActive guards against the engine
// completing a reassignment against a workflow that was archived (or
// reverted to draft) after its predecessor was found done but before the
// downstream rule-applier call runs.
func TestTryFireEdge_SkipsWhenWorkflowNoLongerActive(t *testing.T) {
	f := newEngineFixture()
	ctx := context.Background()
	w := f.addWorkflow()
	member := uuid.New()

	source, _ := f.addNode(w, &f.doneStatus.ID)
	target, task := f.addNode(w, &f.readyStatus.ID)
	f.addTransition(w, f.doneStatus.ID, nil) // doneStatus is this workflow's terminal/done status
	f.addRule(f.readyStatus.ID, member)
	f.addEdge(w, source, target)

	edges, err := f.graph.ListEdgesByWorkflow(ctx, w.ID)
	if err != nil || len(edges) != 1 {
		t.Fatalf("setup: expected exactly one edge, got %v (err=%v)", edges, err)
	}

	// Simulate archival landing in the window between the predecessor being
	// found done and tryFireEdge actually running.
	w.Status = workflowdom.StatusArchived

	if err := f.consumer.tryFireEdge(ctx, f.projectID, edges[0], newEvalCache()); err != nil {
		t.Fatalf("tryFireEdge: %v", err)
	}
	if f.ruleApplier.calls != 0 {
		t.Fatalf("expected no ApplyMatchingRule call once the workflow is no longer active, got %d", f.ruleApplier.calls)
	}
	if got := f.tasks.tasks[task.ID]; len(got.AssigneeIDs) != 0 {
		t.Fatalf("expected target to remain unassigned, got %+v", got.AssigneeIDs)
	}
}

// TestTryFireEdge_ArchivedMidFanOut_StopsLaterEdges guards against the
// workflow's active/archived status being memoized for the whole event: one
// done source can fan out across several outgoing edges in the same event,
// and each edge's tryFireEdge call must see the workflow's current status,
// not whatever the first edge in the fan-out saw.
func TestTryFireEdge_ArchivedMidFanOut_StopsLaterEdges(t *testing.T) {
	f := newEngineFixture()
	ctx := context.Background()
	w := f.addWorkflow()
	downstreamMember := uuid.New()

	source, taskSource := f.addNode(w, &f.doneStatus.ID)
	nodeB, taskB := f.addNode(w, &f.readyStatus.ID)
	nodeC, taskC := f.addNode(w, &f.readyStatus.ID)
	f.addTransition(w, f.doneStatus.ID, nil) // doneStatus is this workflow's terminal/done status
	f.addRule(f.readyStatus.ID, downstreamMember)
	f.addEdge(w, source, nodeB)
	f.addEdge(w, source, nodeC)

	// Simulate the workflow being archived (e.g. via the HTTP handler) right
	// after the first outgoing edge's gate check reads "active" but before
	// the second edge's gate check runs later in this same event's fan-out.
	f.graph.archiveAfterFindWorkflowCall = 1

	if err := f.consumer.processTaskStatusChange(ctx, f.projectID, taskSource.ID); err != nil {
		t.Fatalf("processTaskStatusChange: %v", err)
	}

	gotB := f.tasks.tasks[taskB.ID]
	if !isAssignedOnlyTo(gotB, downstreamMember) {
		t.Fatalf("expected the first edge's downstream reassignment to complete before the archive landed, got %+v", gotB.AssigneeIDs)
	}
	gotC := f.tasks.tasks[taskC.ID]
	if len(gotC.AssigneeIDs) != 0 {
		t.Fatalf("expected the second edge's downstream to NOT be reassigned once the workflow was archived mid-fan-out, got %v", gotC.AssigneeIDs)
	}
	if f.ruleApplier.calls != 1 {
		t.Fatalf("expected exactly 1 ApplyMatchingRule call (first edge only, before the archive), got %d", f.ruleApplier.calls)
	}
}

// TestDiamond_ANDJoin_CachesTransitionsAcrossPredecessors guards against the
// N+1 query pattern where checking an AND-join's predecessors re-fetches the
// same workflow's status transitions once per predecessor (plus once more
// for the node itself and once more for the next-status-name lookup).
func TestDiamond_ANDJoin_CachesTransitionsAcrossPredecessors(t *testing.T) {
	f := newEngineFixture()
	ctx := context.Background()
	w := f.addWorkflow()
	downstreamMember := uuid.New()

	nodeA, taskA := f.addNode(w, &f.doneStatus.ID)
	nodeB, _ := f.addNode(w, &f.doneStatus.ID)
	nodeC, _ := f.addNode(w, &f.readyStatus.ID)
	f.addTransition(w, f.doneStatus.ID, nil) // doneStatus is this workflow's terminal/done status
	f.addRule(f.readyStatus.ID, downstreamMember)
	f.addEdge(w, nodeA, nodeC)
	f.addEdge(w, nodeB, nodeC)

	// Both predecessors already done: a single status-change event on A
	// evaluates isNodeDone for A (directly) and for A and B again (inside
	// tryFireEdge's AND-join loop) — all against the same workflow — plus
	// tryFireEdge's next-status lookup for C. All of those should share one
	// real repository call.
	if err := f.consumer.processTaskStatusChange(ctx, f.projectID, taskA.ID); err != nil {
		t.Fatalf("processTaskStatusChange: %v", err)
	}

	if f.graph.transitionsCalls != 1 {
		t.Fatalf("expected the per-event cache to collapse repeated ListStatusTransitionsByWorkflow calls into 1, got %d", f.graph.transitionsCalls)
	}
}
