//go:build integration

// Integration tests for the event-ingest state machine (Service.IngestEvent).
//
// Spins up a real PostgreSQL container, runs migrations, seeds a task+version
// via CreateTask, then drives IngestEvent and asserts the version/task status
// transitions, the terminal guard, idempotency, and the current-version guard.
//
// Run with: make test-integration  (i.e. `go test -tags=integration -race ./...`)
package task

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
)

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
	// .../api/internal/domain/task/event_sync_integration_test.go
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations")
}

type ingestSuite struct {
	pool    *pgxpool.Pool
	queries *sqlc.Queries
	svc     *Service
	tenant  uuid.UUID
	user    uuid.UUID
}

func newIngestSuite(t *testing.T) *ingestSuite {
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
	svc := NewService(pool, queries, SystemClock{}, UUIDv7Gen{}, "default", 30*time.Minute)

	return &ingestSuite{
		pool:    pool,
		queries: queries,
		svc:     svc,
		tenant:  uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		user:    uuid.MustParse("00000000-0000-0000-0000-000000000002"),
	}
}

// seedTask creates a task + first version (both pending, version active and
// current) and returns the ids.
func (s *ingestSuite) seedTask(t *testing.T) (taskID, versionID uuid.UUID) {
	t.Helper()
	out, err := s.svc.CreateTask(context.Background(), CreateInput{
		TenantID: s.tenant,
		UserID:   s.user,
		Title:    "ingest test",
		TaskType: "research",
		Prompt:   "do a thing",
	})
	if err != nil {
		t.Fatalf("seed CreateTask: %v", err)
	}
	return out.TaskID, out.VersionID
}

func (s *ingestSuite) versionStatus(t *testing.T, id uuid.UUID) string {
	t.Helper()
	row, err := s.queries.GetTaskVersionByID(context.Background(), toPgUUID(id))
	if err != nil {
		t.Fatalf("GetTaskVersionByID: %v", err)
	}
	return row.Status
}

func (s *ingestSuite) taskStatus(t *testing.T, id uuid.UUID) string {
	t.Helper()
	row, err := s.queries.GetTaskByID(context.Background(), toPgUUID(id))
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	return row.Status
}

func statusEvent(taskID, versionID, runID uuid.UUID, seq int64, status string) IngestEventInput {
	payload, _ := json.Marshal(map[string]string{"status": status})
	return IngestEventInput{
		TaskID: taskID, VersionID: versionID, RunID: runID,
		Seq: seq, Kind: "status", Payload: payload,
	}
}

func TestIngestStatusRunning(t *testing.T) {
	s := newIngestSuite(t)
	taskID, versionID := s.seedTask(t)
	runID := uuid.New()

	tr, err := s.svc.IngestEvent(context.Background(), statusEvent(taskID, versionID, runID, 1, "running"))
	if err != nil {
		t.Fatalf("IngestEvent: %v", err)
	}
	if !tr {
		t.Error("expected transitioned=true")
	}
	if got := s.versionStatus(t, versionID); got != "running" {
		t.Errorf("version status = %q, want running", got)
	}
	if got := s.taskStatus(t, taskID); got != "running" {
		t.Errorf("task status = %q, want running", got)
	}
	// exactly one event row
	rows, err := s.queries.ListEventsAfter(context.Background(), sqlc.ListEventsAfterParams{
		TaskID: toPgUUID(taskID), ID: 0, Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListEventsAfter: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("event rows = %d, want 1", len(rows))
	}
}

func TestIngestSucceededReleasesActive(t *testing.T) {
	s := newIngestSuite(t)
	taskID, versionID := s.seedTask(t)
	runID := uuid.New()

	_, _ = s.svc.IngestEvent(context.Background(), statusEvent(taskID, versionID, runID, 1, "running"))
	if _, err := s.svc.IngestEvent(context.Background(), statusEvent(taskID, versionID, runID, 2, "succeeded")); err != nil {
		t.Fatalf("IngestEvent succeeded: %v", err)
	}
	if got := s.versionStatus(t, versionID); got != "succeeded" {
		t.Errorf("version status = %q, want succeeded", got)
	}
	if got := s.taskStatus(t, taskID); got != "succeeded" {
		t.Errorf("task status = %q, want succeeded", got)
	}
	// is_active flipped → no active version for the task
	if _, err := s.queries.GetActiveVersionByTask(context.Background(), toPgUUID(taskID)); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("GetActiveVersionByTask err = %v, want ErrNoRows", err)
	}
}

