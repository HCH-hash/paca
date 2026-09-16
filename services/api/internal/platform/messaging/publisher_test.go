package messaging

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestPublish_NotInitialized(t *testing.T) {
	p := &Publisher{}

	err := p.Publish(context.Background(), "paca.events", struct{}{})
	if err == nil {
		t.Fatal("expected not-initialized error")
	}
	if !strings.Contains(err.Error(), "messaging: publisher not initialized") {
		t.Fatalf("expected not-initialized error, got %v", err)
	}
}

func TestAppend_NotInitialized(t *testing.T) {
	p := &Publisher{}

	err := p.Append(context.Background(), "paca.analytics", "user.created", struct{}{})
	if err == nil {
		t.Fatal("expected not-initialized error")
	}
	if !strings.Contains(err.Error(), "messaging: publisher not initialized") {
		t.Fatalf("expected not-initialized error, got %v", err)
	}
}

func TestClose_NilSafe(_ *testing.T) {
	var p *Publisher
	p.Close()

	(&Publisher{}).Close()
}

// retentionFixture is a publisher on miniredis whose retention clock and
// valkey's stream-id clock move together, so "an entry appended N days ago"
// can be written by setting the clock back N days.
type retentionFixture struct {
	t      *testing.T
	mr     *miniredis.Miniredis
	client *redis.Client
	pub    *Publisher
	now    time.Time
}

func newRetentionFixture(t *testing.T, retention time.Duration) *retentionFixture {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { client.Close() })
	f := &retentionFixture{t: t, mr: mr, client: client}
	f.pub = NewPublisher(client, testLogger()).WithStreamRetention(retention)
	f.pub.now = func() time.Time { return f.now }
	f.at(time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC))
	return f
}

// at sets both clocks: the next append gets a stream id at t, and its
// retention cut is t - retention.
func (f *retentionFixture) at(t time.Time) {
	f.now = t
	f.mr.SetTime(t)
}

func (f *retentionFixture) append(stream, eventType string) {
	f.t.Helper()
	if err := f.pub.Append(context.Background(), stream, eventType, map[string]string{"k": eventType}); err != nil {
		f.t.Fatalf("Append %s: %v", eventType, err)
	}
}

// types returns the "type" field of every entry still in the stream, oldest first.
func (f *retentionFixture) types(stream string) []string {
	f.t.Helper()
	entries, err := f.client.XRange(context.Background(), stream, "-", "+").Result()
	if err != nil {
		f.t.Fatalf("XRange: %v", err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, fmt.Sprint(e.Values["type"]))
	}
	return out
}

// readAll reads everything group has not been delivered yet, as consumer.
func (f *retentionFixture) readAll(stream, group, consumer string) []redis.XMessage {
	f.t.Helper()
	res, err := f.client.XReadGroup(context.Background(), &redis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{stream, ">"},
		Count:    100000,
		Block:    -1,
	}).Result()
	if err == redis.Nil {
		return nil
	}
	if err != nil {
		f.t.Fatalf("XReadGroup %s: %v", group, err)
	}
	if len(res) != 1 {
		f.t.Fatalf("XReadGroup %s: %d streams, want 1", group, len(res))
	}
	return res[0].Messages
}

func messageTypes(msgs []redis.XMessage) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, fmt.Sprint(m.Values["type"]))
	}
	return out
}

const week = 7 * 24 * time.Hour

// The retention cut is an XADD MINID with "~" and never a count cap, so the
// number of entries appended can never push an unread one out.
func TestXAddArgs_CutsByAgeNeverByCount(t *testing.T) {
	p := &Publisher{retention: week, now: func() time.Time { return time.UnixMilli(1_789_000_000_000) }}
	args := p.xaddArgs("paca.task_activities", map[string]any{"type": "x"})

	if want := StreamMinID(time.UnixMilli(1_789_000_000_000), week); args.MinID != want {
		t.Fatalf("MinID = %q, want %q", args.MinID, want)
	}
	if args.MinID != "1788395200000-0" {
		t.Fatalf("MinID = %q, want now minus 7 days in ms, seq 0", args.MinID)
	}
	if !args.Approx {
		t.Fatal("Approx = false; the cut must be MINID ~ so valkey only drops whole nodes")
	}
	if args.MaxLen != 0 {
		t.Fatalf("MaxLen = %d; a count cap could drop entries a lagging group has not read", args.MaxLen)
	}
	if args.Stream != "paca.task_activities" {
		t.Fatalf("Stream = %q", args.Stream)
	}
}

