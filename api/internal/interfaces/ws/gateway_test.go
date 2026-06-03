package ws

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	apptask "github.com/whoisnian/agent-example/api/internal/application/task"
	"github.com/whoisnian/agent-example/api/internal/auth"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/messaging"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// wsTestSecret signs valid tokens for the gateway unit tests; the gateway is
// built with a Verifier over the same secret. fakeOwnership keys on the topic
// id alone, so the token's principal is arbitrary.
const wsTestSecret = "ws-unit-test-secret"

// mintTokenFor returns a valid HS256 token for a specific principal (so the
// integration ownership probe resolves to the seeded rows).
func mintTokenFor(t *testing.T, tenant, user uuid.UUID) string {
	t.Helper()
	tok, _, err := auth.NewIssuer(wsTestSecret, time.Hour).Issue(tenant, user)
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	return tok
}

// mintToken returns a valid HS256 token for an arbitrary principal.
func mintToken(t *testing.T) string {
	return mintTokenFor(t, uuid.New(), uuid.New())
}

// fakeOwnership owns exactly the ids in `owned`; everything else is
// ErrTaskNotFound / ErrVersionNotFound (not-found ≡ not-owned). It counts probes
// so the oversized-subscribe test can assert none ran.
type fakeOwnership struct {
	owned map[uuid.UUID]struct{}
	calls atomic.Int64
}

func (f *fakeOwnership) OwnsTask(_ context.Context, _, _, id uuid.UUID) error {
	f.calls.Add(1)
	if _, ok := f.owned[id]; ok {
		return nil
	}
	return apptask.ErrTaskNotFound
}

func (f *fakeOwnership) OwnsVersion(_ context.Context, _, _, id uuid.UUID) error {
	f.calls.Add(1)
	if _, ok := f.owned[id]; ok {
		return nil
	}
	return apptask.ErrVersionNotFound
}

// syncBuffer is a concurrency-safe log sink: the gateway logs from the server
// handler goroutine while the test reads from the test goroutine.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type anyFrame struct {
	Op      string          `json:"op"`
	Topic   string          `json:"topic"`
	Kind    string          `json:"kind"`
	Seq     int64           `json:"seq"`
	Ts      string          `json:"ts"`
	Payload json.RawMessage `json:"payload"`
}

// testGateway spins up a gin engine + httptest server hosting the gateway. It
// returns the gateway (for hub.Fanout), the ws:// URL, the log buffer, the
// metrics, and the fake ownership checker.
func testGateway(t *testing.T, cfg Config, own *fakeOwnership) (*Gateway, string, *syncBuffer, *observability.Metrics) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	buf := &syncBuffer{}
	logger := observability.NewLogger("info", buf)
	m := observability.NewMetrics()
	hub := NewHub(m)
	g := NewGateway(hub, own, cfg, logger, m, auth.NewVerifier(wsTestSecret))

	e := gin.New()
	v1 := e.Group("/api/v1")
	g.Register(v1)
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/ws"
	return g, wsURL, buf, m
}

func dial(t *testing.T, ctx context.Context, url string, opts *websocket.DialOptions) (*websocket.Conn, error) {
	t.Helper()
	c, _, err := websocket.Dial(ctx, url, opts)
	return c, err
}

func writeJSON(t *testing.T, ctx context.Context, c *websocket.Conn, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("client write: %v", err)
	}
}

func readFrame(t *testing.T, ctx context.Context, c *websocket.Conn) anyFrame {
	t.Helper()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	var f anyFrame
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("unmarshal frame: %v (%s)", err, data)
	}
	return f
}

