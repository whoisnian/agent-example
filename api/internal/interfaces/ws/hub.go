package ws

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/messaging"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// Hub owns the connection registry and the topic→conns index, both guarded by a
// single mutex (design D3). The index makes fan-out O(subscribers-of-topic),
// not O(all-connections). Subscription mutations and fan-out both touch the
// index, hence the shared lock; index ops are short map lookups.
type Hub struct {
	mu      sync.Mutex
	conns   map[*conn]struct{}
	topics  map[string]map[*conn]struct{}
	metrics *observability.Metrics
}

// NewHub builds an empty hub. metrics MUST be non-nil (the hub drives the
// active-connection / active-subscription gauges on every mutation).
func NewHub(m *observability.Metrics) *Hub {
	return &Hub{
		conns:   make(map[*conn]struct{}),
		topics:  make(map[string]map[*conn]struct{}),
		metrics: m,
	}
}

// register adds a freshly-accepted connection with zero subscriptions.
func (h *Hub) register(c *conn) {
	h.mu.Lock()
	h.conns[c] = struct{}{}
	h.mu.Unlock()
	h.metrics.WSConnectionsActive.Inc()
}

// unregister removes a connection and purges it from every topic set it was
// subscribed to (so a closed/evicted/reaped conn can never inflate a gauge or
// receive a stray frame). Idempotent: a second call is a no-op.
func (h *Hub) unregister(c *conn) {
	h.mu.Lock()
	if _, ok := h.conns[c]; !ok {
		h.mu.Unlock()
		return
	}
	delete(h.conns, c)
	removed := 0
	for topic := range c.subs {
		if set, ok := h.topics[topic]; ok {
			delete(set, c)
			if len(set) == 0 {
				delete(h.topics, topic)
			}
		}
		removed++
	}
	c.subs = make(map[string]struct{})
	h.mu.Unlock()

	h.metrics.WSConnectionsActive.Dec()
	if removed > 0 {
		h.metrics.WSSubscriptionsActive.Sub(float64(removed))
	}
}

// subscribe adds topic to the connection's set if it is under maxTopics. The
// return distinguishes the three outcomes the caller must handle:
//   - added=true:  newly subscribed (gauge incremented);
//   - both false:  already subscribed — idempotent, no double-count/double-deliver;
//   - capReached:  at the per-connection topic cap — caller emits an error frame.
func (h *Hub) subscribe(c *conn, topic string, maxTopics int) (added, capReached bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := c.subs[topic]; ok {
		return false, false
	}
	if len(c.subs) >= maxTopics {
		return false, true
	}
	c.subs[topic] = struct{}{}
	set, ok := h.topics[topic]
	if !ok {
		set = make(map[*conn]struct{})
		h.topics[topic] = set
	}
	set[c] = struct{}{}
	h.metrics.WSSubscriptionsActive.Inc()
	return true, false
}

// unsubscribe removes topic from the connection's set (no-op if not subscribed).
func (h *Hub) unsubscribe(c *conn, topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := c.subs[topic]; !ok {
		return
	}
	delete(c.subs, topic)
	if set, ok := h.topics[topic]; ok {
		delete(set, c)
		if len(set) == 0 {
			delete(h.topics, topic)
		}
	}
	h.metrics.WSSubscriptionsActive.Dec()
}

// Fanout delivers one decoded event to local subscribers of its two derived
// topics (design D3/D4). It forwards the worker-stamped ts, falling back to
// receive time only when absent (design D6). A connection subscribed to BOTH
// topics gets TWO frames (one per topic) — intended; the client dedups per topic.
func (h *Hub) Fanout(ev *messaging.FanoutEvent) {
	ts := ev.Ts
	if ts == "" {
		ts = time.Now().UTC().Format(time.RFC3339Nano)
	}
	h.deliver("task:"+ev.TaskID, ev, ts)
	h.deliver("version:"+ev.VersionID, ev, ts)
}

// deliver pushes one frame for `topic` to each subscribed connection with a
// non-blocking send. A full buffer evicts that connection (close +
// dropped{slow}) without blocking the others (design D4). Subscribers are
// snapshotted under the lock so the per-conn enqueue/evict happens lock-free.
func (h *Hub) deliver(topic string, ev *messaging.FanoutEvent, ts string) {
	h.mu.Lock()
	set := h.topics[topic]
	if len(set) == 0 {
		h.mu.Unlock()
		return
	}
	conns := make([]*conn, 0, len(set))
	for c := range set {
		conns = append(conns, c)
	}
	h.mu.Unlock()

	b, err := json.Marshal(serverFrame{
		Topic:   topic,
		Kind:    ev.Kind,
		Seq:     ev.Seq,
		Ts:      ts,
		Payload: ev.Payload,
	})
	if err != nil {
		return
	}
	for _, c := range conns {
		if c.enqueue(b) {
			h.metrics.WSEventsFannedTotal.WithLabelValues("delivered").Inc()
			continue
		}
		c.close(websocket.StatusTryAgainLater, "slow consumer")
		h.metrics.WSEventsFannedTotal.WithLabelValues("dropped").Inc()
		h.metrics.WSClientDroppedTotal.WithLabelValues("slow").Inc()
	}
}

// closeAll closes every live connection with the given code (1001 on shutdown).
// The connections' reader loops then exit and unregister themselves.
func (h *Hub) closeAll(code websocket.StatusCode, reason string) {
	h.mu.Lock()
	conns := make([]*conn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.Unlock()
	for _, c := range conns {
		c.close(code, reason)
	}
}
