package ws

import (
	"encoding/json"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/messaging"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

const testMaxTopics = 64

// hubHasTopic reports whether the hub's topic index currently has any
// subscriber for `topic` (test-only synchronisation helper).
func (g *Gateway) hubHasTopic(topic string) bool {
	g.hub.mu.Lock()
	defer g.hub.mu.Unlock()
	_, ok := g.hub.topics[topic]
	return ok
}

// newTestConn builds a socket-less conn for hub tests: close() tolerates a nil
// ws and only flips the observable `closed` flag, and `send` is a real channel
// we can drain to assert delivery.
func newTestConn(buffer int) *conn {
	return &conn{send: make(chan []byte, buffer), subs: make(map[string]struct{})}
}

// drain reads one frame (non-blocking) and decodes it; ok=false if none queued.
func drain(t *testing.T, c *conn) (serverFrame, bool) {
	t.Helper()
	select {
	case b := <-c.send:
		var f serverFrame
		if err := json.Unmarshal(b, &f); err != nil {
			t.Fatalf("unmarshal frame: %v", err)
		}
		return f, true
	default:
		return serverFrame{}, false
	}
}

func newTestEvent() *messaging.FanoutEvent {
	return &messaging.FanoutEvent{
		TaskID:    "T1",
		VersionID: "V1",
		RunID:     "R1",
		Seq:       7,
		Kind:      "status",
		Ts:        "2026-06-02T00:00:00Z",
		Payload:   json.RawMessage(`{"status":"running"}`),
	}
}

func TestHub_SubscribeFanoutMatchingTopicOnly(t *testing.T) {
	hub := NewHub(observability.NewMetrics())
	a := newTestConn(4)
	b := newTestConn(4)
	hub.register(a)
	hub.register(b)
	hub.subscribe(a, "task:T1", testMaxTopics)
	// b subscribes to a DIFFERENT task; it must not receive T1's event.
	hub.subscribe(b, "task:T2", testMaxTopics)

	hub.Fanout(newTestEvent())

	f, ok := drain(t, a)
	if !ok {
		t.Fatal("subscriber A received nothing")
	}
	if f.Topic != "task:T1" || f.Seq != 7 || f.Kind != "status" || f.Ts != "2026-06-02T00:00:00Z" {
		t.Fatalf("unexpected frame: %+v", f)
	}
	if _, ok := drain(t, b); ok {
		t.Fatal("non-subscriber B received an event")
	}
}

func TestHub_OneEventBothTaskAndVersionSubscribers(t *testing.T) {
	hub := NewHub(observability.NewMetrics())
	a := newTestConn(4) // task:T1
	b := newTestConn(4) // version:V1
	hub.register(a)
	hub.register(b)
	hub.subscribe(a, "task:T1", testMaxTopics)
	hub.subscribe(b, "version:V1", testMaxTopics)

	hub.Fanout(newTestEvent())

	fa, ok := drain(t, a)
	if !ok || fa.Topic != "task:T1" {
		t.Fatalf("A frame = %+v ok=%v", fa, ok)
	}
	fb, ok := drain(t, b)
	if !ok || fb.Topic != "version:V1" {
		t.Fatalf("B frame = %+v ok=%v", fb, ok)
	}
	// Same seq/kind/payload/ts across both topics.
	if fa.Seq != fb.Seq || fa.Kind != fb.Kind || fa.Ts != fb.Ts || string(fa.Payload) != string(fb.Payload) {
		t.Fatalf("frames diverged: %+v vs %+v", fa, fb)
	}
}

func TestHub_ConnSubscribedToBothTopicsGetsTwoFrames(t *testing.T) {
	hub := NewHub(observability.NewMetrics())
	c := newTestConn(4)
	hub.register(c)
	hub.subscribe(c, "task:T1", testMaxTopics)
	hub.subscribe(c, "version:V1", testMaxTopics)

	hub.Fanout(newTestEvent())

	topics := map[string]bool{}
	for i := 0; i < 2; i++ {
		f, ok := drain(t, c)
		if !ok {
			t.Fatalf("expected 2 frames, got %d", i)
		}
		topics[f.Topic] = true
	}
	if !topics["task:T1"] || !topics["version:V1"] {
		t.Fatalf("expected both topics, got %v", topics)
	}
	if _, ok := drain(t, c); ok {
		t.Fatal("got a third (unexpected) frame")
	}
}

func TestHub_DuplicateSubscribeIsIdempotent(t *testing.T) {
	m := observability.NewMetrics()
	hub := NewHub(m)
	c := newTestConn(4)
	hub.register(c)

	added1, _ := hub.subscribe(c, "task:T1", testMaxTopics)
	added2, _ := hub.subscribe(c, "task:T1", testMaxTopics)
	if !added1 || added2 {
		t.Fatalf("idempotency broken: added1=%v added2=%v", added1, added2)
	}
	if got := testutil.ToFloat64(m.WSSubscriptionsActive); got != 1 {
		t.Fatalf("subscriptions gauge = %v, want 1", got)
	}

	hub.Fanout(newTestEvent())
	if _, ok := drain(t, c); !ok {
		t.Fatal("expected exactly one delivery")
	}
	if _, ok := drain(t, c); ok {
		t.Fatal("duplicate subscribe caused double delivery")
	}
}

func TestHub_UnsubscribeStopsDelivery(t *testing.T) {
	hub := NewHub(observability.NewMetrics())
	c := newTestConn(4)
	hub.register(c)
	hub.subscribe(c, "task:T1", testMaxTopics)
	hub.unsubscribe(c, "task:T1")

	hub.Fanout(newTestEvent())
	if _, ok := drain(t, c); ok {
		t.Fatal("received event after unsubscribe")
	}
}

func TestHub_UnregisterPurgesTopicIndex(t *testing.T) {
	m := observability.NewMetrics()
	hub := NewHub(m)
	c := newTestConn(4)
	hub.register(c)
	hub.subscribe(c, "task:T1", testMaxTopics)

	hub.unregister(c)

	if testutil.ToFloat64(m.WSConnectionsActive) != 0 {
		t.Fatal("connections gauge not decremented")
	}
	if testutil.ToFloat64(m.WSSubscriptionsActive) != 0 {
		t.Fatal("subscriptions gauge not decremented on unregister")
	}
	hub.mu.Lock()
	_, present := hub.topics["task:T1"]
	hub.mu.Unlock()
	if present {
		t.Fatal("topic index still references the purged conn")
	}
	// Fanout must not panic / deliver to the removed conn.
	hub.Fanout(newTestEvent())
}

func TestHub_SlowClientEvictedHealthyKeepsFlowing(t *testing.T) {
	m := observability.NewMetrics()
	hub := NewHub(m)
	slow := newTestConn(1) // buffer of 1, never drained → fills immediately
	fast := newTestConn(8)
	hub.register(slow)
	hub.register(fast)
	hub.subscribe(slow, "task:T1", testMaxTopics)
	hub.subscribe(fast, "task:T1", testMaxTopics)

	// First event fills slow's buffer (1 slot) and fast's.
	hub.Fanout(newTestEvent())
	// Second event: slow's buffer is full → evicted; fast still receives.
	hub.Fanout(newTestEvent())

	if !slow.closed.Load() {
		t.Fatal("slow client was not evicted")
	}
	if fast.closed.Load() {
		t.Fatal("fast client was wrongly evicted")
	}
	if got := testutil.ToFloat64(m.WSClientDroppedTotal.WithLabelValues("slow")); got != 1 {
		t.Fatalf("dropped{slow} = %v, want 1", got)
	}
	// fast got both events.
	for i := 0; i < 2; i++ {
		if _, ok := drain(t, fast); !ok {
			t.Fatalf("fast missing frame %d", i)
		}
	}
}

func TestHub_FallbackTsWhenAbsent(t *testing.T) {
	hub := NewHub(observability.NewMetrics())
	c := newTestConn(4)
	hub.register(c)
	hub.subscribe(c, "task:T1", testMaxTopics)

	ev := newTestEvent()
	ev.Ts = "" // worker ts absent → gateway fills receive time
	hub.Fanout(ev)

	f, ok := drain(t, c)
	if !ok || f.Ts == "" {
		t.Fatalf("expected a non-empty fallback ts, got %+v ok=%v", f, ok)
	}
}