func TestIngestErrorFails(t *testing.T) {
	s := newIngestSuite(t)
	taskID, versionID := s.seedTask(t)
	runID := uuid.New()

	payload := json.RawMessage(`{"code":"internal","message":"boom"}`)
	in := IngestEventInput{
		TaskID: taskID, VersionID: versionID, RunID: runID,
		Seq: 1, Kind: "error", Payload: payload,
	}
	tr, err := s.svc.IngestEvent(context.Background(), in)
	if err != nil {
		t.Fatalf("IngestEvent error: %v", err)
	}
	if !tr {
		t.Error("expected transitioned=true")
	}
	if got := s.versionStatus(t, versionID); got != "failed" {
		t.Errorf("version status = %q, want failed", got)
	}
	if got := s.taskStatus(t, taskID); got != "failed" {
		t.Errorf("task status = %q, want failed", got)
	}
	// error payload preserved in the event row
	rows, _ := s.queries.ListEventsAfter(context.Background(), sqlc.ListEventsAfterParams{
		TaskID: toPgUUID(taskID), ID: 0, Limit: 10,
	})
	if len(rows) != 1 || len(rows[0].Payload) == 0 {
		t.Fatalf("expected one event row with payload, got %d", len(rows))
	}
	var p map[string]string
	if err := json.Unmarshal(rows[0].Payload, &p); err != nil || p["code"] != "internal" {
		t.Errorf("event payload = %s, want code=internal", rows[0].Payload)
	}
}

func TestTerminalGuard(t *testing.T) {
	s := newIngestSuite(t)
	taskID, versionID := s.seedTask(t)
	runID := uuid.New()

	if _, err := s.svc.IngestEvent(context.Background(), statusEvent(taskID, versionID, runID, 1, "succeeded")); err != nil {
		t.Fatalf("succeeded: %v", err)
	}
	// late running event for an already-terminal version
	tr, err := s.svc.IngestEvent(context.Background(), statusEvent(taskID, versionID, runID, 2, "running"))
	if err != nil {
		t.Fatalf("late running: %v", err)
	}
	if tr {
		t.Error("expected transitioned=false for late event on terminal version")
	}
	if got := s.versionStatus(t, versionID); got != "succeeded" {
		t.Errorf("version status = %q, want succeeded (unchanged)", got)
	}
	if got := s.taskStatus(t, taskID); got != "succeeded" {
		t.Errorf("task status = %q, want succeeded (unchanged)", got)
	}
	// both events persisted
	rows, _ := s.queries.ListEventsAfter(context.Background(), sqlc.ListEventsAfterParams{
		TaskID: toPgUUID(taskID), ID: 0, Limit: 10,
	})
	if len(rows) != 2 {
		t.Errorf("event rows = %d, want 2", len(rows))
	}
}

