package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	taskdom "github.com/Paca-AI/api/internal/domain/task"
	workflowdom "github.com/Paca-AI/api/internal/domain/workflow"
	"github.com/Paca-AI/api/internal/events"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	workflowConsumerGroup = "api.workflow_engine"
	workflowReadBlock     = 5 * time.Second
	workflowReadCount     = 50
)

// workflowGraphReader is the minimal workflowdom.Repository surface the
// engine needs to walk the graph.
type workflowGraphReader interface {
	FindWorkflowByID(ctx context.Context, id uuid.UUID) (*workflowdom.Workflow, error)
	FindNodeByID(ctx context.Context, id uuid.UUID) (*workflowdom.Node, error)
	ListActiveNodesByTaskID(ctx context.Context, taskID uuid.UUID) ([]*workflowdom.Node, error)
	ListStatusTransitionsByWorkflow(ctx context.Context, workflowID uuid.UUID) ([]*workflowdom.StatusTransition, error)
	ListEdgesByWorkflow(ctx context.Context, workflowID uuid.UUID) ([]*workflowdom.Edge, error)
	ListIncomingEdges(ctx context.Context, targetNodeID uuid.UUID) ([]*workflowdom.Edge, error)
}

// workflowTaskReader is the minimal task-domain surface the engine needs to
// read authoritative task/status state (the activity stream payload only
// carries resolved status *names*, not IDs).
type workflowTaskReader interface {
	FindTaskByID(ctx context.Context, id uuid.UUID) (*taskdom.Task, error)
	FindTaskStatusByID(ctx context.Context, id uuid.UUID) (*taskdom.TaskStatus, error)
}

// statusRuleApplier is the minimal statusruledom.Service surface the
// workflow consumer needs to re-evaluate a downstream task's assignment
// once a predecessor completes — see tryFireEdge. Reassignment itself
// (looking up a matching status-assignment rule, updating the task,
// recording the activity, publishing the event) is NOT this package's
// concern anymore; it lives entirely behind this interface.
type statusRuleApplier interface {
	ApplyMatchingRule(ctx context.Context, projectID uuid.UUID, task *taskdom.Task, reason string, extra map[string]any) error
}

// WorkflowConsumer reads task-activity events from StreamTaskActivities and
// evaluates automation workflows whenever a task's status changes: once a
// node's task reaches the workflow's derived done status (see isNodeDone)
// — and, for nodes with multiple incoming edges, once ALL predecessors
// have — each downstream node's task is re-evaluated against the
// project-wide status-assignment-rule engine (statusRuleApplier), using its
// OWN current status. No status is changed on the downstream task; the
// engine may or may not reassign it depending on whether a rule matches.
//
// This is "event 2" (predecessor done) only — reassignment triggered
// directly by a task's own status change ("event 1") is handled entirely
// by the separate, project-wide status-rule consumer, independent of
// workflow/node membership. Because the workflow graph is a DAG (enforced
// at edge-creation time) and can't change while active, cascades are
// guaranteed to terminate.
type WorkflowConsumer struct {
	client       *redis.Client
	workflowRepo workflowGraphReader
	taskRepo     workflowTaskReader
	ruleApplier  statusRuleApplier
	log          *slog.Logger
	consumerName string
	stopCh       chan struct{}
	doneCh       chan struct{}
}

// NewWorkflowConsumer creates a consumer that is ready to be started.
func NewWorkflowConsumer(
	client *redis.Client,
	workflowRepo workflowGraphReader,
	taskRepo workflowTaskReader,
	ruleApplier statusRuleApplier,
	log *slog.Logger,
) *WorkflowConsumer {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = uuid.New().String()
	}
	return &WorkflowConsumer{
		client:       client,
		workflowRepo: workflowRepo,
		taskRepo:     taskRepo,
		ruleApplier:  ruleApplier,
		log:          log,
		consumerName: fmt.Sprintf("%s.%s", workflowConsumerGroup, hostname),
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
}

// Start creates the consumer group if needed and begins processing in a
// background goroutine. Call Stop to drain and exit cleanly.
func (c *WorkflowConsumer) Start(ctx context.Context) {
	if err := c.ensureGroup(ctx); err != nil {
		c.log.Warn("workflow consumer: could not create consumer group, will retry on first read", "err", err)
	}
	go c.run()
}

// ensureGroup creates the consumer group, tolerating the case where it
// already exists. Called from Start and, defensively, from run whenever a
// read fails with NOGROUP — e.g. because Redis was briefly unreachable when
// Start ran — so a transient failure at boot doesn't permanently disable the
// engine until the process is restarted.
func (c *WorkflowConsumer) ensureGroup(ctx context.Context) error {
	err := c.client.XGroupCreateMkStream(ctx, events.StreamTaskActivities, workflowConsumerGroup, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return err
	}
	return nil
}