func TestGateway_EmptyTokenRejected4001(t *testing.T) {
	_, wsURL, buf, _ := testGateway(t, Config{}, &fakeOwnership{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := dial(t, ctx, wsURL, nil) // no ?token
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.CloseNow()

	_, _, err = c.Read(ctx)
	if got := websocket.CloseStatus(err); got != 4001 {
		t.Fatalf("close status = %v, want 4001", got)
	}
	if strings.Contains(buf.String(), "token") && strings.Contains(strings.ToLower(buf.String()), "secret") {
		t.Fatal("token leaked into logs")
	}
}

func TestGateway_TokenNeverLogged(t *testing.T) {
	_, wsURL, buf, _ := testGateway(t, Config{}, &fakeOwnership{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	token := mintToken(t)
	c, err := dial(t, ctx, wsURL+"?token="+token, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// ping/pong to exercise the read loop, then close.
	writeJSON(t, ctx, c, map[string]string{"op": "ping"})
	_ = readFrame(t, ctx, c)
	_ = c.Close(websocket.StatusNormalClosure, "")

	if strings.Contains(buf.String(), token) {
		t.Fatalf("token leaked into logs:\n%s", buf.String())
	}
}

func TestGateway_InvalidTokenRejected4001(t *testing.T) {
	_, wsURL, _, _ := testGateway(t, Config{}, &fakeOwnership{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// A non-empty but bogus (wrong-signature/garbage) token is rejected the same
	// as a missing one: close 4001, no registration.
	c, err := dial(t, ctx, wsURL+"?token=not-a-valid-jwt", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.CloseNow()

	_, _, err = c.Read(ctx)
	if got := websocket.CloseStatus(err); got != 4001 {
		t.Fatalf("close status = %v, want 4001", got)
	}
}

func TestGateway_CrossOriginRejected(t *testing.T) {
	_, wsURL, _, _ := testGateway(t, Config{AllowedOrigins: []string{"good.example.com"}}, &fakeOwnership{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	opts := &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{"http://evil.example.com"}}}
	c, err := dial(t, ctx, wsURL+"?token="+mintToken(t), opts)
	if err == nil {
		c.CloseNow()
		t.Fatal("cross-origin handshake should have been rejected")
	}
}

func TestGateway_PingElicitsPong(t *testing.T) {
	_, wsURL, _, _ := testGateway(t, Config{}, &fakeOwnership{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := dial(t, ctx, wsURL+"?token="+mintToken(t), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.CloseNow()

	writeJSON(t, ctx, c, map[string]string{"op": "ping"})
	f := readFrame(t, ctx, c)
	if f.Op != "pong" {
		t.Fatalf("expected {op:pong}, got %+v", f)
	}
}

func TestGateway_UnknownOpErrorStaysOpen(t *testing.T) {
	_, wsURL, _, _ := testGateway(t, Config{}, &fakeOwnership{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := dial(t, ctx, wsURL+"?token="+mintToken(t), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.CloseNow()

	writeJSON(t, ctx, c, map[string]string{"op": "bogus"})
	f := readFrame(t, ctx, c)
	if f.Kind != "error" {
		t.Fatalf("expected error frame, got %+v", f)
	}
	// Connection stays open: a follow-up ping still gets a pong.
	writeJSON(t, ctx, c, map[string]string{"op": "ping"})
	if pong := readFrame(t, ctx, c); pong.Op != "pong" {
		t.Fatalf("connection not usable after error frame: %+v", pong)
	}
}

func TestGateway_MalformedTopicRejected(t *testing.T) {
	_, wsURL, _, _ := testGateway(t, Config{}, &fakeOwnership{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := dial(t, ctx, wsURL+"?token="+mintToken(t), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.CloseNow()

	writeJSON(t, ctx, c, map[string]any{"op": "subscribe", "topics": []string{"garbage"}})
	if f := readFrame(t, ctx, c); f.Kind != "error" {
		t.Fatalf("expected error frame for malformed topic, got %+v", f)
	}
}

func TestGateway_OwnershipScopedSubscriptions(t *testing.T) {
	owned := uuid.New()
	unowned := uuid.New()
	g, wsURL, _, _ := testGateway(t, Config{}, &fakeOwnership{owned: map[uuid.UUID]struct{}{owned: {}}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := dial(t, ctx, wsURL+"?token="+mintToken(t), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.CloseNow()

	// Subscribe to the owned task; the fan-out then reaches us. The subscribe
	// is processed asynchronously by the server reader loop, so wait until the
	// topic is registered before driving the event (avoids a delivery race).
	writeJSON(t, ctx, c, map[string]any{"op": "subscribe", "topics": []string{"task:" + owned.String()}})
	waitFor(t, func() bool { return g.hubHasTopic("task:" + owned.String()) })
	g.hub.Fanout(&messaging.FanoutEvent{TaskID: owned.String(), VersionID: uuid.NewString(), Seq: 1, Kind: "status", Ts: "2026-06-02T00:00:00Z", Payload: json.RawMessage(`{}`)})
	if f := readFrame(t, ctx, c); f.Topic != "task:"+owned.String() || f.Kind != "status" {
		t.Fatalf("expected owned-task event, got %+v", f)
	}

	// Subscribe to an unowned task → error frame, not added.
	writeJSON(t, ctx, c, map[string]any{"op": "subscribe", "topics": []string{"task:" + unowned.String()}})
	if f := readFrame(t, ctx, c); f.Kind != "error" {
		t.Fatalf("expected error frame for unowned task, got %+v", f)
	}
	// An event for the unowned task MUST NOT be delivered.
	g.hub.Fanout(&messaging.FanoutEvent{TaskID: unowned.String(), VersionID: uuid.NewString(), Seq: 2, Kind: "status", Ts: "2026-06-02T00:00:01Z", Payload: json.RawMessage(`{}`)})
	shortCtx, shortCancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer shortCancel()
	if _, _, err := c.Read(shortCtx); err == nil {
		t.Fatal("received an event for an unowned (unsubscribed) task")
	}
}

func TestGateway_OversizedSubscribeNoProbes(t *testing.T) {
	own := &fakeOwnership{owned: map[uuid.UUID]struct{}{}}
	_, wsURL, _, _ := testGateway(t, Config{MaxSubscribeTopics: 2}, own)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := dial(t, ctx, wsURL+"?token="+mintToken(t), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.CloseNow()

	topics := []string{"task:" + uuid.NewString(), "task:" + uuid.NewString(), "task:" + uuid.NewString()}
	writeJSON(t, ctx, c, map[string]any{"op": "subscribe", "topics": topics})
	if f := readFrame(t, ctx, c); f.Kind != "error" {
		t.Fatalf("expected error frame for oversized subscribe, got %+v", f)
	}
	if got := own.calls.Load(); got != 0 {
		t.Fatalf("ownership probed %d times on an oversized frame; want 0", got)
	}
}

func TestGateway_IdleReadDeadlineReaped(t *testing.T) {
	_, wsURL, _, m := testGateway(t, Config{ReadDeadline: 200 * time.Millisecond}, &fakeOwnership{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := dial(t, ctx, wsURL+"?token="+mintToken(t), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.CloseNow()

	// Send nothing; the server read deadline must close the connection. The
	// reap interrupts the in-flight Read (context-cancel), which closes
	// abnormally — the spec mandates a clean 1001 only for graceful shutdown,
	// not for reaping a vanished client — so we assert closure, not a code.
	if _, _, rerr := c.Read(ctx); rerr == nil {
		t.Fatal("idle connection was not closed by the read deadline")
	}
	// Give the handler a moment to unregister, then assert the gauges/counters.
	waitFor(t, func() bool { return testutil.ToFloat64(m.WSConnectionsActive) == 0 })
	if got := testutil.ToFloat64(m.WSClientDroppedTotal.WithLabelValues("read_deadline")); got != 1 {
		t.Fatalf("dropped{read_deadline} = %v, want 1", got)
	}
}

func TestGateway_ShutdownClosesWith1001(t *testing.T) {
	g, wsURL, _, m := testGateway(t, Config{}, &fakeOwnership{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := dial(t, ctx, wsURL+"?token="+mintToken(t), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.CloseNow()
	// Ensure the connection is registered before shutting down.
	waitFor(t, func() bool { return testutil.ToFloat64(m.WSConnectionsActive) == 1 })

	g.Shutdown()

	_, _, rerr := c.Read(ctx)
	if got := websocket.CloseStatus(rerr); got != websocket.StatusGoingAway {
		t.Fatalf("close status = %v, want 1001 (going away)", got)
	}
	waitFor(t, func() bool { return testutil.ToFloat64(m.WSConnectionsActive) == 0 })
}

// waitFor polls cond for up to 2s; fails the test if it never becomes true.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}