func TestNewPublisher_DefaultsToSevenDays(t *testing.T) {
	p := NewPublisher(nil, testLogger())
	if p.retention != DefaultStreamRetention || DefaultStreamRetention != week {
		t.Fatalf("retention = %v, want %v", p.retention, week)
	}
}

// Old entries go on the next append; a consumer group that never read
// anything (lagging) still gets every entry inside the window, in order.
func TestAppend_DropsEntriesOlderThanRetention(t *testing.T) {
	f := newRetentionFixture(t, week)
	const stream = "paca.task_activities"
	base := f.now
	if err := f.client.XGroupCreateMkStream(context.Background(), stream, "lagging", "0").Err(); err != nil {
		t.Fatalf("XGroupCreateMkStream: %v", err)
	}

	f.at(base.Add(-10 * 24 * time.Hour))
	f.append(stream, "old-1")
	f.append(stream, "old-2")
	f.at(base.Add(-8 * 24 * time.Hour))
	f.append(stream, "old-3")
	f.at(base.Add(-6 * 24 * time.Hour))
	f.append(stream, "recent-1")
	f.at(base.Add(-time.Hour))
	f.append(stream, "recent-2")

	// The append at base-6d cut at base-13d and kept old-1..old-3; the append
	// at base-1h cut at base-7d-1h and dropped all three.
	if got := f.types(stream); !slices.Equal(got, []string{"recent-1", "recent-2"}) {
		t.Fatalf("after the append at base-1h: %v, want [recent-1 recent-2]", got)
	}

	f.at(base)
	f.append(stream, "new")
	want := []string{"recent-1", "recent-2", "new"}
	if got := f.types(stream); !slices.Equal(got, want) {
		t.Fatalf("stream = %v, want %v", got, want)
	}
	if got := messageTypes(f.readAll(stream, "lagging", "c1")); !slices.Equal(got, want) {
		t.Fatalf("lagging group read %v, want every entry in the window %v", got, want)
	}
}

// The cut moves with the clock: an entry is kept for the whole window and
// dropped by the first append after it ages out.
func TestAppend_KeepsAnEntryForTheWholeWindow(t *testing.T) {
	f := newRetentionFixture(t, week)
	const stream = "paca.plugin_events"
	base := f.now

	f.append(stream, "first")
	f.at(base.Add(week - time.Minute))
	f.append(stream, "second")
	if got := f.types(stream); !slices.Equal(got, []string{"first", "second"}) {
		t.Fatalf("one minute before the window ends: %v, want both entries", got)
	}
	f.at(base.Add(week + time.Minute))
	f.append(stream, "third")
	if got := f.types(stream); !slices.Equal(got, []string{"second", "third"}) {
		t.Fatalf("one minute after the window ends: %v, want [second third]", got)
	}
}