// Stop signals the consumer to stop and waits for the goroutine to exit.
func (c *WorkflowConsumer) Stop() {
	close(c.stopCh)
	<-c.doneCh
}

func (c *WorkflowConsumer) run() {
	defer close(c.doneCh)
	c.log.Info("workflow consumer: started", "stream", events.StreamTaskActivities)

	c.processPending(context.Background())

	for {
		select {
		case <-c.stopCh:
			c.log.Info("workflow consumer: stopping")
			return
		default:
		}

		ctx, cancel := context.WithTimeout(context.Background(), workflowReadBlock+time.Second)
		msgs, err := c.client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    workflowConsumerGroup,
			Consumer: c.consumerName,
			Streams:  []string{events.StreamTaskActivities, ">"},
			Count:    workflowReadCount,
			Block:    workflowReadBlock,
		}).Result()
		cancel()

		if err != nil {
			if err == redis.Nil {
				continue
			}
			c.log.Error("workflow consumer: xreadgroup error", "err", err)
			if strings.Contains(err.Error(), "NOGROUP") {
				if geErr := c.ensureGroup(context.Background()); geErr != nil {
					c.log.Warn("workflow consumer: failed to recreate consumer group", "err", geErr)
				}
			}
			time.Sleep(2 * time.Second)
			continue
		}

		for _, stream := range msgs {
			for _, msg := range stream.Messages {
				c.handle(msg)
			}
		}
	}
}

func (c *WorkflowConsumer) processPending(ctx context.Context) {
	msgs, err := c.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    workflowConsumerGroup,
		Consumer: c.consumerName,
		Streams:  []string{events.StreamTaskActivities, "0"},
		Count:    workflowReadCount,
	}).Result()
	if err != nil && err != redis.Nil {
		c.log.Warn("workflow consumer: could not read pending messages", "err", err)
		return
	}
	for _, stream := range msgs {
		for _, msg := range stream.Messages {
			c.handle(msg)
		}
	}
}

// workflowActivityStreamPayload mirrors the JSON shape produced by
// activityPayload() in activity_service.go — the same payload ActivityConsumer
// decodes, so only the fields this consumer needs are declared here.
type workflowActivityStreamPayload struct {
	TaskID       string `json:"task_id"`
	ProjectID    string `json:"project_id"`
	ActivityType string `json:"activity_type"`
	Content      string `json:"content"`
}

// taskUpdatedContent mirrors the {"changes": [...]} shape task_handler.go
// marshals into a task.updated activity's Content.
type taskUpdatedContent struct {
	Changes []taskdom.FieldChange `json:"changes"`
}

// evalCache memoizes repository lookups shared across the nodes and edges
// evaluated within a single processTaskStatusChange call, so a task
// belonging to several active-workflow nodes — or an AND-join with several
// predecessors — doesn't refetch the same workflow's transitions, or the
// same node/task row, once per node/edge.
//
// Status-assignment rules are not part of this cache at all anymore: they
// live entirely behind the statusRuleApplier interface (see tryFireEdge),
// which reads through its own cache layer (statusrulesvc.CachedRepository)
// invalidated on every write instead of trusting a TTL or an event boundary.
type evalCache struct {
	transitions map[uuid.UUID][]*workflowdom.StatusTransition
	edges       map[uuid.UUID][]*workflowdom.Edge
	nodes       map[uuid.UUID]*workflowdom.Node
	tasks       map[uuid.UUID]*taskdom.Task
}

func newEvalCache() *evalCache {
	return &evalCache{
		transitions: make(map[uuid.UUID][]*workflowdom.StatusTransition),
		edges:       make(map[uuid.UUID][]*workflowdom.Edge),
		nodes:       make(map[uuid.UUID]*workflowdom.Node),
		tasks:       make(map[uuid.UUID]*taskdom.Task),
	}
}

