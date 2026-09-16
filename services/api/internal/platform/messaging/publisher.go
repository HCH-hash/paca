// Package messaging provides Valkey-backed event publishing.
//
// Two delivery mechanisms are supported:
//   - Pub/Sub (Publish): immediate fan-out to real-time subscribers; used to
//     notify services/realtime so it can push updates to connected clients.
//   - Streams (Append): durable, ordered log; used for analytics and any
//     consumer that needs to replay or process events at its own pace.
//
// Stream retention: every XADD this publisher makes also drops the stream's
// entries that are older than the retention window (XADD ... MINID ~
// <now - retention>). Consumer groups read with XREADGROUP '>' and ack within
// seconds, and nothing reads stream history, so without this every stream
// kept every entry since install (valkey reached 2.6 GB). The cut is by AGE,
// never by count: a group that falls behind keeps every entry it has not read
// yet for the whole window, however many entries arrive meanwhile. "~" lets
// valkey drop only whole internal nodes, so it never removes an entry newer
// than the cut; it may keep a few older ones until the next append.
package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultStreamRetention is how long an appended stream entry is kept when
// PACA_STREAM_RETENTION is not set: 7 days (config.Load uses the same value).
const DefaultStreamRetention = 7 * 24 * time.Hour

// Publisher wraps a Valkey client and exposes both Pub/Sub and Stream publishing.
type Publisher struct {
	client    *redis.Client
	log       *slog.Logger
	retention time.Duration    // stream entries older than this are trimmed on append; 0 = keep all
	now       func() time.Time // the clock the retention cut is taken from (tests replace it)
}

// NewPublisher creates a Publisher backed by an existing Valkey client. Its
// streams keep DefaultStreamRetention; see WithStreamRetention.
func NewPublisher(client *redis.Client, log *slog.Logger) *Publisher {
	log.Info("valkey publisher ready")
	return &Publisher{client: client, log: log, retention: DefaultStreamRetention, now: time.Now}
}

// WithStreamRetention sets how long an appended stream entry is kept (the
// validated PACA_STREAM_RETENTION, see config.Load). Returns p for chaining.
func (p *Publisher) WithStreamRetention(retention time.Duration) *Publisher {
	p.retention = retention
	if p.log != nil {
		p.log.Info("valkey publisher: stream retention", "retention", retention.String())
	}
	return p
}

// StreamMinID is the stream id below which every entry is older than
// retention at time now: "<unix ms of now-retention>-0". Stream ids start with
// the valkey server's clock in milliseconds, so XADD MINID with this id drops
// exactly the entries appended before now-retention.
func StreamMinID(now time.Time, retention time.Duration) string {
	return strconv.FormatInt(now.Add(-retention).UnixMilli(), 10) + "-0"
}

// xaddArgs builds the XADD for one append, with the retention cut.
func (p *Publisher) xaddArgs(stream string, values any) *redis.XAddArgs {
	args := &redis.XAddArgs{Stream: stream, Values: values}
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

// Publish serialises payload as JSON and sends it to a Valkey Pub/Sub channel.
// This is the primary path for real-time notifications: services/realtime
// subscribes to the channel and fans the event out to connected Socket.IO clients.
func (p *Publisher) Publish(ctx context.Context, channel string, payload any) error {
	if p == nil || p.client == nil {
		return errors.New("messaging: publisher not initialized")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("messaging: marshal: %w", err)
	}

	if err := p.client.Publish(ctx, channel, string(body)).Err(); err != nil {
		return fmt.Errorf("messaging: publish %q: %w", channel, err)
	}

	return nil
}

// Append serialises payload as JSON and appends it to a Valkey Stream.
// The eventType is stored in the "type" field; the serialised body in "payload".
// Use this for analytics, audit logs, and any consumer that requires a durable
// ordered event log. The same XADD drops the stream's entries older than the
// publisher's stream retention (see the package comment).
func (p *Publisher) Append(ctx context.Context, stream, eventType string, payload any) error {
	if p == nil || p.client == nil {
		return errors.New("messaging: publisher not initialized")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("messaging: marshal: %w", err)
	}

	if err := p.client.XAdd(ctx, p.xaddArgs(stream, map[string]any{
		"type":    eventType,
		"payload": string(body),
	})).Err(); err != nil {
		return fmt.Errorf("messaging: append %q to %q: %w", eventType, stream, err)
	}

	return nil
}

// AppendFlat appends a message to a Valkey Stream writing the provided fields
// directly as top-level stream entry fields (i.e. not JSON-encoded under a
// "payload" key). Use this when the consumer (e.g. services/ai-agent) reads
// the individual fields without further deserialization. Retention is applied
// exactly as in Append.
func (p *Publisher) AppendFlat(ctx context.Context, stream string, fields map[string]any) error {
	if p == nil || p.client == nil {
		return errors.New("messaging: publisher not initialized")
	}

	if err := p.client.XAdd(ctx, p.xaddArgs(stream, fields)).Err(); err != nil {
		return fmt.Errorf("messaging: append flat to %q: %w", stream, err)
	}

	return nil
}

// Close is a no-op; the Valkey client lifecycle is managed by the owner.
func (p *Publisher) Close() {}