// However many entries arrive, a group that has read none of them loses none
// of them while they are inside the window — unlike a MAXLEN cap.
func TestAppend_LaggingGroupKeepsEveryUnreadEntryInTheWindow(t *testing.T) {
	f := newRetentionFixture(t, week)
	const stream = "paca.task_activities"
	ctx := context.Background()
	base := f.now
	for _, g := range []string{"live", "lagging"} {
		if err := f.client.XGroupCreateMkStream(ctx, stream, g, "0").Err(); err != nil {
			t.Fatalf("XGroupCreateMkStream %s: %v", g, err)
		}
	}

	// An old backlog nobody needs any more...
	f.at(base.Add(-9 * 24 * time.Hour))
	for i := 0; i < 50; i++ {
		f.append(stream, fmt.Sprintf("old-%d", i))
	}
	// ...then 1500 entries spread over the last 6 days, while "live" reads
	// and acks as it goes and "lagging" reads nothing.
	const n = 1500
	var want []string
	for i := 0; i < n; i++ {
		f.at(base.Add(-6*24*time.Hour + time.Duration(i)*time.Minute*5))
		typ := fmt.Sprintf("e-%04d", i)
		f.append(stream, typ)
		want = append(want, typ)
		for _, m := range f.readAll(stream, "live", "live-1") {
			if err := f.client.XAck(ctx, stream, "live", m.ID).Err(); err != nil {
				t.Fatalf("XAck: %v", err)
			}
		}
	}

	if got := f.types(stream); !slices.Equal(got, want) {
		t.Fatalf("stream holds %d entries (first %v), want exactly the %d inside the window", len(got), got[:min(3, len(got))], n)
	}
	if got := messageTypes(f.readAll(stream, "lagging", "lagging-1")); !slices.Equal(got, want) {
		t.Fatalf("lagging group read %d entries, want all %d", len(got), n)
	}
}

// An entry a consumer took but has not acked yet (it failed to handle it) is
// still there for its retry as long as it is inside the window.
func TestAppend_KeepsPendingEntriesInsideTheWindow(t *testing.T) {
	f := newRetentionFixture(t, week)
	const stream = "paca.task_assignments"
	ctx := context.Background()
	base := f.now
	if err := f.client.XGroupCreateMkStream(ctx, stream, "writer", "0").Err(); err != nil {
		t.Fatalf("XGroupCreateMkStream: %v", err)
	}
	f.append(stream, "a")
	f.append(stream, "b")
	taken := f.readAll(stream, "writer", "w1") // delivered, never acked
	if len(taken) != 2 {
		t.Fatalf("read %d, want 2", len(taken))
	}

	f.at(base.Add(6 * 24 * time.Hour))
	f.append(stream, "c")

	pending, err := f.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: "writer", Consumer: "w1", Streams: []string{stream, "0"}, Count: 100, Block: -1,
	}).Result()
	if err != nil {
		t.Fatalf("XReadGroup 0: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("XReadGroup 0: %d streams, want 1", len(pending))
	}
	if got := messageTypes(pending[0].Messages); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("pending entries on retry = %v, want [a b] with their payloads", got)
	}
}

func TestAppendFlat_AppliesTheSameRetention(t *testing.T) {
	f := newRetentionFixture(t, week)
	const stream = "paca:agent:triggers"
	ctx := context.Background()
	base := f.now

	f.at(base.Add(-8 * 24 * time.Hour))
	if err := f.pub.AppendFlat(ctx, stream, map[string]any{"type": "old"}); err != nil {
		t.Fatalf("AppendFlat: %v", err)
	}
	f.at(base.Add(-2 * 24 * time.Hour))
	if err := f.pub.AppendFlat(ctx, stream, map[string]any{"type": "recent"}); err != nil {
		t.Fatalf("AppendFlat: %v", err)
	}
	f.at(base)
	if err := f.pub.AppendFlat(ctx, stream, map[string]any{"type": "new"}); err != nil {
		t.Fatalf("AppendFlat: %v", err)
	}
	if got := f.types(stream); !slices.Equal(got, []string{"recent", "new"}) {
		t.Fatalf("stream = %v, want [recent new]", got)
	}
}

// A retention set through WithStreamRetention (PACA_STREAM_RETENTION) is the
// one applied.
func TestWithStreamRetention_SetsTheWindow(t *testing.T) {
	f := newRetentionFixture(t, 24*time.Hour)
	const stream = "paca.doc_activities"
	base := f.now

	f.at(base.Add(-2 * 24 * time.Hour))
	f.append(stream, "two-days-old")
	f.at(base.Add(-12 * time.Hour))
	f.append(stream, "half-a-day-old")
	f.at(base)
	f.append(stream, "new")
	if got := f.types(stream); !slices.Equal(got, []string{"half-a-day-old", "new"}) {
		t.Fatalf("stream = %v, want [half-a-day-old new] with a 24h window", got)
	}
}
