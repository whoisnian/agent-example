//go:build integration

// Integration test for add-realtime-gateway. Stands up a real PostgreSQL
// container (for subscribe-time ownership) AND a net-new RabbitMQ container
// (none existed before this change — design "net-new RabbitMQ fixture"). It
// drives a real WebSocket client through the gateway and asserts: an owned
// task's event arrives carrying the worker-stamped ts; an unowned task's event
// is never delivered; and after an AMQP connection drop the exclusive fan-out
// queue is re-declared and delivery resumes (tasks 1.3 / 6.6).
//
// Reads run in a background goroutine: coder/websocket closes the connection
// when a Read's context expires, so a "read with timeout" would destroy the
// conn. The reader drains every frame into a channel and the test inspects it
// with time-based selects instead.
//
// Run with: make test-integration
package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcrabbitmq "github.com/testcontainers/testcontainers-go/modules/rabbitmq"
	"github.com/testcontainers/testcontainers-go/wait"

	apptask "github.com/whoisnian/agent-example/api/internal/application/task"
	taskdomain "github.com/whoisnian/agent-example/api/internal/domain/task"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/messaging"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
)

const (
	itPGImage     = "postgres:18.4-alpine"
	itPGUser      = "postgres"
	itPGPassword  = "postgres"
	itPGDatabase  = "agent_example"
	itRabbitImage = "rabbitmq:3.13-management-alpine"
)

func itMigrationsDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// .../api/internal/interfaces/ws/gateway_integration_test.go
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations")
}