func TestNonCurrentVersionDoesNotMoveTask(t *testing.T) {
	s := newIngestSuite(t)
	taskID, versionID := s.seedTask(t)
	runID := uuid.New()

	// Simulate the task having moved its current_version elsewhere (e.g. a
	// race where iterate advanced it) so versionID is no longer current.
	other := uuid.New()
	if _, err := s.pool.Exec(context.Background(),
		"UPDATE tasks SET current_version=$2 WHERE id=$1", toPgUUID(taskID), toPgUUID(other)); err != nil {
		t.Fatalf("flip current_version: %v", err)
	}
	before := s.taskStatus(t, taskID)

	tr, err := s.svc.IngestEvent(context.Background(), statusEvent(taskID, versionID, runID, 1, "running"))
	if err != nil {
		t.Fatalf("IngestEvent: %v", err)
	}
	// version still updates (it's non-terminal), so a transition happened
	if !tr {
		t.Error("expected version transition")
	}
	if got := s.versionStatus(t, versionID); got != "running" {
		t.Errorf("version status = %q, want running", got)
	}
	if got := s.taskStatus(t, taskID); got != before {
		t.Errorf("task status = %q, want unchanged %q", got, before)
	}
}

func TestDuplicateEventNoop(t *testing.T) {
	s := newIngestSuite(t)
	taskID, versionID := s.seedTask(t)
	runID := uuid.New()

	if _, err := s.svc.IngestEvent(context.Background(), statusEvent(taskID, versionID, runID, 1, "running")); err != nil {
		t.Fatalf("first: %v", err)
	}
	// redeliver same (run_id, seq) → no new row, no transition (IS DISTINCT FROM)
	tr, err := s.svc.IngestEvent(context.Background(), statusEvent(taskID, versionID, runID, 1, "running"))
	if err != nil {
		t.Fatalf("redeliver: %v", err)
	}
	if tr {
		t.Error("expected transitioned=false on redelivery")
	}
	rows, _ := s.queries.ListEventsAfter(context.Background(), sqlc.ListEventsAfterParams{
		TaskID: toPgUUID(taskID), ID: 0, Limit: 10,
	})
	if len(rows) != 1 {
		t.Errorf("event rows = %d, want 1 (dedupe)", len(rows))
	}
}

func TestUnknownStatusPersistOnly(t *testing.T) {
	s := newIngestSuite(t)
	taskID, versionID := s.seedTask(t)
	runID := uuid.New()

	tr, err := s.svc.IngestEvent(context.Background(), statusEvent(taskID, versionID, runID, 1, "bogus"))
	if err != nil {
		t.Fatalf("IngestEvent: %v", err)
	}
	if tr {
		t.Error("expected transitioned=false for unknown status")
	}
	if got := s.versionStatus(t, versionID); got != "pending" {
		t.Errorf("version status = %q, want pending (unchanged)", got)
	}
	rows, _ := s.queries.ListEventsAfter(context.Background(), sqlc.ListEventsAfterParams{
		TaskID: toPgUUID(taskID), ID: 0, Limit: 10,
	})
	if len(rows) != 1 {
		t.Errorf("event rows = %d, want 1 (persisted)", len(rows))
	}
}

// ---------------------------------------------------------------------------
// kind=title — semantic title updates (add-semantic-task-title)
// ---------------------------------------------------------------------------

func titleEvent(taskID, versionID, runID uuid.UUID, seq int64, title string) IngestEventInput {
	payload, _ := json.Marshal(map[string]string{"title": title})
	return IngestEventInput{
		TaskID: taskID, VersionID: versionID, RunID: runID,
		Seq: seq, Kind: "title", Payload: payload,
	}
}

func (s *ingestSuite) taskTitle(t *testing.T, id uuid.UUID) string {
	t.Helper()
	row, err := s.queries.GetTaskByID(context.Background(), toPgUUID(id))
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	return row.Title
}

func (s *ingestSuite) eventCount(t *testing.T, taskID uuid.UUID) int {
	t.Helper()
	rows, err := s.queries.ListEventsAfter(context.Background(), sqlc.ListEventsAfterParams{
		TaskID: toPgUUID(taskID), ID: 0, Limit: 50,
	})
	if err != nil {
		t.Fatalf("ListEventsAfter: %v", err)
	}
	return len(rows)
}

