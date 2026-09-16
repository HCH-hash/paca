package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	// EventsMaxLen is how many entries StreamAgentEvents keeps (XADD MAXLEN
	// ~). Nothing reads that stream (see its doc comment in topics.go) —
	// conversation history is served from Postgres — so a count cap loses
	// nothing and keeps a recent tail for anyone inspecting it by hand.
	EventsMaxLen = 1000
	// DefaultStreamRetention is how long an entry appended to
	// StreamAgentConversationStatus is kept when PACA_STREAM_RETENTION is not
	// set: 7 days, the same window services/api applies to its streams.
	DefaultStreamRetention = 7 * 24 * time.Hour
)

// Publisher writes to the three Valkey destinations a conversation's
// lifecycle needs — see topics.go's doc comments for what each one is
// actually for (verified against services/ai-agent's real Python source,
// not the docs, which conflated StreamAgentEvents with the realtime path).
//
// Neither stream grows without bound: StreamAgentEvents keeps its newest
// EventsMaxLen entries, and StreamAgentConversationStatus drops entries older
// than the retention window on every append (XADD MINID ~ <now-retention>).
// The status stream has readers (services/api's api.agent_queue and
// api.automation_engine groups), so it is cut by AGE, never by count: a group
// that falls behind still gets every entry of the window.
type Publisher struct {
	client    *redis.Client
	retention time.Duration    // status entries older than this are trimmed on append; 0 = keep all
	now       func() time.Time // the clock the retention cut is taken from (tests replace it)
}

// NewPublisher builds a Publisher writing through the given Valkey client.
// Its status stream keeps DefaultStreamRetention; see WithStreamRetention.
func NewPublisher(client *redis.Client) *Publisher {
	return &Publisher{client: client, retention: DefaultStreamRetention, now: time.Now}
}

// WithStreamRetention sets how long a StreamAgentConversationStatus entry is
// kept (the validated PACA_STREAM_RETENTION, see config.Load). Returns p for
// chaining.
func (p *Publisher) WithStreamRetention(retention time.Duration) *Publisher {
	p.retention = retention
	return p
}

// StreamMinID is the stream id below which every entry is older than
// retention at time now: "<unix ms of now-retention>-0". Stream ids start with
// the valkey server's clock in milliseconds, so XADD MINID with this id drops
// exactly the entries appended before now-retention.
func StreamMinID(now time.Time, retention time.Duration) string {
	return strconv.FormatInt(now.Add(-retention).UnixMilli(), 10) + "-0"
}

// eventArgs builds the XADD for one StreamAgentEvents entry: newest
// EventsMaxLen kept.
func eventArgs(fields map[string]any) *redis.XAddArgs {
	return &redis.XAddArgs{Stream: StreamAgentEvents, MaxLen: EventsMaxLen, Approx: true, Values: fields}
}

// statusArgs builds the XADD for one StreamAgentConversationStatus entry,
// with the retention cut.
func (p *Publisher) statusArgs(fields map[string]any) *redis.XAddArgs {
	args := &redis.XAddArgs{Stream: StreamAgentConversationStatus, Values: fields}
	if p.retention > 0 {
		now := time.Now()
		if p.now != nil {
			now = p.now()
		}
		args.MinID = StreamMinID(now, p.retention)
		args.Approx = true
	}
	return args
}

// PublishEvent appends one conversation event to the durable
// StreamAgentEvents log. payload should be the raw JSON of the underlying
// update (e.g. an acp.Event's Raw field) — passed through as-is, matching
// how executor.py's _persist_event forwards event.model_dump_json()
// untouched. The same XADD keeps the stream at its newest EventsMaxLen
// entries.
func (p *Publisher) PublishEvent(
	ctx context.Context,
	conversationID, projectID uuid.UUID,
	eventType, eventSource string,
	eventIndex int,
	payload []byte,
	status string,
) error {
	fields := map[string]any{
		"conversation_id": conversationID.String(),
		"event_type":      eventType,
		"event_source":    eventSource,
		"event_index":     eventIndex,
		"payload":         string(payload),
		"status":          status,
	}
	if projectID != uuid.Nil {
		fields["project_id"] = projectID.String()
	}

	if err := p.client.XAdd(ctx, eventArgs(fields)).Err(); err != nil {
		return fmt.Errorf("messaging: publish event for conversation %s: %w", conversationID, err)
	}
	return nil
}

// PublishRealtime is the actual live-UI-update path — mirrors
// core/streams.py's publish_realtime field-for-field. Called both per
// individual conversation event (so the UI streams tool calls/messages as
// they happen) and on every status transition (started/finished/failed/
// paused/stopped).
//
// projectID zero (uuid.Nil) is a global-chat conversation and is omitted
// from the payload entirely, matching Python's `if project_id is not
// None:` — not sent as an empty string. actorUserID is nil for a
// project-scoped conversation; services/realtime's routeEvent needs at
// least one of project_id/actor_user_id present to know which room to
// fan this out to (see the Python doc comment this mirrors).
//
// conversationID zero (uuid.Nil), unlike projectID, is sent as a literal
// empty string rather than omitted — matching internal/acpbridge's status
// event, the one caller with no real conversation to report
// (publish_realtime's own Python callers pass conversation_id="" for that
// same event; Python's str type has no separate "unset" to omit).
func (p *Publisher) PublishRealtime(
	ctx context.Context,
	projectID uuid.UUID,
	conversationID uuid.UUID,
	eventType string,
	extraPayload map[string]any,
	actorUserID *uuid.UUID,
) error {
	conversationIDStr := conversationID.String()
	if conversationID == uuid.Nil {
		conversationIDStr = ""
	}
	payload := map[string]any{"conversation_id": conversationIDStr}
	if projectID != uuid.Nil {
		payload["project_id"] = projectID.String()
	}
	if actorUserID != nil {
		payload["actor_user_id"] = actorUserID.String()
	}
	for k, v := range extraPayload {
		payload[k] = v
	}

	message, err := json.Marshal(map[string]any{"type": eventType, "payload": payload})
	if err != nil {
		return fmt.Errorf("messaging: marshal realtime event: %w", err)
	}
	if err := p.client.Publish(ctx, ChannelRealtime, message).Err(); err != nil {
		return fmt.Errorf("messaging: publish realtime event for conversation %s: %w", conversationID, err)
	}
	return nil
}

// PublishConversationStatus durably records that conversationID reached a
// terminal status (finished/failed/stopped — never "paused", which isn't
// terminal). Mirrors core/streams.py's publish_conversation_status; see
// StreamAgentConversationStatus's doc comment for who consumes this and
// why it can't just be PublishRealtime's fire-and-forget pub/sub. The same
// XADD drops the stream's entries older than the retention window.
func (p *Publisher) PublishConversationStatus(ctx context.Context, conversationID uuid.UUID, status string) error {
	if err := p.client.XAdd(ctx, p.statusArgs(map[string]any{
		"conversation_id": conversationID.String(),
		"status":          status,
	})).Err(); err != nil {
		return fmt.Errorf("messaging: publish conversation status for %s: %w", conversationID, err)
	}
	return nil
}