func startPG(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, itPGImage,
		tcpostgres.WithDatabase(itPGDatabase),
		tcpostgres.WithUsername(itPGUser),
		tcpostgres.WithPassword(itPGPassword),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}
	mig, err := persistence.NewMigrator(itMigrationsDir(t), dsn)
	if err != nil {
		t.Fatalf("new migrator: %v", err)
	}
	if upErr := mig.Up(); upErr != nil {
		_ = mig.Close()
		t.Fatalf("migrate up: %v", upErr)
	}
	_ = mig.Close()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func startRabbit(t *testing.T) (amqpURL, httpURL string) {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcrabbitmq.Run(ctx, itRabbitImage)
	if err != nil {
		t.Fatalf("start rabbitmq: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })
	amqpURL, err = ctr.AmqpURL(ctx)
	if err != nil {
		t.Fatalf("amqp url: %v", err)
	}
	httpURL, err = ctr.HttpURL(ctx)
	if err != nil {
		t.Fatalf("http url: %v", err)
	}
	return amqpURL, httpURL
}

// seedTask inserts a task owned by (tenant,user) and returns its id + task_type.
func seedTask(t *testing.T, pool *pgxpool.Pool, tenant, user uuid.UUID) (uuid.UUID, string) {
	t.Helper()
	id := uuid.New()
	const taskType = "research"
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO tasks (id, tenant_id, user_id, title, task_type, status) VALUES ($1,$2,$3,$4,$5,$6)`,
		id, tenant, user, "live view", taskType, "running"); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	return id, taskType
}

// publishEvent publishes a worker TaskEvent (including ts) to task.events with
// the worker's 3-segment routing key event.<task_type>.<kind>.
func publishEvent(t *testing.T, amqpURL, taskType string, taskID, versionID uuid.UUID, seq int64, kind, ts string) {
	t.Helper()
	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		t.Fatalf("publish dial: %v", err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("publish channel: %v", err)
	}
	body, _ := json.Marshal(map[string]any{
		"task_id":    taskID.String(),
		"version_id": versionID.String(),
		"run_id":     uuid.NewString(),
		"seq":        seq,
		"kind":       kind,
		"ts":         ts,
		"payload":    json.RawMessage(`{"status":"running"}`),
	})
	if err := ch.PublishWithContext(context.Background(), messaging.ExchangeEvents,
		fmt.Sprintf("event.%s.%s", taskType, kind), false, false,
		amqp.Publishing{ContentType: "application/json", Body: body},
	); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

// forceConnectionDrop closes every live AMQP connection via the management API,
// simulating a broker-side connection drop so the gateway's exclusive queue
// must be re-declared on reconnect. The management connection list is
// eventually consistent (stats emission lag), so it polls until at least one
// connection is visible before deleting.
func forceConnectionDrop(t *testing.T, httpURL string) {
	t.Helper()
	type mgmtConn struct {
		Name string `json:"name"`
	}
	var conns []mgmtConn
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, httpURL+"/api/connections", nil)
		req.SetBasicAuth("guest", "guest")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("list connections: %v", err)
		}
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		conns = nil
		if err := json.Unmarshal(raw, &conns); err != nil {
			t.Fatalf("decode connections (%d): %s", resp.StatusCode, raw)
		}
		if len(conns) > 0 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if len(conns) == 0 {
		t.Fatal("no AMQP connections visible to force-drop within 20s")
	}
	for _, c := range conns {
		req, _ := http.NewRequest(http.MethodDelete, httpURL+"/api/connections/"+url.PathEscape(c.Name), nil)
		req.SetBasicAuth("guest", "guest")
		resp, derr := http.DefaultClient.Do(req)
		if derr != nil {
			t.Fatalf("delete connection: %v", derr)
		}
		_ = resp.Body.Close()
	}
}

// buildGateway wires a real gateway (PG ownership + RabbitMQ fan-out) onto a
// test HTTP server and returns the ws URL.
func buildGateway(t *testing.T, pool *pgxpool.Pool, mqConn *messaging.Connection, devTenant, devUser uuid.UUID) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger := observability.NewLogger("info", io.Discard)
	m := observability.NewMetrics()
	queries := sqlc.New(pool)
	ownership := apptask.NewOwnershipChecker(taskdomain.NewReadService(queries))
	hub := NewHub(m)
	g := NewGateway(hub, ownership, Config{}, logger, m, devTenant, devUser)

	consumer := messaging.NewFanoutConsumer(mqConn, 16, hub.Fanout, logger,
		m.WSFanoutConsumerConnected, m.WSFanoutMalformedTotal)
	consumerCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go consumer.Run(consumerCtx)

	e := gin.New()
	v1 := e.Group("/api/v1")
	g.Register(v1)
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return "ws" + srv.URL[len("http"):] + "/api/v1/ws"
}

// readFrames drains every inbound frame into a channel until the connection
// closes. The per-read context is the long-lived test context (NOT a per-read
// timeout), so reads never destroy the connection.
func readFrames(ctx context.Context, c *websocket.Conn) <-chan anyFrame {
	out := make(chan anyFrame, 128)
	go func() {
		defer close(out)
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			var f anyFrame
			if json.Unmarshal(data, &f) == nil {
				out <- f
			}
		}
	}()
	return out
}

// awaitSeq republishes on a 1s tick (covering dropped/unroutable publishes
// during consumer bind / reconnect) until an event frame with seq==wantSeq
// arrives, skipping control/error frames and stale-seq duplicates.
func awaitSeq(t *testing.T, frames <-chan anyFrame, wantSeq int64, publish func()) anyFrame {
	t.Helper()
	publish()
	tick := time.NewTicker(1 * time.Second)
	defer tick.Stop()
	deadline := time.After(25 * time.Second)
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatal("connection closed before the expected event arrived")
			}
			if f.Kind == "error" || f.Op == "pong" || f.Seq != wantSeq {
				continue
			}
			return f
		case <-tick.C:
			publish()
		case <-deadline:
			t.Fatalf("event seq=%d not delivered within the retry window", wantSeq)
		}
	}
}

// assertNoTopic fails if a frame for the given topic arrives within the window;
// frames for other topics (late duplicates) are tolerated.
func assertNoTopic(t *testing.T, frames <-chan anyFrame, topic string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				return
			}
			if f.Topic == topic {
				t.Fatalf("received an event for forbidden topic %s: %+v", topic, f)
			}
		case <-deadline:
			return
		}
	}
}

func declareTopology(t *testing.T, mqConn *messaging.Connection) {
	t.Helper()
	ch, err := mqConn.Channel()
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	if err := messaging.DeclareTopology(ch); err != nil {
		t.Fatalf("topology: %v", err)
	}
	_ = ch.Close()
}

func TestIntegration_FanoutDeliveryAndOwnership(t *testing.T) {
	pool := startPG(t)
	amqpURL, _ := startRabbit(t)

	mqConn, err := messaging.Dial(context.Background(), amqpURL, observability.NewLogger("info", io.Discard))
	if err != nil {
		t.Fatalf("mq dial: %v", err)
	}
	t.Cleanup(func() { _ = mqConn.Close() })
	declareTopology(t, mqConn)

	devTenant, devUser := uuid.New(), uuid.New()
	ownedTask, taskType := seedTask(t, pool, devTenant, devUser)
	unownedTask, unownedType := seedTask(t, pool, uuid.New(), uuid.New()) // different owner

	wsURL := buildGateway(t, pool, mqConn, devTenant, devUser)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL+"?token=x", nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer c.CloseNow()
	frames := readFrames(ctx, c)

	writeJSON(t, ctx, c, map[string]any{"op": "subscribe", "topics": []string{"task:" + ownedTask.String()}})

	const wantTs = "2026-06-02T12:00:00Z"
	versionID := uuid.New()
	got := awaitSeq(t, frames, 1, func() {
		publishEvent(t, amqpURL, taskType, ownedTask, versionID, 1, "status", wantTs)
	})
	if got.Topic != "task:"+ownedTask.String() || got.Kind != "status" || got.Ts != wantTs {
		t.Fatalf("unexpected delivered frame: %+v", got)
	}

	// An event for the unowned (unsubscribed) task MUST NOT be delivered.
	for i := 0; i < 3; i++ {
		publishEvent(t, amqpURL, unownedType, unownedTask, uuid.New(), 9, "status", wantTs)
	}
	assertNoTopic(t, frames, "task:"+unownedTask.String())
}

func TestIntegration_FanoutResumesAfterConnectionDrop(t *testing.T) {
	pool := startPG(t)
	amqpURL, httpURL := startRabbit(t)

	mqConn, err := messaging.Dial(context.Background(), amqpURL, observability.NewLogger("info", io.Discard))
	if err != nil {
		t.Fatalf("mq dial: %v", err)
	}
	t.Cleanup(func() { _ = mqConn.Close() })
	declareTopology(t, mqConn)

	devTenant, devUser := uuid.New(), uuid.New()
	ownedTask, taskType := seedTask(t, pool, devTenant, devUser)
	wsURL := buildGateway(t, pool, mqConn, devTenant, devUser)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL+"?token=x", nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer c.CloseNow()
	frames := readFrames(ctx, c)

	writeJSON(t, ctx, c, map[string]any{"op": "subscribe", "topics": []string{"task:" + ownedTask.String()}})
	vid := uuid.New()
	first := awaitSeq(t, frames, 1, func() {
		publishEvent(t, amqpURL, taskType, ownedTask, vid, 1, "status", "2026-06-02T12:00:00Z")
	})
	if first.Seq != 1 {
		t.Fatalf("pre-drop delivery failed: %+v", first)
	}

	// Drop the AMQP connection broker-side; the exclusive fan-out queue vanishes
	// and MUST be re-declared + re-bound by the consumer on reconnect.
	forceConnectionDrop(t, httpURL)

	// Delivery must resume. awaitSeq republishes seq=2 (reconnect + re-declare
	// takes a moment) and skips any stale seq=1 duplicate.
	second := awaitSeq(t, frames, 2, func() {
		publishEvent(t, amqpURL, taskType, ownedTask, vid, 2, "status", "2026-06-02T12:00:02Z")
	})
	if second.Seq != 2 {
		t.Fatalf("post-drop delivery failed: %+v", second)
	}
}