func TestIngestTitleUpdatesTaskTitle(t *testing.T) {
	s := newIngestSuite(t)
	taskID, versionID := s.seedTask(t)
	runID := uuid.New()

	applied, err := s.svc.IngestEvent(context.Background(),
		titleEvent(taskID, versionID, runID, 1, "重构用户认证模块"))
	if err != nil {
		t.Fatalf("IngestEvent: %v", err)
	}
	if !applied {
		t.Error("expected applied=true")
	}
	if got := s.taskTitle(t, taskID); got != "重构用户认证模块" {
		t.Errorf("title = %q, want 重构用户认证模块", got)
	}
	// Title events never touch the state machine.
	if got := s.taskStatus(t, taskID); got != "pending" {
		t.Errorf("task status = %q, want pending (unchanged)", got)
	}
	if got := s.eventCount(t, taskID); got != 1 {
		t.Errorf("event rows = %d, want 1", got)
	}
}

func TestIngestTitleAppliesAfterTerminal(t *testing.T) {
	s := newIngestSuite(t)
	taskID, versionID := s.seedTask(t)
	runID := uuid.New()

	_, _ = s.svc.IngestEvent(context.Background(), statusEvent(taskID, versionID, runID, 1, "running"))
	_, _ = s.svc.IngestEvent(context.Background(), statusEvent(taskID, versionID, runID, 2, "succeeded"))

	// Fast run: the title event lands after the task is terminal — still applies.
	applied, err := s.svc.IngestEvent(context.Background(),
		titleEvent(taskID, versionID, runID, 3, "Late but welcome"))
	if err != nil {
		t.Fatalf("IngestEvent title: %v", err)
	}
	if !applied {
		t.Error("expected applied=true after terminal status")
	}
	if got := s.taskTitle(t, taskID); got != "Late but welcome" {
		t.Errorf("title = %q", got)
	}
	if got := s.taskStatus(t, taskID); got != "succeeded" {
		t.Errorf("task status = %q, want succeeded (unchanged)", got)
	}
}

func TestIngestTitleRedeliveryNoop(t *testing.T) {
	s := newIngestSuite(t)
	taskID, versionID := s.seedTask(t)
	runID := uuid.New()

	if _, err := s.svc.IngestEvent(context.Background(),
		titleEvent(taskID, versionID, runID, 1, "First title")); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Overwrite the title out-of-band, then redeliver the same (run_id, seq):
	// the duplicate must not re-apply (insert no-op covers the title write).
	if _, err := s.pool.Exec(context.Background(),
		`UPDATE tasks SET title = 'manual' WHERE id = $1`, taskID); err != nil {
		t.Fatalf("manual update: %v", err)
	}
	applied, err := s.svc.IngestEvent(context.Background(),
		titleEvent(taskID, versionID, runID, 1, "First title"))
	if err != nil {
		t.Fatalf("redeliver: %v", err)
	}
	if applied {
		t.Error("expected applied=false on redelivery")
	}
	if got := s.taskTitle(t, taskID); got != "manual" {
		t.Errorf("title = %q, want manual (redelivery must not re-apply)", got)
	}
	if got := s.eventCount(t, taskID); got != 1 {
		t.Errorf("event rows = %d, want 1 (dedupe)", got)
	}
}

func TestIngestTitleEmptyOrMissingPersistOnly(t *testing.T) {
	s := newIngestSuite(t)
	taskID, versionID := s.seedTask(t)
	runID := uuid.New()

	// Whitespace-only payload.title → event row only, no title change.
	applied, err := s.svc.IngestEvent(context.Background(),
		titleEvent(taskID, versionID, runID, 1, "   \n\t "))
	if err != nil {
		t.Fatalf("whitespace title: %v", err)
	}
	if applied {
		t.Error("expected applied=false for whitespace title")
	}

	// Missing payload.title entirely.
	applied, err = s.svc.IngestEvent(context.Background(), IngestEventInput{
		TaskID: taskID, VersionID: versionID, RunID: runID,
		Seq: 2, Kind: "title", Payload: json.RawMessage(`{"unexpected":"shape"}`),
	})
	if err != nil {
		t.Fatalf("missing title: %v", err)
	}
	if applied {
		t.Error("expected applied=false for missing title")
	}

	if got := s.taskTitle(t, taskID); got != "ingest test" {
		t.Errorf("title = %q, want seeded title unchanged", got)
	}
	if got := s.eventCount(t, taskID); got != 2 {
		t.Errorf("event rows = %d, want 2 (both persisted)", got)
	}
}

