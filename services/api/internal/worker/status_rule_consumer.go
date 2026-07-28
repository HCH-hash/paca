package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	taskdom "github.com/Paca-AI/api/internal/domain/task"
	"github.com/Paca-AI/api/internal/events"
)

const (
	statusRuleConsumerGroup = "api.status_rule_engine"
	statusRuleReadBlock     = 5 * time.Second
	statusRuleReadCount     = 50
)

// statusRuleTaskReader is the minimal task-domain surface the consumer
// needs to read a task's authoritative current state (the activity stream
// payload only carries resolved status *names*, not IDs).
type statusRuleTaskReader interface {
	FindTaskByID(ctx context.Context, id uuid.UUID) (*taskdom.Task, error)
}

// statusRuleEngine is the minimal statusruledom.Service surface the
// consumer needs.
type statusRuleEngine interface {
	ApplyMatchingRule(ctx context.Context, projectID uuid.UUID, task *taskdom.Task, reason string, extra map[string]any) error
}

// StatusRuleConsumer reads task-activity events from StreamTaskActivities
// and, whenever ANY task in ANY project changes status, re-evaluates that
// task's assignment against the project-wide status-assignment-rule engine
// — "event 1" from the former automation-workflow engine, now applying to
// every task in the project rather than only tasks wired into a workflow
// canvas. It runs as its own independent consumer group on the same
// stream as WorkflowConsumer, which now only handles the graph-driven
// "predecessor done" cascade (event 2).
type StatusRuleConsumer struct {
	client       *redis.Client
	taskRepo     statusRuleTaskReader
	ruleSvc      statusRuleEngine
	log          *slog.Logger
	consumerName string
	stopCh       chan struct{}
	doneCh       chan struct{}
}

// NewStatusRuleConsumer creates a consumer that is ready to be started.
func NewStatusRuleConsumer(client *redis.Client, taskRepo statusRuleTaskReader, ruleSvc statusRuleEngine, log *slog.Logger) *StatusRuleConsumer {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = uuid.New().String()
	}
	return &StatusRuleConsumer{
		client:       client,
		taskRepo:     taskRepo,
		ruleSvc:      ruleSvc,
		log:          log,
		consumerName: fmt.Sprintf("%s.%s", statusRuleConsumerGroup, hostname),
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
}

// Start creates the consumer group if needed and begins processing in a
// background goroutine. Call Stop to drain and exit cleanly.
func (c *StatusRuleConsumer) Start(ctx context.Context) {
	if err := c.ensureGroup(ctx); err != nil {
		c.log.Warn("status rule consumer: could not create consumer group, will retry on first read", "err", err)
	}
	go c.run()
}

// ensureGroup creates the consumer group, tolerating the case where it
// already exists.
func (c *StatusRuleConsumer) ensureGroup(ctx context.Context) error {
	err := c.client.XGroupCreateMkStream(ctx, events.StreamTaskActivities, statusRuleConsumerGroup, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return err
	}
	return nil
}

// Stop signals the consumer to stop and waits for the goroutine to exit.
func (c *StatusRuleConsumer) Stop() {
	close(c.stopCh)
	<-c.doneCh
}

func (c *StatusRuleConsumer) run() {
	defer close(c.doneCh)
	c.log.Info("status rule consumer: started", "stream", events.StreamTaskActivities)

	c.processPending(context.Background())

	for {
		select {
		case <-c.stopCh:
			c.log.Info("status rule consumer: stopping")
			return
		default:
		}

		ctx, cancel := context.WithTimeout(context.Background(), statusRuleReadBlock+time.Second)
		msgs, err := c.client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    statusRuleConsumerGroup,
			Consumer: c.consumerName,
			Streams:  []string{events.StreamTaskActivities, ">"},
			Count:    statusRuleReadCount,
			Block:    statusRuleReadBlock,
		}).Result()
		cancel()

		if err != nil {
			if err == redis.Nil {
				continue
			}
			c.log.Error("status rule consumer: xreadgroup error", "err", err)
			if strings.Contains(err.Error(), "NOGROUP") {
				if geErr := c.ensureGroup(context.Background()); geErr != nil {
					c.log.Warn("status rule consumer: failed to recreate consumer group", "err", geErr)
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

func (c *StatusRuleConsumer) processPending(ctx context.Context) {
	msgs, err := c.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    statusRuleConsumerGroup,
		Consumer: c.consumerName,
		Streams:  []string{events.StreamTaskActivities, "0"},
		Count:    statusRuleReadCount,
	}).Result()
	if err != nil && err != redis.Nil {
		c.log.Warn("status rule consumer: could not read pending messages", "err", err)
		return
	}
	for _, stream := range msgs {
		for _, msg := range stream.Messages {
			c.handle(msg)
		}
	}
}

func (c *StatusRuleConsumer) handle(msg redis.XMessage) {
	ctx := context.Background()

	raw, ok := msg.Values["payload"].(string)
	if !ok {
		c.ack(ctx, msg.ID)
		return
	}

	var p workflowActivityStreamPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		c.log.Warn("status rule consumer: failed to decode payload", "id", msg.ID, "err", err)
		c.ack(ctx, msg.ID)
		return
	}

	// Only task.updated events with a status field change can affect a
	// rule — everything else (comments, links, other field edits) is a
	// cheap no-op skip.
	if p.ActivityType != string(taskdom.ActivityTypeTaskUpdated) || !p.hasStatusChange() {
		c.ack(ctx, msg.ID)
		return
	}

	taskID, err := uuid.Parse(p.TaskID)
	if err != nil {
		c.log.Warn("status rule consumer: invalid task_id", "id", msg.ID)
		c.ack(ctx, msg.ID)
		return
	}
	projectID, err := uuid.Parse(p.ProjectID)
	if err != nil {
		c.log.Warn("status rule consumer: invalid project_id", "id", msg.ID)
		c.ack(ctx, msg.ID)
		return
	}

	if err := c.processTaskStatusChange(ctx, projectID, taskID); err != nil {
		c.log.Error("status rule consumer: failed to process task status change", "id", msg.ID, "task_id", taskID, "err", err)
		// Do not ack — retried via processPending on next restart.
		return
	}
	c.ack(ctx, msg.ID)
}

func (c *StatusRuleConsumer) ack(ctx context.Context, id string) {
	if err := c.client.XAck(ctx, events.StreamTaskActivities, statusRuleConsumerGroup, id).Err(); err != nil {
		c.log.Warn("status rule consumer: xack failed", "id", id, "err", err)
	}
}

// processTaskStatusChange re-evaluates taskID's assignment against the
// project-wide rule engine — no node/graph lookup at all, unlike
// WorkflowConsumer, since this applies regardless of workflow membership.
func (c *StatusRuleConsumer) processTaskStatusChange(ctx context.Context, projectID, taskID uuid.UUID) error {
	task, err := c.taskRepo.FindTaskByID(ctx, taskID)
	if err != nil {
		return fmt.Errorf("find task: %w", err)
	}
	if task.StatusID == nil {
		return nil
	}
	return c.ruleSvc.ApplyMatchingRule(ctx, projectID, task, "status_rule", nil)
}
