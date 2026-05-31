//go:build integration

// Schema-level integration tests for the task and cost domains.
//
// Spins up a real PostgreSQL 18.4 container per test class via testcontainers
// and asserts:
//   - structural integrity (every table, UNIQUE, FK, CHECK declared in spec
//     is present; `task_versions.is_active` is a generated stored column)
//   - migration up/down round-trip for outbox (0001) and task/cost (0002, 0003)
//     leaves no residue
//   - the load-bearing mutex (`one_active_version_per_task`) blocks concurrent
//     active versions for the same task_id
//   - per-run idempotency uniques on task_events, cost_events, task_checkpoints
//   - pricing window invariants
//
// Run with: make test-integration  (i.e. `go test -tags=integration -race ./...`)
package persistence_test

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence"
)

// ---------------------------------------------------------------------------
// shared fixtures
// ---------------------------------------------------------------------------

const (
	pgImage    = "postgres:18.4-alpine"
	pgUser     = "postgres"
	pgPassword = "postgres"
	pgDatabase = "agent_example"
)

// migrationsDir resolves to <repo>/api/migrations regardless of where `go test`
// is invoked from. We walk up from this file's directory to reach `api/`.
func migrationsDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// .../api/internal/infrastructure/persistence/migrations_integration_test.go
	// climb four levels to reach api/, then descend into migrations/.
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations")
}

// newPostgresContainer starts a fresh PostgreSQL 18.4 container and returns
// its DSN. The caller is responsible for terminate via t.Cleanup.
func newPostgresContainer(t *testing.T) string {
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
	t.Cleanup(func() {
		_ = ctr.Terminate(context.Background())
	})
	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}
	return dsn
}

