package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func newTestPublisher(t *testing.T) (*Publisher, *redis.Client) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { client.Close() })
	return NewPublisher(client), client
}

// subscribeAndPublish subscribes to channel, runs publish (expected to
// itself publish to that channel) in a goroutine, and returns the first
// message received.
//
// Subscribing before starting publish — rather than starting publish
// immediately and racing a 50ms sleep against subscribeOne's own setup, as
// this used to — guarantees the message can't be missed. Joining the
// publish goroutine before returning — rather than leaving it to report its
// own error via t.Errorf whenever it happens to finish — guarantees that
// error is surfaced from this goroutine, not from one that may still be
// running after the test function has already returned: under `go test
// -race`'s heavier scheduling, subscribeOne's old racy version could return
// before the publish goroutine did, letting t.Cleanup close the miniredis
// client out from under it and making its late t.Errorf panic with "Fail in
// goroutine after Test has completed".
func subscribeAndPublish(t *testing.T, client *redis.Client, channel string, publish func() error) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sub := client.Subscribe(ctx, channel)
	defer sub.Close()
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- publish() }()

	var decoded map[string]any
	select {
	case msg := <-sub.Channel():
		if err := json.Unmarshal([]byte(msg.Payload), &decoded); err != nil {
			t.Fatalf("unmarshal message: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for a published message")
	}

	if err := <-done; err != nil {
		t.Fatalf("publish: %v", err)
	}
	return decoded
}

func TestPublishRealtime_ProjectScoped(t *testing.T) {
	pub, client := newTestPublisher(t)
	projectID := uuid.New()
	convID := uuid.New()

	msg := subscribeAndPublish(t, client, ChannelRealtime, func() error {
		return pub.PublishRealtime(context.Background(), projectID, convID,
			"agent.tool_call", map[string]any{"event_index": 5}, nil)
	})
	if msg["type"] != "agent.tool_call" {
		t.Errorf(`type = %v, want "agent.tool_call"`, msg["type"])
	}
	payload, ok := msg["payload"].(map[string]any)
	if !ok {
		t.Fatalf("payload = %T, want an object", msg["payload"])
	}
	if payload["conversation_id"] != convID.String() {
		t.Errorf("conversation_id = %v, want %s", payload["conversation_id"], convID)
	}
	if payload["project_id"] != projectID.String() {
		t.Errorf("project_id = %v, want %s", payload["project_id"], projectID)
	}
	if _, present := payload["actor_user_id"]; present {
		t.Errorf("actor_user_id = %v, want absent for a project-scoped event", payload["actor_user_id"])
	}
	if payload["event_index"] != float64(5) { // JSON numbers decode as float64
		t.Errorf("event_index = %v, want 5", payload["event_index"])
	}
}

// TestPublishRealtime_GlobalChatOmitsProjectID mirrors the routing
// contract services/realtime's subscriber.ts implements: an agent.* event
// with no project_id falls through to routing by actor_user_id instead
// (a global agent's home-page/admin chat) — so project_id must be an
// entirely absent key, not present-but-empty, or subscriber.ts's
// `typeof projectId === "string" && projectId` check would need to also
// reject empty string correctly, which it does, but omission is what the
// Python producer does and this pins the Go side to match it exactly.
func TestPublishRealtime_GlobalChatOmitsProjectID(t *testing.T) {
	pub, client := newTestPublisher(t)
	convID := uuid.New()
	actorUserID := uuid.New()

	msg := subscribeAndPublish(t, client, ChannelRealtime, func() error {
		return pub.PublishRealtime(context.Background(), uuid.Nil, convID,
			"agent.conversation.finished", nil, &actorUserID)
	})
	payload := msg["payload"].(map[string]any)
	if _, present := payload["project_id"]; present {
		t.Errorf("project_id = %v, want entirely absent for a global-chat event", payload["project_id"])
	}
	if payload["actor_user_id"] != actorUserID.String() {
		t.Errorf("actor_user_id = %v, want %s", payload["actor_user_id"], actorUserID)
	}
}

// TestPublishRealtime_NilConversationIDSendsEmptyString pins
// internal/acpbridge's status event shape — the one caller with no real
// conversation to report (an ACP bridge connecting/disconnecting is a
// per-agent event, not a per-conversation one). Unlike projectID's
// omit-when-nil handling, conversation_id must still be present as a
// literal empty string, matching Python's publish_realtime callers, which
// pass conversation_id="" here rather than None (str has no separate
// "unset" to omit).
func TestPublishRealtime_NilConversationIDSendsEmptyString(t *testing.T) {
	pub, client := newTestPublisher(t)

	msg := subscribeAndPublish(t, client, ChannelRealtime, func() error {
		return pub.PublishRealtime(context.Background(), uuid.Nil, uuid.Nil,
			"agent.acp_bridge.status", map[string]any{"agent_id": "a1", "connected": true}, nil)
	})
	payload := msg["payload"].(map[string]any)
	if payload["conversation_id"] != "" {
		t.Errorf("conversation_id = %v, want empty string", payload["conversation_id"])
	}
	if _, present := payload["project_id"]; present {
		t.Errorf("project_id = %v, want absent", payload["project_id"])
	}
}