func TestIngestTitleOversizedTruncated(t *testing.T) {
	s := newIngestSuite(t)
	taskID, versionID := s.seedTask(t)
	runID := uuid.New()

	long := strings.Repeat("汉", 100) // 100 runes / 300 bytes
	applied, err := s.svc.IngestEvent(context.Background(),
		titleEvent(taskID, versionID, runID, 1, long))
	if err != nil {
		t.Fatalf("IngestEvent: %v", err)
	}
	if !applied {
		t.Error("expected applied=true")
	}
	got := s.taskTitle(t, taskID)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("title %q should end with ellipsis", got)
	}
	if n := utf8.RuneCountInString(got); n > 64 {
		t.Errorf("title runes = %d, want <= 64 (including ellipsis)", n)
	}
	if n := len(got); n > 200 {
		t.Errorf("title bytes = %d, want <= 200", n)
	}
}

// --- kind=summary → task_versions.summary (refactor-task-conversation-continuity)

func summaryEvent(taskID, versionID, runID uuid.UUID, seq int64, summary string) IngestEventInput {
	payload, _ := json.Marshal(map[string]string{"summary": summary})
	return IngestEventInput{
		TaskID: taskID, VersionID: versionID, RunID: runID,
		Seq: seq, Kind: "summary", Payload: payload,
	}
}

func (s *ingestSuite) versionSummary(t *testing.T, id uuid.UUID) *string {
	t.Helper()
	row, err := s.queries.GetTaskVersionByID(context.Background(), toPgUUID(id))
	if err != nil {
		t.Fatalf("GetTaskVersionByID: %v", err)
	}
	return row.Summary
}

func TestIngestSummaryUpdatesVersionSummary(t *testing.T) {
	s := newIngestSuite(t)
	taskID, versionID := s.seedTask(t)
	runID := uuid.New()

	applied, err := s.svc.IngestEvent(context.Background(),
		summaryEvent(taskID, versionID, runID, 1, "完成登录页与表单校验"))
	if err != nil {
		t.Fatalf("IngestEvent: %v", err)
	}
	if !applied {
		t.Error("expected applied=true")
	}
	got := s.versionSummary(t, versionID)
	if got == nil || *got != "完成登录页与表单校验" {
		t.Errorf("summary = %v, want 完成登录页与表单校验", got)
	}
	// Summary events never touch the state machine.
	if got := s.versionStatus(t, versionID); got != "pending" {
		t.Errorf("version status = %q, want pending (unchanged)", got)
	}
	if got := s.eventCount(t, taskID); got != 1 {
		t.Errorf("event rows = %d, want 1", got)
	}
}

func TestIngestSummaryAppliesAfterTerminal(t *testing.T) {
	s := newIngestSuite(t)
	taskID, versionID := s.seedTask(t)
	runID := uuid.New()

	_, _ = s.svc.IngestEvent(context.Background(), statusEvent(taskID, versionID, runID, 1, "running"))
	_, _ = s.svc.IngestEvent(context.Background(), statusEvent(taskID, versionID, runID, 2, "succeeded"))

	// The summary event races the trailing status event — late still applies.
	applied, err := s.svc.IngestEvent(context.Background(),
		summaryEvent(taskID, versionID, runID, 3, "shipped the thing"))
	if err != nil {
		t.Fatalf("IngestEvent summary: %v", err)
	}
	if !applied {
		t.Error("expected applied=true after terminal status")
	}
	if got := s.versionSummary(t, versionID); got == nil || *got != "shipped the thing" {
		t.Errorf("summary = %v", got)
	}
	if got := s.versionStatus(t, versionID); got != "succeeded" {
		t.Errorf("version status = %q, want succeeded (unchanged)", got)
	}
}

