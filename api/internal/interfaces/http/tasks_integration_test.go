//go:build integration

// HTTP-level integration tests for task-write-api.
//
// Spins up a real PostgreSQL 18.4 container, runs migrations, builds the
// real gin engine in-process via httptest.Server, and exercises both endpoints.
// The savepoint-rollback path is exercised by the concurrent-iterate scenario;
// the FOR UPDATE pre-check is exercised by the "active task" scenario.
//
// Run with: make test-integration  (i.e. `go test -tags=integration -race ./...`)
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	apptask "github.com/whoisnian/agent-example/api/internal/application/task"
	"github.com/whoisnian/agent-example/api/internal/auth"
	taskdomain "github.com/whoisnian/agent-example/api/internal/domain/task"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
	httpapi "github.com/whoisnian/agent-example/api/internal/interfaces/http"
)

// intgJWTSecret signs tokens for the integration server's Bearer middleware.
// intgAuthHeader mints a token for the fixed dev principal the suite seeds
// against, so owner-scoped reads resolve to the seeded rows.
const intgJWTSecret = "intg-test-secret"

// intgAuthHeaderFor mints a Bearer header for an arbitrary principal, signed
// with intgJWTSecret so the integration server's Verifier accepts it.
func intgAuthHeaderFor(tenant, user uuid.UUID) string {
	tok, _, err := auth.NewIssuer(intgJWTSecret, time.Hour).Issue(tenant, user)
	if err != nil {
		panic(err)
	}
	return "Bearer " + tok
}

// intgAuthHeader is the header for the fixed dev principal the task suite seeds.
func intgAuthHeader() string {
	return intgAuthHeaderFor(
		uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		uuid.MustParse("00000000-0000-0000-0000-000000000002"),
	)
}

// ---------------------------------------------------------------------------
// fixtures
// ---------------------------------------------------------------------------

const (
	pgImage    = "postgres:18.4-alpine"
	pgUser     = "postgres"
	pgPassword = "postgres"
	pgDatabase = "agent_example"
)

func migrationsDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// .../api/internal/interfaces/http/tasks_integration_test.go
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations")
}

// suite owns the per-test PostgreSQL container, pool, and gin engine.
type suite struct {
	ts          *httptest.Server
	pool        *pgxpool.Pool
	queries     *sqlc.Queries
	metrics     *observability.Metrics
	devTenantID uuid.UUID
	devUserID   uuid.UUID
}

func newSuite(t *testing.T) *suite {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, pgImage,
		tcpostgres.WithDatabase(pgDatabase),
		tcpostgres.WithUsername(pgUser),
		tcpostgres.WithPassword(pgPassword),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
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

	mig, err := persistence.NewMigrator(migrationsDir(t), dsn)
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

	queries := sqlc.New(pool)
	metrics := observability.NewMetrics()
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))

	devTenantID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	devUserID := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	domainSvc := taskdomain.NewService(
		pool,
		queries,
		taskdomain.SystemClock{},
		taskdomain.UUIDv7Gen{},
		"default",
		30*time.Minute,
	)
	appSvc := apptask.NewService(domainSvc)
	appReadSvc := apptask.NewReadService(taskdomain.NewReadService(queries))
	appCostReadSvc := apptask.NewCostReadService(taskdomain.NewCostReadService(queries))
	appControlSvc := apptask.NewControlService(
		taskdomain.NewControlService(pool, queries, taskdomain.SystemClock{}),
	)

	probes := httpapi.NewProbeRegistry(time.Second)
	engine := httpapi.NewEngine(&httpapi.ServerDeps{
		Logger:   logger,
		Metrics:  metrics,
		Probes:   probes,
		Verifier: auth.NewVerifier(intgJWTSecret),
		TaskHandlers: &httpapi.TaskHandlers{
			App:     appSvc,
			Logger:  logger,
			Metrics: metrics,
		},
		TaskReadHandlers: &httpapi.TaskReadHandlers{
			App:    appReadSvc,
			Logger: logger,
		},
		TaskCostHandlers: &httpapi.TaskCostHandlers{
			App:    appCostReadSvc,
			Logger: logger,
		},
		TaskControlHandlers: &httpapi.TaskControlHandlers{
			App:     appControlSvc,
			Logger:  logger,
			Metrics: metrics,
		},
	})

	ts := httptest.NewServer(engine)
	t.Cleanup(ts.Close)

	return &suite{
		ts:          ts,
		pool:        pool,
		queries:     queries,
		metrics:     metrics,
		devTenantID: devTenantID,
		devUserID:   devUserID,
	}
}