// StreamAgentEvents has no reader, so it is capped by count at the newest
// EventsMaxLen entries ("~": valkey drops whole nodes only).
func TestEventArgs_CapsTheReaderlessStreamByCount(t *testing.T) {
	args := eventArgs(map[string]any{"event_type": "x"})
	if args.Stream != StreamAgentEvents || args.MaxLen != EventsMaxLen || EventsMaxLen != 1000 || !args.Approx || args.MinID != "" {
		t.Fatalf("eventArgs = %+v, want Stream %s, MaxLen ~ 1000, no MinID", args, StreamAgentEvents)
	}
}

func TestPublishEvent_KeepsTheNewestEventsMaxLen(t *testing.T) {
	pub, client := newTestPublisher(t)
	ctx := context.Background()
	convID := uuid.New()
	const n = EventsMaxLen + 500
	for i := 0; i < n; i++ {
		if err := pub.PublishEvent(ctx, convID, uuid.Nil, "agent_message_chunk", "agent", i, []byte(`{}`), "running"); err != nil {
			t.Fatalf("PublishEvent %d: %v", i, err)
		}
	}
	entries, err := client.XRange(ctx, StreamAgentEvents, "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange: %v", err)
	}
	if len(entries) != EventsMaxLen {
		t.Fatalf("stream holds %d entries, want the newest %d", len(entries), EventsMaxLen)
	}
	if got := entries[0].Values["event_index"]; got != "500" {
		t.Errorf("oldest kept event_index = %v, want 500", got)
	}
	if got := entries[len(entries)-1].Values["event_index"]; got != "1499" {
		t.Errorf("newest event_index = %v, want 1499", got)
	}
}

// The status stream has readers (services/api's api.agent_queue and
// api.automation_engine groups), so it is cut by age, never by count.
func TestStatusArgs_CutsByAgeNeverByCount(t *testing.T) {
	p := &Publisher{retention: 7 * 24 * time.Hour, now: func() time.Time { return time.UnixMilli(1_789_000_000_000) }}
	args := p.statusArgs(map[string]any{"status": "finished"})
	if args.Stream != StreamAgentConversationStatus || args.MinID != "1788395200000-0" || !args.Approx || args.MaxLen != 0 {
		t.Fatalf("statusArgs = %+v, want Stream %s, MINID ~ now-7d, no MaxLen", args, StreamAgentConversationStatus)
	}
	if NewPublisher(nil).retention != DefaultStreamRetention || DefaultStreamRetention != 7*24*time.Hour {
		t.Fatalf("NewPublisher retention = %v, want 7 days", NewPublisher(nil).retention)
	}
}

// Old terminal statuses go on the next publish; a consumer group that has not
// read yet (api.agent_queue while the API restarts) still gets every status
// inside the window, so the queue drain is never skipped.
func TestPublishConversationStatus_DropsOnlyStatusesOlderThanRetention(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { client.Close() })
	var now time.Time
	pub := NewPublisher(client).WithStreamRetention(7 * 24 * time.Hour)
	pub.now = func() time.Time { return now }
	at := func(ts time.Time) { // valkey's id clock and the retention clock move together
		now = ts
		mr.SetTime(ts)
	}
	ctx := context.Background()
	if err := client.XGroupCreateMkStream(ctx, StreamAgentConversationStatus, "api.agent_queue", "0").Err(); err != nil {
		t.Fatalf("XGroupCreateMkStream: %v", err)
	}

	base := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	old, recent, latest := uuid.New(), uuid.New(), uuid.New()
	at(base.Add(-8 * 24 * time.Hour))
	if err := pub.PublishConversationStatus(ctx, old, "finished"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	at(base.Add(-3 * 24 * time.Hour))
	if err := pub.PublishConversationStatus(ctx, recent, "failed"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	at(base)
	if err := pub.PublishConversationStatus(ctx, latest, "stopped"); err != nil {
		t.Fatalf("publish: %v", err)
	}

	res, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: "api.agent_queue", Consumer: "api-1", Streams: []string{StreamAgentConversationStatus, ">"}, Count: 100, Block: -1,
	}).Result()
	if err != nil || len(res) != 1 {
		t.Fatalf("XReadGroup: %v, %d streams", err, len(res))
	}
	var got []string
	for _, m := range res[0].Messages {
		got = append(got, fmt.Sprint(m.Values["conversation_id"]))
	}
	if want := []string{recent.String(), latest.String()}; !slices.Equal(got, want) {
		t.Fatalf("the group read %v, want the two statuses inside the window %v", got, want)
	}
}

func TestPublishConversationStatus(t *testing.T) {
	pub, client := newTestPublisher(t)
	convID := uuid.New()
	ctx := context.Background()

	if err := pub.PublishConversationStatus(ctx, convID, "finished"); err != nil {
		t.Fatalf("PublishConversationStatus: %v", err)
	}

	entries, err := client.XRange(ctx, StreamAgentConversationStatus, "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d stream entries, want 1", len(entries))
	}
	if entries[0].Values["conversation_id"] != convID.String() {
		t.Errorf("conversation_id = %v, want %s", entries[0].Values["conversation_id"], convID)
	}
	if entries[0].Values["status"] != "finished" {
		t.Errorf("status = %v, want finished", entries[0].Values["status"])
	}
}
