package ws

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

// conn is one live WebSocket connection. Its outbound buffer `send` is bounded
// (design D4): the fan-out path does a non-blocking enqueue, and a full buffer
// triggers eviction rather than blocking the hub. One writer goroutine drains
// `send` to the socket; the reader loop runs in the HTTP handler goroutine.
//
// `subs` (the per-connection topic set) is guarded by Hub.mu — the fan-out path
// never touches it (it reads Hub.topics), only subscribe/unsubscribe/unregister
// mutate it. Keyed by topic so a duplicate subscribe is idempotent.
type conn struct {
	ws       *websocket.Conn
	send     chan []byte
	tenantID uuid.UUID
	userID   uuid.UUID

	subs map[string]struct{}

	closeOnce sync.Once
	closed    atomic.Bool // observable in tests; set once on close
}

func newConn(ws *websocket.Conn, tenantID, userID uuid.UUID, buffer int) *conn {
	return &conn{
		ws:       ws,
		send:     make(chan []byte, buffer),
		tenantID: tenantID,
		userID:   userID,
		subs:     make(map[string]struct{}),
	}
}

// enqueue is the backpressure primitive: a non-blocking send. It returns false
// when the buffer is full, so the caller (fan-out or a control reply) can evict
// the slow connection instead of stalling.
func (c *conn) enqueue(b []byte) bool {
	select {
	case c.send <- b:
		return true
	default:
		return false
	}
}

// writeLoop drains `send` to the socket until ctx is cancelled (connection
// teardown) or a write fails (socket closed). It is the SOLE writer goroutine,
// so coder/websocket's single-writer constraint is never violated.
func (c *conn) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case b := <-c.send:
			if err := c.ws.Write(ctx, websocket.MessageText, b); err != nil {
				return
			}
		}
	}
}

// close shuts the socket with the given code/reason exactly once. The first
// caller wins (e.g. a 1001 shutdown close beats the handler's normal-closure
// cleanup), so the client observes the meaningful code.
//
// The actual socket close runs in a goroutine: coder/websocket's Close performs
// a close handshake that blocks until the peer echoes or an internal timeout
// (~5s) elapses. Doing it inline would stall the fan-out path on a slow-client
// eviction (D4) and serialise shutdown across every connection (D7). The
// observable `closed` flag is set synchronously so callers/tests see the state
// flip immediately.
func (c *conn) close(code websocket.StatusCode, reason string) {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		if c.ws != nil { // nil only in hub unit tests that exercise eviction
			go func() { _ = c.ws.Close(code, reason) }()
		}
	})
}