// migrateUp applies all migrations from disk against dsn.
func migrateUp(t *testing.T, dsn string) {
	t.Helper()
	m, err := persistence.NewMigrator(migrationsDir(t), dsn)
	if err != nil {
		t.Fatalf("new migrator: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	if err := m.Up(); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
}

// migrateAllDown rolls back step-by-step until no migrations remain.
func migrateAllDown(t *testing.T, dsn string) {
	t.Helper()
	m, err := persistence.NewMigrator(migrationsDir(t), dsn)
	if err != nil {
		t.Fatalf("new migrator: %v", err)
	}
	defer func() { _ = m.Close() }()
	// Step down until the version row reports either ErrNilVersion (no row in
	// schema_migrations — clean slate) OR version 0 (golang-migrate v4.19.1's
	// Postgres driver writes version=0 after the last down instead of clearing
	// the table; either marker means "no migration applied"). Cap at 20 to fail
	// loud if a future migration is added without updating this guard.
	for i := 0; i < 20; i++ {
		v, _, vErr := m.Version()
		if errors.Is(vErr, migrate.ErrNilVersion) || v == 0 {
			return
		}
		if vErr != nil {
			t.Fatalf("read version: %v", vErr)
		}
		if err := m.Down(); err != nil {
			t.Fatalf("migrate down (version was %d): %v", v, err)
		}
	}
	t.Fatalf("migrateAllDown: still has migrations after 20 steps")
}

// connect returns a fresh pgx connection that the caller closes via t.Cleanup.
func connect(t *testing.T, dsn string) *pgx.Conn {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// pgErrorCode extracts the SQLSTATE and constraint name from a pgx error.
func pgErrorCode(t *testing.T, err error) (code, constraint string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected pgconn.PgError, got %T: %v", err, err)
	}
	return pgErr.Code, pgErr.ConstraintName
}

// ---------------------------------------------------------------------------
// 5.1 structural integrity
// ---------------------------------------------------------------------------

func TestStructuralIntegrity(t *testing.T) {
	t.Parallel()
	dsn := newPostgresContainer(t)
	migrateUp(t, dsn)
	conn := connect(t, dsn)
	ctx := context.Background()

	// Every table from both specs is present.
	wantTables := []string{
		"tasks", "task_versions", "task_runs",
		"task_events", "task_checkpoints", "artifacts",
		"pricing", "cost_events", "task_costs",
		"outbox", // sanity check we didn't disturb 0001
	}
	for _, tbl := range wantTables {
		var n int
		if err := conn.QueryRow(ctx,
			`SELECT 1 FROM pg_class c
			 JOIN pg_namespace n ON n.oid = c.relnamespace
			 WHERE c.relname = $1 AND n.nspname = 'public' AND c.relkind = 'r'`,
			tbl).Scan(&n); err != nil {
			t.Errorf("table %q missing: %v", tbl, err)
		}
	}

	// task_versions.is_active must be a generated STORED column.
	// pg_attribute.attgenerated is a `char` (OID 18); cast to text so pgx
	// binary protocol can scan into a Go string.
	var attgenerated string
	if err := conn.QueryRow(ctx,
		`SELECT a.attgenerated::text
		 FROM pg_attribute a
		 JOIN pg_class c ON c.oid = a.attrelid
		 WHERE c.relname = 'task_versions' AND a.attname = 'is_active'`,
	).Scan(&attgenerated); err != nil {
		t.Fatalf("read is_active attgenerated: %v", err)
	}
	if attgenerated != "s" {
		t.Errorf("is_active attgenerated = %q, want \"s\" (STORED)", attgenerated)
	}

	// The mutex partial unique index exists.
	var indexDef string
	if err := conn.QueryRow(ctx,
		`SELECT indexdef FROM pg_indexes
		 WHERE indexname = 'one_active_version_per_task'`,
	).Scan(&indexDef); err != nil {
		t.Fatalf("read mutex index: %v", err)
	}
	if !strings.Contains(strings.ToLower(indexDef), "where is_active") {
		t.Errorf("one_active_version_per_task index missing WHERE clause: %s", indexDef)
	}

	// A few representative CHECK / UNIQUE constraints must be present.
	wantConstraints := []string{
		"tasks_status_check",
		"task_versions_status_check",
		"task_versions_task_version_no_key",
		"task_runs_idempotency_key_key",
		"task_runs_version_attempt_key",
		"pricing_kind_check",
		"pricing_window_check",
		"pricing_unique_effective",
		"cost_events_kind_check",
	}
	for _, name := range wantConstraints {
		var n int
		if err := conn.QueryRow(ctx,
			`SELECT 1 FROM pg_constraint WHERE conname = $1`, name,
		).Scan(&n); err != nil {
			t.Errorf("constraint %q missing: %v", name, err)
		}
	}

	// A few representative indexes (existence, not query plan).
	// cost_events_run_kind_seq_key replaces the legacy cost_events_run_seq_key
	// as of migration 0004_cost_events_kind_unique (add-cost-service).
	wantIndexes := []string{
		"tasks_tenant_user_status_idx",
		"task_versions_task_parent_idx",
		"one_active_version_per_task",
		"task_runs_status_heartbeat_idx",
		"task_events_run_seq_key",
		"task_events_task_id_idx",
		"cost_events_run_kind_seq_key",
		"cost_events_task_occurred_idx",
		"cost_events_version_idx",
		"task_costs_task_idx",
	}
	for _, name := range wantIndexes {
		var n int
		if err := conn.QueryRow(ctx,
			`SELECT 1 FROM pg_indexes WHERE indexname = $1`, name,
		).Scan(&n); err != nil {
			t.Errorf("index %q missing: %v", name, err)
		}
	}

	// The legacy index MUST be gone after 0004.
	var legacy int
	err := conn.QueryRow(ctx,
		`SELECT 1 FROM pg_indexes WHERE indexname = 'cost_events_run_seq_key'`,
	).Scan(&legacy)
	if err == nil {
		t.Errorf("legacy index cost_events_run_seq_key still present after 0004_cost_events_kind_unique")
	} else if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("unexpected error checking legacy index: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 5.2 + 5.7 up→down→up round-trips
// ---------------------------------------------------------------------------

func TestMigrationsRoundTrip(t *testing.T) {
	t.Parallel()
	dsn := newPostgresContainer(t)
	migrateUp(t, dsn)
	migrateAllDown(t, dsn)

	// After full down, none of our tables exist (schema_migrations may still
	// linger as that's golang-migrate's bookkeeping).
	conn := connect(t, dsn)
	ctx := context.Background()
	for _, tbl := range []string{
		"tasks", "task_versions", "task_runs",
		"task_events", "task_checkpoints", "artifacts",
		"pricing", "cost_events", "task_costs",
		"outbox",
	} {
		var n int
		err := conn.QueryRow(ctx,
			`SELECT 1 FROM pg_class WHERE relname = $1`, tbl,
		).Scan(&n)
		if err == nil {
			t.Errorf("table %q still present after full down", tbl)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("unexpected error checking %q: %v", tbl, err)
		}
	}

	// Re-apply and re-verify a sentinel table.
	migrateUp(t, dsn)
	var ok int
	if err := conn.QueryRow(ctx,
		`SELECT 1 FROM pg_class WHERE relname = 'task_versions'`,
	).Scan(&ok); err != nil {
		t.Fatalf("task_versions missing after re-up: %v", err)
	}

	// schema_migrations.dirty must be false.
	var dirty bool
	if err := conn.QueryRow(ctx,
		`SELECT dirty FROM schema_migrations`,
	).Scan(&dirty); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if dirty {
		t.Errorf("schema_migrations.dirty = true after clean round-trip")
	}
}

// ---------------------------------------------------------------------------
// 5.3 mutex regression — the load-bearing test
// ---------------------------------------------------------------------------

func TestMutexBlocksConcurrentActiveVersions(t *testing.T) {
	t.Parallel()
	dsn := newPostgresContainer(t)
	migrateUp(t, dsn)
	conn := connect(t, dsn)
	ctx := context.Background()

	taskID := mustUUID(t)
	tenantID := mustUUID(t)
	userID := mustUUID(t)

	// Seed task + an INACTIVE version_no=1 (status='succeeded'). This is the
	// critical fixture: with no active version pre-existing AND distinct
	// version_no values in the racing inserts, neither (task_id, version_no)
	// UNIQUE nor any other constraint can fire — only the partial unique
	// index `one_active_version_per_task` can. If the test passes via the
	// wrong constraint name, the assert at the bottom catches it.
	_, err := conn.Exec(ctx,
		`INSERT INTO tasks (id, tenant_id, user_id, title, task_type, status)
		 VALUES ($1, $2, $3, 'fixture', 'code-gen', 'succeeded')`,
		taskID, tenantID, userID,
	)
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}
	_, err = conn.Exec(ctx,
		`INSERT INTO task_versions (id, task_id, version_no, prompt, status)
		 VALUES ($1, $2, 1, 'seed', 'succeeded')`,
		mustUUID(t), taskID,
	)
	if err != nil {
		t.Fatalf("seed inactive version: %v", err)
	}

	// Two goroutines race to INSERT version_no=2 and version_no=3 — both
	// active. Exactly one succeeds; the other surfaces 23505 /
	// one_active_version_per_task.
	type result struct {
		err     error
		runner  string
		success bool
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	insertActive := func(name string, versionNo int) {
		// Each goroutine gets its own connection so the inserts truly race.
		c, cErr := pgx.Connect(ctx, dsn)
		if cErr != nil {
			results <- result{err: cErr, runner: name}
			return
		}
		defer func() { _ = c.Close(ctx) }()
		<-start
		_, e := c.Exec(ctx,
			`INSERT INTO task_versions (id, task_id, version_no, prompt, status)
			 VALUES ($1, $2, $3, 'race', 'pending')`,
			mustUUID(t), taskID, versionNo,
		)
		results <- result{err: e, runner: name, success: e == nil}
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); insertActive("A", 2) }()
	go func() { defer wg.Done(); insertActive("B", 3) }()
	close(start)
	wg.Wait()
	close(results)

	var successes, failures int
	for r := range results {
		if r.success {
			successes++
			continue
		}
		failures++
		code, constraint := pgErrorCode(t, r.err)
		if code != "23505" {
			t.Errorf("goroutine %s: SQLSTATE %s, want 23505", r.runner, code)
		}
		if constraint != "one_active_version_per_task" {
			t.Errorf(
				"goroutine %s: constraint %q, want one_active_version_per_task "+
					"(if you see task_versions_task_version_no_key, the fixture "+
					"isn't exercising the partial unique index)",
				r.runner, constraint,
			)
		}
	}
	if successes != 1 || failures != 1 {
		t.Errorf("want exactly 1 success + 1 failure, got successes=%d failures=%d",
			successes, failures)
	}
}

// ---------------------------------------------------------------------------
// 5.4 active-set transition release
// ---------------------------------------------------------------------------

func TestActiveSetReleaseOnTerminalTransition(t *testing.T) {
	t.Parallel()
	dsn := newPostgresContainer(t)
	migrateUp(t, dsn)
	conn := connect(t, dsn)
	ctx := context.Background()

	taskID := mustUUID(t)
	_, err := conn.Exec(ctx,
		`INSERT INTO tasks (id, tenant_id, user_id, title, task_type, status)
		 VALUES ($1, $2, $3, 'fixture', 'code-gen', 'running')`,
		taskID, mustUUID(t), mustUUID(t),
	)
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}

	// Insert an active version → succeeds.
	v1 := mustUUID(t)
	if _, err := conn.Exec(ctx,
		`INSERT INTO task_versions (id, task_id, version_no, prompt, status)
		 VALUES ($1, $2, 1, 'v1', 'running')`,
		v1, taskID,
	); err != nil {
		t.Fatalf("insert v1: %v", err)
	}

	// Transition to terminal → is_active becomes false automatically.
	if _, err := conn.Exec(ctx,
		`UPDATE task_versions SET status='succeeded' WHERE id=$1`, v1,
	); err != nil {
		t.Fatalf("terminate v1: %v", err)
	}

	// Insert another active version for the same task — must succeed.
	if _, err := conn.Exec(ctx,
		`INSERT INTO task_versions (id, task_id, version_no, prompt, status)
		 VALUES ($1, $2, 2, 'v2', 'pending')`,
		mustUUID(t), taskID,
	); err != nil {
		t.Errorf("v2 insert blocked after v1 terminated: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 5.5 per-run idempotency uniques
// ---------------------------------------------------------------------------

func TestPerRunIdempotencyUniques(t *testing.T) {
	t.Parallel()
	dsn := newPostgresContainer(t)
	migrateUp(t, dsn)
	conn := connect(t, dsn)
	ctx := context.Background()

	// Seed task / version / run scaffolding.
	taskID, verID, runID := mustUUID(t), mustUUID(t), mustUUID(t)
	tenantID, userID := mustUUID(t), mustUUID(t)
	if _, err := conn.Exec(ctx,
		`INSERT INTO tasks (id, tenant_id, user_id, title, task_type, status)
		 VALUES ($1, $2, $3, 'f', 'code-gen', 'running')`,
		taskID, tenantID, userID,
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if _, err := conn.Exec(ctx,
		`INSERT INTO task_versions (id, task_id, version_no, prompt, status)
		 VALUES ($1, $2, 1, 'p', 'running')`, verID, taskID,
	); err != nil {
		t.Fatalf("seed version: %v", err)
	}
	if _, err := conn.Exec(ctx,
		`INSERT INTO task_runs (id, version_id, attempt_no, status, idempotency_key)
		 VALUES ($1, $2, 1, 'running', $3)`,
		runID, verID, "ik-"+runID.String(),
	); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	t.Run("task_events (run_id, seq)", func(t *testing.T) {
		if _, err := conn.Exec(ctx,
			`INSERT INTO task_events (task_id, version_id, run_id, seq, kind, payload)
			 VALUES ($1, $2, $3, 7, 'status', '{}'::jsonb)`,
			taskID, verID, runID,
		); err != nil {
			t.Fatalf("first insert: %v", err)
		}
		_, err := conn.Exec(ctx,
			`INSERT INTO task_events (task_id, version_id, run_id, seq, kind, payload)
			 VALUES ($1, $2, $3, 7, 'status', '{}'::jsonb)`,
			taskID, verID, runID,
		)
		code, constraint := pgErrorCode(t, err)
		if code != "23505" || constraint != "task_events_run_seq_key" {
			t.Errorf("got SQLSTATE %s / constraint %q, want 23505 / task_events_run_seq_key",
				code, constraint)
		}
	})

	t.Run("cost_events (run_id, kind, seq) — same kind collides", func(t *testing.T) {
		if _, err := conn.Exec(ctx,
			`INSERT INTO cost_events
			 (task_id, version_id, run_id, seq, kind, resource_name, amount_usd, occurred_at)
			 VALUES ($1, $2, $3, 3, 'llm', 'claude-opus-4-7', 0.1234, now())`,
			taskID, verID, runID,
		); err != nil {
			t.Fatalf("first insert: %v", err)
		}
		_, err := conn.Exec(ctx,
			`INSERT INTO cost_events
			 (task_id, version_id, run_id, seq, kind, resource_name, amount_usd, occurred_at)
			 VALUES ($1, $2, $3, 3, 'llm', 'claude-opus-4-7', 0.1234, now())`,
			taskID, verID, runID,
		)
		code, constraint := pgErrorCode(t, err)
		if code != "23505" || constraint != "cost_events_run_kind_seq_key" {
			t.Errorf("got SQLSTATE %s / constraint %q, want 23505 / cost_events_run_kind_seq_key",
				code, constraint)
		}
	})

	t.Run("cost_events cross-kind seq=1 coexist (S1 regression)", func(t *testing.T) {
		// The Worker allocates seq per-(run_id, kind). With the (run_id, kind, seq)
		// uniqueness from 0004, three events with seq=1 across kinds MUST all
		// persist; the legacy (run_id, seq) index would have collided them.
		newRun := mustUUID(t)
		if _, err := conn.Exec(ctx,
			`INSERT INTO task_runs (id, version_id, attempt_no, status, idempotency_key)
			 VALUES ($1, $2, 2, 'running', $3)`,
			newRun, verID, "ik-cross-kind-"+newRun.String(),
		); err != nil {
			t.Fatalf("seed run: %v", err)
		}
		for _, k := range []string{"llm", "tool", "compute"} {
			if _, err := conn.Exec(ctx,
				`INSERT INTO cost_events
				 (task_id, version_id, run_id, seq, kind, resource_name, amount_usd, occurred_at)
				 VALUES ($1, $2, $3, 1, $4, 'r', 0.0, now())`,
				taskID, verID, newRun, k,
			); err != nil {
				t.Errorf("seq=1 kind=%s insert failed: %v", k, err)
			}
		}
		// Verify three rows landed.
		var n int
		if err := conn.QueryRow(ctx,
			`SELECT count(*) FROM cost_events WHERE run_id = $1 AND seq = 1`,
			newRun,
		).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 3 {
			t.Errorf("want 3 cost_events rows with seq=1 across kinds, got %d", n)
		}
	})

	t.Run("task_checkpoints (run_id, step_seq)", func(t *testing.T) {
		if _, err := conn.Exec(ctx,
			`INSERT INTO task_checkpoints (id, run_id, step_seq, step_name, state)
			 VALUES ($1, $2, 1, 'plan', '{}'::jsonb)`,
			mustUUID(t), runID,
		); err != nil {
			t.Fatalf("first insert: %v", err)
		}
		_, err := conn.Exec(ctx,
			`INSERT INTO task_checkpoints (id, run_id, step_seq, step_name, state)
			 VALUES ($1, $2, 1, 'plan', '{}'::jsonb)`,
			mustUUID(t), runID,
		)
		code, constraint := pgErrorCode(t, err)
		if code != "23505" || constraint != "task_checkpoints_run_step_key" {
			t.Errorf("got SQLSTATE %s / constraint %q, want 23505 / task_checkpoints_run_step_key",
				code, constraint)
		}
	})
}

// ---------------------------------------------------------------------------
// 5.6 pricing invariants
// ---------------------------------------------------------------------------

func TestPricingInvariants(t *testing.T) {
	t.Parallel()
	dsn := newPostgresContainer(t)
	migrateUp(t, dsn)
	conn := connect(t, dsn)
	ctx := context.Background()

	t.Run("duplicate effective window rejected", func(t *testing.T) {
		eff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		if _, err := conn.Exec(ctx,
			`INSERT INTO pricing
			 (id, resource_kind, resource_name, unit, unit_price_usd, effective_at)
			 VALUES ($1, 'llm', 'claude-opus-4-7', 'per_1k_input_tokens', 0.000015, $2)`,
			mustUUID(t), eff,
		); err != nil {
			t.Fatalf("first insert: %v", err)
		}
		_, err := conn.Exec(ctx,
			`INSERT INTO pricing
			 (id, resource_kind, resource_name, unit, unit_price_usd, effective_at)
			 VALUES ($1, 'llm', 'claude-opus-4-7', 'per_1k_input_tokens', 0.000020, $2)`,
			mustUUID(t), eff,
		)
		code, constraint := pgErrorCode(t, err)
		if code != "23505" || constraint != "pricing_unique_effective" {
			t.Errorf("got SQLSTATE %s / constraint %q, want 23505 / pricing_unique_effective",
				code, constraint)
		}
	})

	t.Run("expires_at <= effective_at rejected", func(t *testing.T) {
		eff := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
		_, err := conn.Exec(ctx,
			`INSERT INTO pricing
			 (id, resource_kind, resource_name, unit, unit_price_usd, effective_at, expires_at)
			 VALUES ($1, 'tool', 'web_search', 'per_call', 0.005, $2, $2)`,
			mustUUID(t), eff,
		)
		code, constraint := pgErrorCode(t, err)
		if code != "23514" || constraint != "pricing_window_check" {
			t.Errorf("got SQLSTATE %s / constraint %q, want 23514 / pricing_window_check",
				code, constraint)
		}
	})
}

// ---------------------------------------------------------------------------
// 5.7 seed pricing (0005) — load + tolerant down
// ---------------------------------------------------------------------------

func TestSeedPricingLoaded(t *testing.T) {
	t.Parallel()
	dsn := newPostgresContainer(t)
	migrateUp(t, dsn)
	conn := connect(t, dsn)
	ctx := context.Background()

	// At least one row exists for (llm, claude-opus-4-7, per_1k_input_tokens)
	// in force at now(). Mirrors the spec scenario "Seeded prices are
	// queryable after migrate up".
	var price float64
	if err := conn.QueryRow(ctx,
		`SELECT unit_price_usd
		 FROM pricing
		 WHERE resource_kind = 'llm'
		   AND resource_name = 'claude-opus-4-7'
		   AND unit          = 'per_1k_input_tokens'
		   AND effective_at <= now()
		   AND (expires_at IS NULL OR expires_at > now())
		 ORDER BY effective_at DESC
		 LIMIT 1`,
	).Scan(&price); err != nil {
		t.Fatalf("seed lookup: %v", err)
	}
	if price <= 0 {
		t.Errorf("opus input price = %v, want > 0", price)
	}

	// All expected (kind, resource_name, unit) triples present.
	wantTriples := []struct{ kind, name, unit string }{
		{"llm", "claude-opus-4-7", "per_1k_input_tokens"},
		{"llm", "claude-opus-4-7", "per_1k_output_tokens"},
		{"llm", "claude-sonnet-4-6", "per_1k_input_tokens"},
		{"llm", "claude-sonnet-4-6", "per_1k_output_tokens"},
		{"llm", "claude-haiku-4-5", "per_1k_input_tokens"},
		{"llm", "claude-haiku-4-5", "per_1k_output_tokens"},
		{"tool", "oss_fs", "per_call"},
		{"compute", "worker", "per_second"},
	}
	for _, w := range wantTriples {
		var n int
		if err := conn.QueryRow(ctx,
			`SELECT 1 FROM pricing
			 WHERE resource_kind = $1 AND resource_name = $2 AND unit = $3`,
			w.kind, w.name, w.unit,
		).Scan(&n); err != nil {
			t.Errorf("seed (%s, %s, %s) missing: %v", w.kind, w.name, w.unit, err)
		}
	}
}

func TestSeedPricingDownPreservesReferencedRows(t *testing.T) {
	t.Parallel()
	dsn := newPostgresContainer(t)
	migrateUp(t, dsn)
	conn := connect(t, dsn)
	ctx := context.Background()

	// Identify a seeded pricing row's id.
	var seedID uuid.UUID
	if err := conn.QueryRow(ctx,
		`SELECT id FROM pricing
		 WHERE resource_kind = 'llm' AND resource_name = 'claude-opus-4-7'
		   AND unit = 'per_1k_input_tokens'`,
	).Scan(&seedID); err != nil {
		t.Fatalf("seed id lookup: %v", err)
	}

	// Insert a cost_events row referencing the seed (need a task/version/run
	// for FK satisfaction — cost_events.task_id/version_id/run_id have no FK
	// but task_costs would; we only INSERT into cost_events here).
	if _, err := conn.Exec(ctx,
		`INSERT INTO cost_events
		 (task_id, version_id, run_id, seq, kind, resource_name, amount_usd,
		  pricing_id, occurred_at)
		 VALUES ($1, $2, $3, 1, 'llm', 'claude-opus-4-7', 0.50, $4, now())`,
		mustUUID(t), mustUUID(t), mustUUID(t), seedID,
	); err != nil {
		t.Fatalf("insert referencing cost_events: %v", err)
	}

	// Roll back 0005_seed_pricing one step. The tolerant DELETE must skip the
	// referenced row and complete without 23503.
	m, err := persistence.NewMigrator(migrationsDir(t), dsn)
	if err != nil {
		t.Fatalf("new migrator: %v", err)
	}
	defer func() { _ = m.Close() }()
	if err := m.Down(); err != nil {
		t.Fatalf("0005 down: %v", err)
	}

	// The referenced row MUST still be present.
	var stillThere int
	if err := conn.QueryRow(ctx,
		`SELECT 1 FROM pricing WHERE id = $1`, seedID,
	).Scan(&stillThere); err != nil {
		t.Errorf("referenced seed row was deleted by 0005_down: %v", err)
	}

	// schema_migrations.dirty must be false.
	var dirty bool
	if err := conn.QueryRow(ctx,
		`SELECT dirty FROM schema_migrations`,
	).Scan(&dirty); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if dirty {
		t.Errorf("schema_migrations.dirty = true after partial 0005 down")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func mustUUID(t *testing.T) uuid.UUID {
	t.Helper()
	u, err := uuid.NewRandom()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return u
}