func TestIngestSummaryRedeliveryNoop(t *testing.T) {
	s := newIngestSuite(t)
	taskID, versionID := s.seedTask(t)
	runID := uuid.New()

	if _, err := s.svc.IngestEvent(context.Background(),
		summaryEvent(taskID, versionID, runID, 1, "first summary")); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Overwrite out-of-band, then redeliver the same (run_id, seq): the
	// duplicate must not re-apply (insert no-op covers the summary write).
	if _, err := s.pool.Exec(context.Background(),
		`UPDATE task_versions SET summary = 'manual' WHERE id = $1`, versionID); err != nil {
		t.Fatalf("manual update: %v", err)
	}
	applied, err := s.svc.IngestEvent(context.Background(),
		summaryEvent(taskID, versionID, runID, 1, "first summary"))
	if err != nil {
		t.Fatalf("redeliver: %v", err)
	}
	if applied {
		t.Error("expected applied=false on redelivery")
	}
	if got := s.versionSummary(t, versionID); got == nil || *got != "manual" {
		t.Errorf("summary = %v, want manual (redelivery must not re-apply)", got)
	}
	if got := s.eventCount(t, taskID); got != 1 {
		t.Errorf("event rows = %d, want 1 (dedupe)", got)
	}
}

func TestIngestSummaryEmptyOrMissingPersistOnly(t *testing.T) {
	s := newIngestSuite(t)
	taskID, versionID := s.seedTask(t)
	runID := uuid.New()

	// Whitespace-only payload.summary → event row only, no column change.
	applied, err := s.svc.IngestEvent(context.Background(),
		summaryEvent(taskID, versionID, runID, 1, "   \n\t "))
	if err != nil {
		t.Fatalf("whitespace summary: %v", err)
	}
	if applied {
		t.Error("expected applied=false for whitespace summary")
	}

	// Missing payload.summary entirely.
	applied, err = s.svc.IngestEvent(context.Background(), IngestEventInput{
		TaskID: taskID, VersionID: versionID, RunID: runID,
		Seq: 2, Kind: "summary", Payload: json.RawMessage(`{"unexpected":"shape"}`),
	})
	if err != nil {
		t.Fatalf("missing summary: %v", err)
	}
	if applied {
		t.Error("expected applied=false for missing summary")
	}

	if got := s.versionSummary(t, versionID); got != nil {
		t.Errorf("summary = %v, want NULL unchanged", got)
	}
	if got := s.eventCount(t, taskID); got != 2 {
		t.Errorf("event rows = %d, want 2 (both persisted)", got)
	}
}

func TestIngestSummaryOversizedTruncated(t *testing.T) {
	s := newIngestSuite(t)
	taskID, versionID := s.seedTask(t)
	runID := uuid.New()

	long := strings.Repeat("汉", 800) // 2400 bytes > 2048
	applied, err := s.svc.IngestEvent(context.Background(),
		summaryEvent(taskID, versionID, runID, 1, long))
	if err != nil {
		t.Fatalf("IngestEvent: %v", err)
	}
	if !applied {
		t.Error("expected applied=true")
	}
	got := s.versionSummary(t, versionID)
	if got == nil {
		t.Fatal("summary is NULL, want truncated value")
	}
	if !strings.HasSuffix(*got, "…") {
		t.Errorf("summary should end with ellipsis")
	}
	if n := len(*got); n > 2048 {
		t.Errorf("summary bytes = %d, want <= 2048", n)
	}
	if !utf8.ValidString(*got) {
		t.Error("summary must remain valid UTF-8 (rune-boundary cut)")
	}
}