// envelope is the assert-side counterpart to httpapi.Envelope (we cannot share
// the struct directly because Data is concretely typed there).
type envelope struct {
	Code    any             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	TraceID string          `json:"trace_id"`
}

// postJSON is a convenience that returns (status, decoded envelope).
func postJSON(t *testing.T, ts *httptest.Server, path string, body any) (int, envelope) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, &buf)
	if err != nil {
		t.Fatalf("build POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", intgAuthHeader())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	var env envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return resp.StatusCode, env
}

// ---------------------------------------------------------------------------
// 7.4 — POST /api/v1/tasks happy path
// ---------------------------------------------------------------------------

func TestCreateTaskHappy(t *testing.T) {
	s := newSuite(t)
	status, env := postJSON(t, s.ts, "/api/v1/tasks", map[string]any{
		"title":     "build music app",
		"task_type": "code-gen",
		"prompt":    "react desktop music app",
	})
	if status != http.StatusCreated {
		t.Fatalf("status=%d env=%+v", status, env)
	}
	var data struct {
		TaskID    uuid.UUID `json:"task_id"`
		VersionID uuid.UUID `json:"version_id"`
		VersionNo int32     `json:"version_no"`
		Status    string    `json:"status"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("data unmarshal: %v body=%s", err, env.Data)
	}
	if data.VersionNo != 1 || data.Status != "pending" {
		t.Fatalf("unexpected envelope payload: %+v", data)
	}

	// Cross-check DB state.
	ctx := context.Background()
	var (
		taskCount, versionCount, runCount, outboxCount int
		topic                                          string
		payloadRaw                                     []byte
	)
	if err := s.pool.QueryRow(ctx,
		"SELECT count(*) FROM tasks WHERE id = $1", data.TaskID).Scan(&taskCount); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if err := s.pool.QueryRow(ctx,
		"SELECT count(*) FROM task_versions WHERE id = $1 AND is_active", data.VersionID).Scan(&versionCount); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	if err := s.pool.QueryRow(ctx,
		"SELECT count(*) FROM task_runs WHERE version_id = $1 AND status = 'queued' AND attempt_no = 1", data.VersionID).Scan(&runCount); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if err := s.pool.QueryRow(ctx,
		"SELECT count(*), topic, payload FROM outbox WHERE aggregate_id = $1 GROUP BY topic, payload",
		data.VersionID).Scan(&outboxCount, &topic, &payloadRaw); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if taskCount != 1 || versionCount != 1 || runCount != 1 || outboxCount != 1 {
		t.Fatalf("row counts task=%d version=%d run=%d outbox=%d", taskCount, versionCount, runCount, outboxCount)
	}
	if topic != "execute.code-gen.default" {
		t.Fatalf("wrong outbox topic: %q", topic)
	}
	var payload map[string]any
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if payload["task_type"] != "code-gen" {
		t.Fatalf("payload.task_type=%v", payload["task_type"])
	}
	if payload["lane"] != "default" {
		t.Fatalf("payload.lane=%v", payload["lane"])
	}
	if payload["parent_version_id"] != nil {
		t.Fatalf("expected null parent_version_id, got %v", payload["parent_version_id"])
	}
	if payload["attempt_no"].(float64) != 1 {
		t.Fatalf("attempt_no=%v", payload["attempt_no"])
	}
}

// ---------------------------------------------------------------------------
// 7.4b — POST /api/v1/tasks title derivation (add-task-title-autogen)
// ---------------------------------------------------------------------------

func TestCreateTitleDerived(t *testing.T) {
	s := newSuite(t)
	ctx := context.Background()

	fetchTitle := func(t *testing.T, env envelope) string {
		t.Helper()
		var data struct {
			TaskID uuid.UUID `json:"task_id"`
		}
		if err := json.Unmarshal(env.Data, &data); err != nil {
			t.Fatalf("data unmarshal: %v body=%s", err, env.Data)
		}
		var title string
		if err := s.pool.QueryRow(ctx,
			"SELECT title FROM tasks WHERE id = $1", data.TaskID).Scan(&title); err != nil {
			t.Fatalf("select title: %v", err)
		}
		return title
	}

	// Absent title → first non-empty prompt line.
	st, env := postJSON(t, s.ts, "/api/v1/tasks", map[string]any{
		"task_type": "code-gen",
		"prompt":    "\n  build a music app  \nwith react",
	})
	if st != http.StatusCreated {
		t.Fatalf("absent title: status=%d env=%+v", st, env)
	}
	if got := fetchTitle(t, env); got != "build a music app" {
		t.Fatalf("derived title=%q", got)
	}

	// Long first line → 64-rune cut with ellipsis, still ≤ 200 bytes.
	st, env = postJSON(t, s.ts, "/api/v1/tasks", map[string]any{
		"task_type": "code-gen",
		"prompt":    strings.Repeat("x", 100),
	})
	if st != http.StatusCreated {
		t.Fatalf("long prompt: status=%d env=%+v", st, env)
	}
	if got := fetchTitle(t, env); got != strings.Repeat("x", 64)+"…" {
		t.Fatalf("truncated title=%q", got)
	}

	// All-whitespace prompt is legal input (no trim in prompt validation) and
	// falls back to the literal.
	st, env = postJSON(t, s.ts, "/api/v1/tasks", map[string]any{
		"task_type": "code-gen",
		"prompt":    "   ",
	})
	if st != http.StatusCreated {
		t.Fatalf("whitespace prompt: status=%d env=%+v", st, env)
	}
	if got := fetchTitle(t, env); got != "Untitled task" {
		t.Fatalf("fallback title=%q", got)
	}

	// Explicit title is preserved verbatim (trimmed), never derived.
	st, env = postJSON(t, s.ts, "/api/v1/tasks", map[string]any{
		"title":     "  my explicit title  ",
		"task_type": "code-gen",
		"prompt":    "something entirely different",
	})
	if st != http.StatusCreated {
		t.Fatalf("explicit title: status=%d env=%+v", st, env)
	}
	if got := fetchTitle(t, env); got != "my explicit title" {
		t.Fatalf("explicit title=%q", got)
	}
}

// ---------------------------------------------------------------------------
// 7.5 — POST /iterate happy path on a terminal task
// ---------------------------------------------------------------------------

func TestIterateHappy(t *testing.T) {
	s := newSuite(t)
	// Create v1, then forcibly mark it succeeded so iterate is allowed.
	taskID, v1ID := createAndTerminate(t, s, "research", "v1 prompt", "succeeded", "artifacts/v1/")

	status, env := postJSON(t, s.ts, "/api/v1/tasks/"+taskID.String()+"/iterate", map[string]any{
		"prompt": "iterate v2",
	})
	if status != http.StatusCreated {
		t.Fatalf("status=%d env=%+v", status, env)
	}
	var data struct {
		VersionID uuid.UUID `json:"version_id"`
		VersionNo int32     `json:"version_no"`
		Status    string    `json:"status"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("data unmarshal: %v", err)
	}
	if data.VersionNo != 2 || data.Status != "pending" {
		t.Fatalf("unexpected: %+v", data)
	}

	// tasks.current_version should now be v2.
	ctx := context.Background()
	var current uuid.UUID
	if err := s.pool.QueryRow(ctx,
		"SELECT current_version FROM tasks WHERE id = $1", taskID).Scan(&current); err != nil {
		t.Fatalf("current_version: %v", err)
	}
	if current != data.VersionID {
		t.Fatalf("current_version=%s want=%s", current, data.VersionID)
	}

	// Outbox payload should reference v1 as parent and inherit artifact_root.
	var payloadRaw []byte
	if err := s.pool.QueryRow(ctx,
		"SELECT payload FROM outbox WHERE aggregate_id = $1", data.VersionID).Scan(&payloadRaw); err != nil {
		t.Fatalf("outbox payload: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if payload["parent_version_id"] != v1ID.String() {
		t.Fatalf("parent_version_id=%v want=%s", payload["parent_version_id"], v1ID)
	}
	if payload["parent_artifact_root"] != "artifacts/v1/" {
		t.Fatalf("parent_artifact_root=%v", payload["parent_artifact_root"])
	}
}

// ---------------------------------------------------------------------------
// 7.6 — concurrent iterate
// ---------------------------------------------------------------------------

func TestIterateConcurrent(t *testing.T) {
	s := newSuite(t)
	taskID, _ := createAndTerminate(t, s, "code-gen", "v1", "succeeded", "")

	type outcome struct {
		status int
		env    envelope
	}
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, env := postJSON(t, s.ts, "/api/v1/tasks/"+taskID.String()+"/iterate", map[string]any{
				"prompt": "iterate me",
			})
			results <- outcome{status: st, env: env}
		}()
	}
	wg.Wait()
	close(results)

	var success, conflict outcome
	got := 0
	for r := range results {
		got++
		switch r.status {
		case http.StatusCreated:
			success = r
		case http.StatusConflict:
			conflict = r
		default:
			t.Fatalf("unexpected status %d env=%+v", r.status, r.env)
		}
	}
	if got != 2 {
		t.Fatalf("got %d results", got)
	}
	if success.env.Code != float64(0) || conflict.env.Code != "active_version_exists" {
		t.Fatalf("envelope codes wrong: success=%v conflict=%v", success.env.Code, conflict.env.Code)
	}

	var successData struct {
		VersionID uuid.UUID `json:"version_id"`
	}
	if err := json.Unmarshal(success.env.Data, &successData); err != nil {
		t.Fatalf("success data: %v", err)
	}
	var conflictData struct {
		ActiveVersionID     uuid.UUID `json:"active_version_id"`
		ActiveVersionStatus string    `json:"active_version_status"`
	}
	if err := json.Unmarshal(conflict.env.Data, &conflictData); err != nil {
		t.Fatalf("conflict data: %v", err)
	}
	if conflictData.ActiveVersionID != successData.VersionID {
		t.Fatalf("conflict.active_version_id=%s != success.version_id=%s",
			conflictData.ActiveVersionID, successData.VersionID)
	}
	if conflictData.ActiveVersionStatus != "pending" {
		t.Fatalf("conflict.active_version_status=%q", conflictData.ActiveVersionStatus)
	}

	// Exactly one new active row should exist.
	ctx := context.Background()
	var active int
	if err := s.pool.QueryRow(ctx,
		"SELECT count(*) FROM task_versions WHERE task_id = $1 AND is_active", taskID).Scan(&active); err != nil {
		t.Fatalf("count active: %v", err)
	}
	if active != 1 {
		t.Fatalf("active count=%d", active)
	}
}

