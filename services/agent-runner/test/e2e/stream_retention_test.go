package e2e_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/Paca-AI/agent-runner/internal/messaging"
)

// retentionDB is a Valkey database index of its own for these tests: the
// streams they check have fixed names (paca:agent:events,
// paca:agent:conversation_status) that other e2e tests also write in db 0,
// and seeding entries with old ids needs an empty stream.
const retentionDB = 7

func newRetentionRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	if os.Getenv("PACA_E2E") != "1" {
		t.Skip("set PACA_E2E=1 to run e2e tests (requires Docker)")
	}
	checkDockerAvailable(t)
	client := redis.NewClient(&redis.Options{Addr: sharedRedisAddr, DB: retentionDB})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestPublishEventCapsTheReaderlessStream: on a real Valkey 8, 2,500 events
// leave StreamAgentEvents at its newest ~1000 ("~" trims whole nodes only,
// so a little over 1000 may stay, never fewer).
func TestPublishEventCapsTheReaderlessStream(t *testing.T) {
	client := newRetentionRedisClient(t)
	ctx := context.Background()
	if err := client.Del(ctx, messaging.StreamAgentEvents).Err(); err != nil {
		t.Fatalf("Del: %v", err)
	}
	t.Cleanup(func() { _ = client.Del(context.Background(), messaging.StreamAgentEvents).Err() })

	pub := messaging.NewPublisher(client)
	convID := uuid.New()
	const n = 2500
	for i := 0; i < n; i++ {
		if err := pub.PublishEvent(ctx, convID, uuid.New(), "agent_message_chunk", "agent", i, []byte(`{"text":"x"}`), "running"); err != nil {
			t.Fatalf("PublishEvent %d: %v", i, err)
		}
	}
	xlen, err := client.XLen(ctx, messaging.StreamAgentEvents).Result()
	if err != nil {
		t.Fatalf("XLen: %v", err)
	}
	if xlen < messaging.EventsMaxLen || xlen > messaging.EventsMaxLen+100 {
		t.Fatalf("XLEN = %d after %d events, want %d..%d", xlen, n, messaging.EventsMaxLen, messaging.EventsMaxLen+100)
	}
	last, err := client.XRevRangeN(ctx, messaging.StreamAgentEvents, "+", "-", 1).Result()
	if err != nil || len(last) != 1 || last[0].Values["event_index"] != fmt.Sprint(n-1) {
		t.Fatalf("newest entry = %v (%v), want event_index %d", last, err, n-1)
	}
}

// TestPublishConversationStatusRetention: on a real Valkey 8, statuses older
// than the window go with the next publish, and a consumer group that has not
// read yet (services/api's api.agent_queue while the API restarts) gets every
// status inside the window — the queue drain never loses one.
func TestPublishConversationStatusRetention(t *testing.T) {
	client := newRetentionRedisClient(t)
	ctx := context.Background()
	stream := messaging.StreamAgentConversationStatus
	if err := client.Del(ctx, stream).Err(); err != nil {
		t.Fatalf("Del: %v", err)
	}
	t.Cleanup(func() { _ = client.Del(context.Background(), stream).Err() })
	if err := client.XGroupCreateMkStream(ctx, stream, "api.agent_queue", "0").Err(); err != nil {
		t.Fatalf("XGroupCreateMkStream: %v", err)
	}

	// 500 statuses with the ids valkey gave them 10 days ago.
	const nOld = 500
	oldMs := time.Now().Add(-10 * 24 * time.Hour).UnixMilli()
	pipe := client.Pipeline()
	for i := 1; i <= nOld; i++ {
		pipe.XAdd(ctx, &redis.XAddArgs{
			Stream: stream,
			ID:     fmt.Sprintf("%d-%d", oldMs, i),
			Values: map[string]any{"conversation_id": uuid.NewString(), "status": "finished"},
		})
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatalf("seed old statuses: %v", err)
	}

	pub := messaging.NewPublisher(client)
	start := time.Now()
	const n = 300
	want := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id := uuid.New()
		want = append(want, id.String())
		if err := pub.PublishConversationStatus(ctx, id, "finished"); err != nil {
			t.Fatalf("PublishConversationStatus %d: %v", i, err)
		}
	}
	cut := messaging.StreamMinID(start, messaging.DefaultStreamRetention)

	left, err := client.XRangeN(ctx, stream, "-", "("+cut, nOld).Result()
	if err != nil {
		t.Fatalf("XRange below the cut: %v", err)
	}
	if len(left) >= 100 {
		t.Fatalf("%d of the %d statuses older than the window are still there", len(left), nOld)
	}

	res, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: "api.agent_queue", Consumer: "api-1", Streams: []string{stream, ">"}, Count: 10 * n, Block: -1,
	}).Result()
	if err != nil || len(res) != 1 {
		t.Fatalf("XReadGroup: %v, %d streams", err, len(res))
	}
	published := make(map[string]bool, n)
	for _, id := range want {
		published[id] = true
	}
	got := make([]string, 0, n)
	for _, m := range res[0].Messages {
		if id := fmt.Sprint(m.Values["conversation_id"]); published[id] {
			got = append(got, id) // skips a leftover old status from a node straddling the cut
		}
	}
	if len(got) != n {
		t.Fatalf("the group read %d statuses inside the window, want all %d", len(got), n)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("status %d: got conversation %s, want %s", i, got[i], want[i])
		}
	}
}