func (e *evalCache) getTransitions(ctx context.Context, repo workflowGraphReader, workflowID uuid.UUID) ([]*workflowdom.StatusTransition, error) {
	if t, ok := e.transitions[workflowID]; ok {
		return t, nil
	}
	t, err := repo.ListStatusTransitionsByWorkflow(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	e.transitions[workflowID] = t
	return t, nil
}

func (e *evalCache) getEdges(ctx context.Context, repo workflowGraphReader, workflowID uuid.UUID) ([]*workflowdom.Edge, error) {
	if edges, ok := e.edges[workflowID]; ok {
		return edges, nil
	}
	edges, err := repo.ListEdgesByWorkflow(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	e.edges[workflowID] = edges
	return edges, nil
}

func (e *evalCache) getNode(ctx context.Context, repo workflowGraphReader, id uuid.UUID) (*workflowdom.Node, error) {
	if n, ok := e.nodes[id]; ok {
		return n, nil
	}
	n, err := repo.FindNodeByID(ctx, id)
	if err != nil {
		return nil, err
	}
	e.nodes[id] = n
	return n, nil
}

func (e *evalCache) getTask(ctx context.Context, repo workflowTaskReader, id uuid.UUID) (*taskdom.Task, error) {
	if t, ok := e.tasks[id]; ok {
		return t, nil
	}
	t, err := repo.FindTaskByID(ctx, id)
	if err != nil {
		return nil, err
	}
	e.tasks[id] = t
	return t, nil
}

func (p workflowActivityStreamPayload) hasStatusChange() bool {
	var content taskUpdatedContent
	if err := json.Unmarshal([]byte(p.Content), &content); err != nil {
		return false
	}
	for _, c := range content.Changes {
		if c.Field == "status" {
			return true
		}
	}
	return false
}

func (c *WorkflowConsumer) handle(msg redis.XMessage) {
	ctx := context.Background()

	raw, ok := msg.Values["payload"].(string)
	if !ok {
		c.ack(ctx, msg.ID)
		return
	}

	var p workflowActivityStreamPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		c.log.Warn("workflow consumer: failed to decode payload", "id", msg.ID, "err", err)
		c.ack(ctx, msg.ID)
		return
	}

	// Only task.updated events with a status field change can affect a
	// workflow — everything else (comments, links, other field edits) is a
	// cheap no-op skip.
	if p.ActivityType != string(taskdom.ActivityTypeTaskUpdated) || !p.hasStatusChange() {
		c.ack(ctx, msg.ID)
		return
	}

	taskID, err := uuid.Parse(p.TaskID)
	if err != nil {
		c.log.Warn("workflow consumer: invalid task_id", "id", msg.ID)
		c.ack(ctx, msg.ID)
		return
	}
	projectID, err := uuid.Parse(p.ProjectID)
	if err != nil {
		c.log.Warn("workflow consumer: invalid project_id", "id", msg.ID)
		c.ack(ctx, msg.ID)
		return
	}

	if err := c.processTaskStatusChange(ctx, projectID, taskID); err != nil {
		c.log.Error("workflow consumer: failed to process task status change", "id", msg.ID, "task_id", taskID, "err", err)
		// Do not ack — retried via processPending on next restart.
		return
	}
	c.ack(ctx, msg.ID)
}

func (c *WorkflowConsumer) ack(ctx context.Context, id string) {
	if err := c.client.XAck(ctx, events.StreamTaskActivities, workflowConsumerGroup, id).Err(); err != nil {
		c.log.Warn("workflow consumer: xack failed", "id", id, "err", err)
	}
}

// processTaskStatusChange re-evaluates every active-workflow node that
// references taskID. Errors applying an individual node are logged and
// skipped (best-effort) rather than aborting the whole batch — a bug
// affecting one workflow shouldn't block others. An error is returned only
// when the foundational lookups (nodes, task) fail, so the message is
// retried.
func (c *WorkflowConsumer) processTaskStatusChange(ctx context.Context, projectID, taskID uuid.UUID) error {
	nodes, err := c.workflowRepo.ListActiveNodesByTaskID(ctx, taskID)
	if err != nil {
		return fmt.Errorf("list active nodes: %w", err)
	}
	if len(nodes) == 0 {
		return nil
	}

	task, err := c.taskRepo.FindTaskByID(ctx, taskID)
	if err != nil {
		return fmt.Errorf("find task: %w", err)
	}
	if task.StatusID == nil {
		return nil
	}

	cache := newEvalCache()
	cache.tasks[task.ID] = task
	for _, node := range nodes {
		if err := c.applyNode(ctx, projectID, node, task, cache); err != nil {
			c.log.Error("workflow consumer: failed to apply node", "node_id", node.ID, "workflow_id", node.WorkflowID, "task_id", taskID, "err", err)
		}
	}
	return nil
}

// applyNode runs event 1 (this node's own status rule) and, if the node just
// became done, fans out event 2 to its outgoing edges. task is shared with
// every other node evaluated for this same event (see cache): the shared
// rule-applier mutates a matched task's AssigneeIDs in place after a
// successful reassignment so a task wrapped by multiple active-workflow
// nodes/edges doesn't have a later node's idempotency check read a stale
// assignee.
func (c *WorkflowConsumer) applyNode(ctx context.Context, projectID uuid.UUID, node *workflowdom.Node, task *taskdom.Task, cache *evalCache) error {
	done, err := c.isNodeDone(ctx, node, task, cache)
	if err != nil {
		return fmt.Errorf("check done status: %w", err)
	}
	if !done {
		return nil
	}

	edges, err := cache.getEdges(ctx, c.workflowRepo, node.WorkflowID)
	if err != nil {
		return fmt.Errorf("list edges: %w", err)
	}
	for _, e := range edges {
		if e.SourceNodeID != node.ID {
			continue
		}
		if err := c.tryFireEdge(ctx, projectID, e, cache); err != nil {
			c.log.Error("workflow consumer: failed to fire edge", "edge_id", e.ID, "err", err)
		}
	}
	return nil
}

// isNodeDone reports whether task's current status equals node's workflow's
// derived done status — the one status-transition entry with no configured
// next status. If the chain has no unique terminal entry (shouldn't happen
// for an active workflow, since Activate enforces it, but handled
// defensively), the node is treated as not done rather than erroring.
func (c *WorkflowConsumer) isNodeDone(ctx context.Context, node *workflowdom.Node, task *taskdom.Task, cache *evalCache) (bool, error) {
	if task.StatusID == nil {
		return false, nil
	}
	transitions, err := cache.getTransitions(ctx, c.workflowRepo, node.WorkflowID)
	if err != nil {
		return false, err
	}
	doneStatusID, ok := workflowdom.DeriveDoneStatusID(transitions)
	if !ok {
		return false, nil
	}
	return *task.StatusID == doneStatusID, nil
}

// tryFireEdge evaluates the AND-join for edge's target node — ALL of the
// target's incoming edges must have a done source — and, if satisfied,
// re-evaluates the target task's assignment (event 2: "predecessor done")
// against the project-wide status-assignment-rule engine, using the
// target's own current status. No status is changed on the target task.
func (c *WorkflowConsumer) tryFireEdge(ctx context.Context, projectID uuid.UUID, edge *workflowdom.Edge, cache *evalCache) error {
	target, err := cache.getNode(ctx, c.workflowRepo, edge.TargetNodeID)
	if err != nil {
		return fmt.Errorf("find target node: %w", err)
	}

	incoming, err := c.workflowRepo.ListIncomingEdges(ctx, target.ID)
	if err != nil {
		return fmt.Errorf("list incoming edges: %w", err)
	}
	for _, in := range incoming {
		srcNode, err := cache.getNode(ctx, c.workflowRepo, in.SourceNodeID)
		if err != nil {
			return fmt.Errorf("find source node: %w", err)
		}
		srcTask, err := cache.getTask(ctx, c.taskRepo, srcNode.TaskID)
		if err != nil {
			return fmt.Errorf("find source task: %w", err)
		}
		done, err := c.isNodeDone(ctx, srcNode, srcTask, cache)
		if err != nil {
			return fmt.Errorf("check predecessor done: %w", err)
		}
		if !done {
			// AND-join not satisfied yet — this is not an error, just not
			// time to fire. It will be re-evaluated when the remaining
			// predecessor(s) also reach done.
			return nil
		}
	}

	targetTask, err := cache.getTask(ctx, c.taskRepo, target.TaskID)
	if err != nil {
		return fmt.Errorf("find target task: %w", err)
	}
	if targetTask.StatusID == nil {
		return nil
	}

	// Fetched fresh (not through evalCache) despite the workflow possibly
	// already being read earlier in this same event's fan-out: this check
	// gates the mutating call just below, and a single event can cascade
	// across many nodes/edges (an AND-join). Caching it for the whole event
	// would let a workflow archived mid-fan-out keep triggering
	// reassignment for every edge evaluated after the cache was first
	// populated.
	workflow, err := c.workflowRepo.FindWorkflowByID(ctx, target.WorkflowID)
	if err != nil {
		return fmt.Errorf("find workflow: %w", err)
	}
	if workflow.Status != workflowdom.StatusActive {
		// The workflow was archived or reverted to draft after this event
		// started evaluating — don't complete an automation action against
		// it. Not an error: the message is simply done being processed.
		return nil
	}

	extra := map[string]any{
		"workflow_id":   workflow.ID.String(),
		"workflow_name": workflow.Name,
	}
	if transitions, err := cache.getTransitions(ctx, c.workflowRepo, target.WorkflowID); err == nil {
		for _, tr := range transitions {
			if tr.StatusID == *targetTask.StatusID && tr.NextStatusID != nil {
				if status, err := c.taskRepo.FindTaskStatusByID(ctx, *tr.NextStatusID); err == nil {
					extra["next_status_name"] = status.Name
				}
				break
			}
		}
	}

	return c.ruleApplier.ApplyMatchingRule(ctx, projectID, targetTask, "predecessor_done", extra)
}
