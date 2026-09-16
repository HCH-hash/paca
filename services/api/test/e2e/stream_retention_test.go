package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/Paca-AI/api/internal/platform/messaging"
)

// newRetentionRedisClient connects to the suite's shared Valkey container
// only; these tests need no per-test Postgres database.
func newRetentionRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	if os.Getenv("PACA_E2E") != "1" {
		t.Skip("set PACA_E2E=1 to run e2e tests (requires Docker)")
	}
	checkDockerAvailable(t)
	opt, err := redis.ParseURL(sharedRedisURL)
	if err != nil {
		t.Fatalf("parse %q: %v", sharedRedisURL, err)
	}
	client := redis.NewClient(opt)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestE2EStreamRetention_RealValkey runs the publisher's retention against a
// real Valkey 8 (approximate trimming, radix-tree nodes, the per-call trim
// limit), which miniredis does not model:
//   - a backlog appended 10 days ago goes with the next append;
//   - 25,000 appends inside the window, far more than any count cap, all stay;
//   - a consumer group that read nothing (lagging) still reads every one of
//     them, and a group that kept up has nothing pending.
func TestE2EStreamRetention_RealValkey(t *testing.T) {
	t.Parallel()
	client := newRetentionRedisClient(t)
	ctx := context.Background()
	stream := "e2e:retention:" + uuid.NewString()
	t.Cleanup(func() { _ = client.Del(context.Background(), stream).Err() })

	for _, g := range []string{"live", "lagging"} {
		if err := client.XGroupCreateMkStream(ctx, stream, g, "0").Err(); err != nil {
			t.Fatalf("XGroupCreateMkStream %s: %v", g, err)
		}
	}

	// The backlog: 3,000 entries with the ids valkey gave them 10 days ago.
	const nOld = 3000
	oldMs := time.Now().Add(-10 * 24 * time.Hour).UnixMilli()
	pipe := client.Pipeline()
	for i := 1; i <= nOld; i++ {
		pipe.XAdd(ctx, &redis.XAddArgs{
			Stream: stream,
			ID:     fmt.Sprintf("%d-%d", oldMs, i),
			Values: map[string]any{"type": "old", "payload": "{}"},
		})
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatalf("seed the old backlog: %v", err)
	}
	liveReadAndAck(t, client, stream) // a group that keeps up has taken the backlog

	pub := messaging.NewPublisher(client, slog.New(slog.NewTextHandler(io.Discard, nil)))
	const n = 25000
	start := time.Now()
	for i := 0; i < n; i++ {
		if err := pub.Append(ctx, stream, "task.updated", map[string]int{"i": i}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
		if i%1000 == 999 {
			liveReadAndAck(t, client, stream)
		}
	}
	liveReadAndAck(t, client, stream)
	cut := messaging.StreamMinID(start, messaging.DefaultStreamRetention)

	// The backlog is gone ("~" may keep part of one node that straddles the cut).
	left, err := client.XRangeN(ctx, stream, "-", "("+cut, nOld).Result()
	if err != nil {
		t.Fatalf("XRange below the cut: %v", err)
	}
	if len(left) >= 200 {
		t.Fatalf("%d of the %d entries older than the window are still there; want at most part of one node", len(left), nOld)
	}

	// Every entry inside the window is still there, in order, payload intact.
	kept, err := client.XRange(ctx, stream, cut, "+").Result()
	if err != nil {
		t.Fatalf("XRange inside the window: %v", err)
	}
	if len(kept) != n {
		t.Fatalf("%d entries inside the window, want all %d", len(kept), n)
	}
	for i, m := range kept {
		if got := payloadIndex(t, m); got != i {
			t.Fatalf("entry %d inside the window has i=%d", i, got)
		}
	}

	// The group that read nothing gets every one of them.
	res, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: "lagging", Consumer: "lagging-1", Streams: []string{stream, ">"}, Count: 2 * n, Block: -1,
	}).Result()
	if err != nil {
		t.Fatalf("XReadGroup lagging: %v", err)
	}
	next := 0
	for _, m := range res[0].Messages {
		if m.Values["type"] != "task.updated" {
			continue // a leftover old entry from the straddling node
		}
		if got := payloadIndex(t, m); got != next {
			t.Fatalf("lagging group: entry %d has i=%d", next, got)
		}
		next++
	}
	if next != n {
		t.Fatalf("lagging group read %d of the %d entries inside the window", next, n)
	}

	groups, err := client.XInfoGroups(ctx, stream).Result()
	if err != nil {
		t.Fatalf("XInfoGroups: %v", err)
	}
	for _, g := range groups {
		if g.Name == "live" && g.Pending != 0 {
			t.Fatalf("live group has %d pending, want 0", g.Pending)
		}
	}
}

// liveReadAndAck reads and acks everything the "live" group has not taken yet.
func liveReadAndAck(t *testing.T, client *redis.Client, stream string) {
	t.Helper()
	ctx := context.Background()
	res, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: "live", Consumer: "live-1", Streams: []string{stream, ">"}, Count: 100000, Block: -1,
	}).Result()
	if err == redis.Nil {
		return
	}
	if err != nil {
		t.Fatalf("XReadGroup live: %v", err)
	}
	ids := make([]string, 0, len(res[0].Messages))
	for _, m := range res[0].Messages {
		ids = append(ids, m.ID)
	}
	if len(ids) > 0 {
		if err := client.XAck(ctx, stream, "live", ids...).Err(); err != nil {
			t.Fatalf("XAck live: %v", err)
		}
	}
}

func payloadIndex(t *testing.T, m redis.XMessage) int {
	t.Helper()
	raw, _ := m.Values["payload"].(string)
	var p struct {
		I int `json:"i"`
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("entry %s: payload %q: %v", m.ID, raw, err)
	}
	return p.I
}