// ---------------------------------------------------------------------------
// 7.7 — iterate against running version short-circuits at app layer
// ---------------------------------------------------------------------------

func TestIterateAgainstRunningShortCircuits(t *testing.T) {
	s := newSuite(t)
	// Build a task whose v1 is "running" (active). The app-level pre-check
	// should fire before the savepoint-wrapped INSERT.
	taskID, v1ID := createTask(t, s, "code-gen", "v1")
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx,
		"UPDATE task_versions SET status = 'running' WHERE id = $1", v1ID); err != nil {
		t.Fatalf("force running: %v", err)
	}
	if _, err := s.pool.Exec(ctx,
		"UPDATE tasks SET status = 'running' WHERE id = $1", taskID); err != nil {
		t.Fatalf("force running on tasks: %v", err)
	}

	status, env := postJSON(t, s.ts, "/api/v1/tasks/"+taskID.String()+"/iterate", map[string]any{
		"prompt": "should fail",
	})
	if status != http.StatusConflict || env.Code != "active_version_exists" {
		t.Fatalf("status=%d env=%+v", status, env)
	}
	var data struct {
		ActiveVersionID     uuid.UUID `json:"active_version_id"`
		ActiveVersionStatus string    `json:"active_version_status"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("data: %v", err)
	}
	if data.ActiveVersionID != v1ID || data.ActiveVersionStatus != "running" {
		t.Fatalf("data=%+v", data)
	}
	// And no row should have leaked into task_versions / task_runs / outbox.
	var versionCount, runCount, outboxCount int
	if err := s.pool.QueryRow(ctx,
		"SELECT count(*) FROM task_versions WHERE task_id = $1", taskID).Scan(&versionCount); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	if err := s.pool.QueryRow(ctx,
		"SELECT count(*) FROM task_runs WHERE version_id IN (SELECT id FROM task_versions WHERE task_id=$1)",
		taskID).Scan(&runCount); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if err := s.pool.QueryRow(ctx,
		"SELECT count(*) FROM outbox WHERE aggregate_id IN (SELECT id FROM task_versions WHERE task_id=$1)",
		taskID).Scan(&outboxCount); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	// versionCount == 1 (just v1); runCount == 1 (just v1's queued run); outboxCount == 1 (v1's).
	if versionCount != 1 || runCount != 1 || outboxCount != 1 {
		t.Fatalf("leaked rows v=%d r=%d o=%d", versionCount, runCount, outboxCount)
	}
}

// ---------------------------------------------------------------------------
// 7.8 — 404 cases
// ---------------------------------------------------------------------------

func TestIterate404(t *testing.T) {
	s := newSuite(t)

	// Unknown task.
	st, env := postJSON(t, s.ts, "/api/v1/tasks/"+uuid.New().String()+"/iterate", map[string]any{
		"prompt": "hi",
	})
	if st != http.StatusNotFound || env.Code != "task_not_found" {
		t.Fatalf("unknown task: status=%d env=%+v", st, env)
	}

	// base_version_id from a different task.
	taskA, _ := createAndTerminate(t, s, "code-gen", "A v1", "succeeded", "")
	_, taskBv1 := createAndTerminate(t, s, "code-gen", "B v1", "succeeded", "")
	body := map[string]any{
		"prompt":          "cross-task base",
		"base_version_id": taskBv1.String(),
	}
	st, env = postJSON(t, s.ts, "/api/v1/tasks/"+taskA.String()+"/iterate", body)
	if st != http.StatusNotFound || env.Code != "version_not_found" {
		t.Fatalf("cross-task base: status=%d env=%+v", st, env)
	}
}

// ---------------------------------------------------------------------------
// 7.9 — 400 cases
// ---------------------------------------------------------------------------

func TestCreate400(t *testing.T) {
	s := newSuite(t)

	// Oversize explicit title (absent title is legal — see TestCreateTitleDerived).
	st, env := postJSON(t, s.ts, "/api/v1/tasks", map[string]any{
		"title":     strings.Repeat("a", 201),
		"task_type": "code-gen",
		"prompt":    "hi",
	})
	if st != http.StatusBadRequest || env.Code != "invalid_input" {
		t.Fatalf("oversize title: status=%d env=%+v", st, env)
	}

	// Invalid task_type pattern.
	st, env = postJSON(t, s.ts, "/api/v1/tasks", map[string]any{
		"title":     "ok",
		"task_type": "Code-Gen",
		"prompt":    "hi",
	})
	if st != http.StatusBadRequest || env.Code != "invalid_input" {
		t.Fatalf("bad task_type: status=%d env=%+v", st, env)
	}

	// Oversize params.
	big := strings.Repeat("a", 33*1024)
	st, env = postJSON(t, s.ts, "/api/v1/tasks", map[string]any{
		"title":     "ok",
		"task_type": "code-gen",
		"prompt":    "hi",
		"params":    map[string]string{"k": big},
	})
	if st != http.StatusBadRequest || env.Code != "invalid_input" {
		t.Fatalf("oversize params: status=%d env=%+v", st, env)
	}
}

// ---------------------------------------------------------------------------
// 7.10 — metrics
// ---------------------------------------------------------------------------

func TestCreateMetric(t *testing.T) {
	s := newSuite(t)
	_, _ = postJSON(t, s.ts, "/api/v1/tasks", map[string]any{
		"title":     "metric",
		"task_type": "research",
		"prompt":    "go",
	})
	// scrape /metrics
	resp, err := http.Get(s.ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	want := `tasks_created_total{task_type="research"} 1`
	if !strings.Contains(string(body), want) {
		t.Fatalf("metric missing: want %q\nin %s", want, body)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// createTask issues POST /tasks and returns (task_id, version_id).
//
//nolint:unparam // unnamedResult: two-UUID returns are obvious in context (taskID, versionID).
func createTask(t *testing.T, s *suite, taskType, prompt string) (taskID, versionID uuid.UUID) {
	t.Helper()
	st, env := postJSON(t, s.ts, "/api/v1/tasks", map[string]any{
		"title":     "t",
		"task_type": taskType,
		"prompt":    prompt,
	})
	if st != http.StatusCreated {
		t.Fatalf("createTask status=%d env=%+v", st, env)
	}
	var data struct {
		TaskID    uuid.UUID `json:"task_id"`
		VersionID uuid.UUID `json:"version_id"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("createTask data: %v", err)
	}
	return data.TaskID, data.VersionID
}

// createAndTerminate creates a task and immediately flips v1 to a terminal
// state so iterate is allowed. Also stamps tasks.status accordingly.
//
//nolint:unparam // unnamedResult: two-UUID returns are obvious in context (taskID, versionID).
func createAndTerminate(t *testing.T, s *suite, taskType, prompt, terminalStatus, artifactRoot string) (taskID, versionID uuid.UUID) {
	t.Helper()
	taskID, versionID = createTask(t, s, taskType, prompt)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx,
		`UPDATE task_versions SET status = $2, artifact_root = NULLIF($3, '') WHERE id = $1`,
		versionID, terminalStatus, artifactRoot); err != nil {
		t.Fatalf("terminate version: %v", err)
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE tasks SET status = $2 WHERE id = $1`,
		taskID, terminalStatus); err != nil {
		t.Fatalf("terminate task: %v", err)
	}
	return taskID, versionID
}

// silence unused import warnings if a helper goes away.
var _ = fmt.Sprintf
