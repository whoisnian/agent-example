//go:build integration

// Integration tests for the Cost Service settler — drives
// application/cost.Settler directly against a real postgres:18.4 container
// (no broker; the broker round-trip is covered by topology + handler tests).
//
// Run with: make test-integration
package persistence_test

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	appcost "github.com/whoisnian/agent-example/api/internal/application/cost"
	costdomain "github.com/whoisnian/agent-example/api/internal/domain/cost"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
)

// settlerFixture spins up a fresh DB (with all migrations + seeds applied)
// and returns a wired settler plus helpers to seed task/version rows.
type settlerFixture struct {
	pool     *pgxpool.Pool
	queries  *sqlc.Queries
	settler  *appcost.Settler
	taskID   uuid.UUID
	versionID uuid.UUID
	runID    uuid.UUID
}

func newSettlerFixture(t *testing.T) *settlerFixture {
	t.Helper()
	dsn := newPostgresContainer(t)
	migrateUp(t, dsn)
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

	q := sqlc.New(pool)
	settler := appcost.NewSettler(pool, q)
	fx := &settlerFixture{
		pool:    pool,
		queries: q,
		settler: settler,
	}
	fx.taskID = mustUUID(t)
	fx.versionID = mustUUID(t)
	fx.runID = mustUUID(t)

	ctx := context.Background()
	tenantID, userID := mustUUID(t), mustUUID(t)
	if _, err := pool.Exec(ctx,
		`INSERT INTO tasks (id, tenant_id, user_id, title, task_type, status)
		 VALUES ($1, $2, $3, 'f', 'code-gen', 'running')`,
		fx.taskID, tenantID, userID,
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO task_versions (id, task_id, version_no, prompt, status)
		 VALUES ($1, $2, 1, 'p', 'running')`, fx.versionID, fx.taskID,
	); err != nil {
		t.Fatalf("seed version: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO task_runs (id, version_id, attempt_no, status, idempotency_key)
		 VALUES ($1, $2, 1, 'running', $3)`,
		fx.runID, fx.versionID, "ik-"+fx.runID.String(),
	); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	return fx
}

// taskCostRow is the subset of task_costs columns the integration assertions
// care about. Bundled so the helper stays under gocritic's results-count cap.
type taskCostRow struct {
	InputTokens    int64
	OutputTokens   int64
	ToolCalls      int32
	WallTimeMs     int64
	ComputeSeconds int64
	AmountUSD      string
}

// readTaskCost loads the fixture version's row from task_costs.
func (fx *settlerFixture) readTaskCost(t *testing.T) taskCostRow {
	t.Helper()
	var r taskCostRow
	var amt pgtype.Numeric
	err := fx.pool.QueryRow(context.Background(),
		`SELECT input_tokens, output_tokens, tool_calls, wall_time_ms, compute_seconds, amount_usd
		 FROM task_costs WHERE version_id = $1`,
		fx.versionID,
	).Scan(&r.InputTokens, &r.OutputTokens, &r.ToolCalls, &r.WallTimeMs, &r.ComputeSeconds, &amt)
	if err != nil {
		t.Fatalf("read task_costs: %v", err)
	}
	r.AmountUSD = numericString(t, amt)
	return r
}

func numericString(t *testing.T, n pgtype.Numeric) string {
	t.Helper()
	v, err := n.Value()
	if err != nil {
		t.Fatalf("numeric value: %v", err)
	}
	if v == nil {
		return "<nil>"
	}
	return v.(string)
}

func i64p(v int64) *int64 { return &v }
func i32p(v int32) *int32 { return &v }

// -----------------------------------------------------------------------------
// happy paths
// -----------------------------------------------------------------------------

func TestCostSettler_HappyLLM_InputAndOutput(t *testing.T) {
	t.Parallel()
	fx := newSettlerFixture(t)
	ctx := context.Background()

	res, err := fx.settler.Settle(ctx, costdomain.CostEventInput{
		TaskID:       fx.taskID,
		VersionID:    fx.versionID,
		RunID:        fx.runID,
		Seq:          1,
		Kind:         "llm",
		ResourceName: "claude-opus-4-7",
		InputTokens:  i64p(2000),
		OutputTokens: i64p(500),
		OccurredAt:   time.Now(),
	})
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if res.Kind != costdomain.SettleOK {
		t.Fatalf("kind = %v, want ok", res.Kind)
	}

	// seed prices: opus input=$0.015/1k, output=$0.075/1k.
	// 2000/1000 × 0.015 + 500/1000 × 0.075 = 0.03 + 0.0375 = 0.0675
	if want, _ := new(big.Rat).SetString("0.0675"); res.AmountUSD.Cmp(want) != 0 {
		t.Errorf("amount = %s, want 0.0675", res.AmountUSD.FloatString(8))
	}

	row := fx.readTaskCost(t)
	if row.InputTokens != 2000 || row.OutputTokens != 500 {
		t.Errorf("tokens = (%d, %d), want (2000, 500)", row.InputTokens, row.OutputTokens)
	}
	if row.AmountUSD != "0.06750000" {
		t.Errorf("amount_usd = %s, want 0.06750000", row.AmountUSD)
	}

	// cost_events row carries the dominant pricing_id (input row).
	var pid pgtype.UUID
	if err := fx.pool.QueryRow(ctx,
		`SELECT pricing_id FROM cost_events WHERE run_id = $1 AND kind = 'llm' AND seq = 1`,
		fx.runID,
	).Scan(&pid); err != nil {
		t.Fatalf("read cost_events: %v", err)
	}
	if !pid.Valid {
		t.Errorf("pricing_id is NULL; expected the dominant (per_1k_input_tokens) seed id")
	}
}

func TestCostSettler_Tool_PerCallOnly(t *testing.T) {
	t.Parallel()
	fx := newSettlerFixture(t)
	ctx := context.Background()

	res, err := fx.settler.Settle(ctx, costdomain.CostEventInput{
		TaskID:       fx.taskID,
		VersionID:    fx.versionID,
		RunID:        fx.runID,
		Seq:          1,
		Kind:         "tool",
		ResourceName: "oss_fs",
		Calls:        i32p(3),
		DurationMs:   i64p(150),
		OccurredAt:   time.Now(),
	})
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if res.Kind != costdomain.SettleOK {
		t.Fatalf("kind = %v", res.Kind)
	}

	// seed: oss_fs per_call=$0.000001 → 3 × 0.000001 = 0.000003
	if want, _ := new(big.Rat).SetString("0.000003"); res.AmountUSD.Cmp(want) != 0 {
		t.Errorf("amount = %s, want 0.000003", res.AmountUSD.FloatString(8))
	}

	row := fx.readTaskCost(t)
	if row.ToolCalls != 3 {
		t.Errorf("tool_calls = %d, want 3", row.ToolCalls)
	}
	if row.WallTimeMs != 150 {
		t.Errorf("wall_time_ms = %d, want 150", row.WallTimeMs)
	}
	if row.ComputeSeconds != 0 {
		t.Errorf("compute_seconds = %d, want 0 (tool event doesn't feed compute)", row.ComputeSeconds)
	}
}

func TestCostSettler_Compute_SubSecondAmountExactSecondsZero(t *testing.T) {
	t.Parallel()
	fx := newSettlerFixture(t)
	ctx := context.Background()

	res, err := fx.settler.Settle(ctx, costdomain.CostEventInput{
		TaskID:       fx.taskID,
		VersionID:    fx.versionID,
		RunID:        fx.runID,
		Seq:          1,
		Kind:         "compute",
		ResourceName: "worker",
		DurationMs:   i64p(800),
		OccurredAt:   time.Now(),
	})
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if res.Kind != costdomain.SettleOK {
		t.Fatalf("kind = %v", res.Kind)
	}

	// seed: worker per_second=$0.0001 → 0.8 × 0.0001 = 0.00008 exactly
	if want, _ := new(big.Rat).SetString("0.00008"); res.AmountUSD.Cmp(want) != 0 {
		t.Errorf("amount = %s, want 0.00008", res.AmountUSD.FloatString(8))
	}

	row := fx.readTaskCost(t)
	if row.ComputeSeconds != 0 {
		t.Errorf("compute_seconds = %d, want 0 (floor(800/1000) = 0)", row.ComputeSeconds)
	}
	if row.AmountUSD != "0.00008000" {
		t.Errorf("amount_usd = %s, want 0.00008000", row.AmountUSD)
	}
}

// -----------------------------------------------------------------------------
// missing pricing / idempotency / mismatch
// -----------------------------------------------------------------------------

func TestCostSettler_MissingPricingPreservesTokensWithZeroAmount(t *testing.T) {
	t.Parallel()
	fx := newSettlerFixture(t)
	ctx := context.Background()

	res, err := fx.settler.Settle(ctx, costdomain.CostEventInput{
		TaskID:       fx.taskID,
		VersionID:    fx.versionID,
		RunID:        fx.runID,
		Seq:          1,
		Kind:         "llm",
		ResourceName: "experimental-model", // no seed row
		InputTokens:  i64p(1000),
		OccurredAt:   time.Now(),
	})
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if res.Kind != costdomain.SettleMissingPricing {
		t.Errorf("kind = %v, want missing_pricing", res.Kind)
	}

	// cost_events row persists with NULL pricing_id and amount=0.
	var pid pgtype.UUID
	var amt pgtype.Numeric
	if err := fx.pool.QueryRow(ctx,
		`SELECT pricing_id, amount_usd FROM cost_events WHERE run_id = $1 AND kind = 'llm' AND seq = 1`,
		fx.runID,
	).Scan(&pid, &amt); err != nil {
		t.Fatalf("read cost_events: %v", err)
	}
	if pid.Valid {
		t.Errorf("pricing_id valid; want NULL")
	}
	if numericString(t, amt) != "0.00000000" {
		t.Errorf("cost_events.amount_usd = %s, want 0", numericString(t, amt))
	}

	// task_costs still gets the tokens.
	row := fx.readTaskCost(t)
	if row.InputTokens != 1000 {
		t.Errorf("task_costs.input_tokens = %d, want 1000 (preserved for backfill)", row.InputTokens)
	}
	if row.AmountUSD != "0.00000000" {
		t.Errorf("task_costs.amount_usd = %s, want 0", row.AmountUSD)
	}
}

func TestCostSettler_DuplicateDeliveryIdempotent(t *testing.T) {
	t.Parallel()
	fx := newSettlerFixture(t)
	ctx := context.Background()

	ev := costdomain.CostEventInput{
		TaskID:       fx.taskID,
		VersionID:    fx.versionID,
		RunID:        fx.runID,
		Seq:          1,
		Kind:         "llm",
		ResourceName: "claude-opus-4-7",
		InputTokens:  i64p(2000),
		OccurredAt:   time.Now(),
	}
	if _, err := fx.settler.Settle(ctx, ev); err != nil {
		t.Fatalf("first settle: %v", err)
	}
	res, err := fx.settler.Settle(ctx, ev)
	if err != nil {
		t.Fatalf("second settle: %v", err)
	}
	if res.Kind != costdomain.SettleDuplicate {
		t.Errorf("kind = %v, want duplicate", res.Kind)
	}

	// task_costs.input_tokens MUST stay at 2000, not 4000.
	row := fx.readTaskCost(t)
	if row.InputTokens != 2000 {
		t.Errorf("input_tokens = %d, want 2000 (no double-count)", row.InputTokens)
	}
}

func TestCostSettler_CrossKindSameSeqBothPersist(t *testing.T) {
	t.Parallel()
	fx := newSettlerFixture(t)
	ctx := context.Background()

	if _, err := fx.settler.Settle(ctx, costdomain.CostEventInput{
		TaskID: fx.taskID, VersionID: fx.versionID, RunID: fx.runID,
		Seq: 1, Kind: "llm", ResourceName: "claude-opus-4-7",
		InputTokens: i64p(1000),
		OccurredAt:  time.Now(),
	}); err != nil {
		t.Fatalf("llm settle: %v", err)
	}
	if _, err := fx.settler.Settle(ctx, costdomain.CostEventInput{
		TaskID: fx.taskID, VersionID: fx.versionID, RunID: fx.runID,
		Seq: 1, Kind: "tool", ResourceName: "oss_fs",
		Calls:      i32p(1),
		OccurredAt: time.Now(),
	}); err != nil {
		t.Fatalf("tool settle: %v", err)
	}

	var n int
	if err := fx.pool.QueryRow(ctx,
		`SELECT count(*) FROM cost_events WHERE run_id = $1 AND seq = 1`,
		fx.runID,
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("count = %d, want 2 (cross-kind seq=1 must coexist)", n)
	}
}

func TestCostSettler_TaskIDMismatchReturnsErrorMismatch(t *testing.T) {
	t.Parallel()
	fx := newSettlerFixture(t)
	ctx := context.Background()

	otherTask := mustUUID(t)
	res, err := fx.settler.Settle(ctx, costdomain.CostEventInput{
		TaskID:       otherTask, // does NOT own fx.versionID
		VersionID:    fx.versionID,
		RunID:        fx.runID,
		Seq:          1,
		Kind:         "llm",
		ResourceName: "claude-opus-4-7",
		InputTokens:  i64p(1000),
		OccurredAt:   time.Now(),
	})
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if res.Kind != costdomain.SettleErrorMismatch {
		t.Errorf("kind = %v, want error_mismatch", res.Kind)
	}
	// No cost_events row persisted.
	var n int
	if err := fx.pool.QueryRow(ctx,
		`SELECT count(*) FROM cost_events WHERE run_id = $1`, fx.runID,
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("cost_events count = %d, want 0 on mismatch", n)
	}
}

func TestCostSettler_UnknownVersionIDReturnsErrorMismatch(t *testing.T) {
	t.Parallel()
	fx := newSettlerFixture(t)
	ctx := context.Background()

	res, err := fx.settler.Settle(ctx, costdomain.CostEventInput{
		TaskID:       fx.taskID,
		VersionID:    mustUUID(t), // does not exist
		RunID:        fx.runID,
		Seq:          1,
		Kind:         "llm",
		ResourceName: "claude-opus-4-7",
		InputTokens:  i64p(1000),
		OccurredAt:   time.Now(),
	})
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if res.Kind != costdomain.SettleErrorMismatch {
		t.Errorf("kind = %v, want error_mismatch", res.Kind)
	}
}

// -----------------------------------------------------------------------------
// pricing window edge: abuts exactly at occurred_at
// -----------------------------------------------------------------------------

func TestCostSettler_AbuttingPricingWindowsPickSuccessor(t *testing.T) {
	t.Parallel()
	fx := newSettlerFixture(t)
	ctx := context.Background()

	// Insert a NEW pricing window for opus input that supersedes the seed at
	// boundary `t0`. The old (seed) row will get expires_at = t0 via UPDATE;
	// per the immutability contract this is normally forbidden, but the test
	// crafts the situation explicitly using a fresh model name so it doesn't
	// conflict with the immutability convention on production rows.
	t0 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	oldID, newID := mustUUID(t), mustUUID(t)
	if _, err := fx.pool.Exec(ctx,
		`INSERT INTO pricing (id, resource_kind, resource_name, unit, unit_price_usd, effective_at, expires_at)
		 VALUES ($1, 'llm', 'edge-model', 'per_1k_input_tokens', 1.0, '2026-01-01'::timestamptz, $2)`,
		oldID, t0,
	); err != nil {
		t.Fatalf("seed old row: %v", err)
	}
	if _, err := fx.pool.Exec(ctx,
		`INSERT INTO pricing (id, resource_kind, resource_name, unit, unit_price_usd, effective_at)
		 VALUES ($1, 'llm', 'edge-model', 'per_1k_input_tokens', 2.0, $2)`,
		newID, t0,
	); err != nil {
		t.Fatalf("seed new row: %v", err)
	}

	res, err := fx.settler.Settle(ctx, costdomain.CostEventInput{
		TaskID:       fx.taskID,
		VersionID:    fx.versionID,
		RunID:        fx.runID,
		Seq:          1,
		Kind:         "llm",
		ResourceName: "edge-model",
		InputTokens:  i64p(1000),
		OccurredAt:   t0,
	})
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if want, _ := new(big.Rat).SetString("2.0"); res.AmountUSD.Cmp(want) != 0 {
		t.Errorf("amount = %s, want 2.0 (successor window, NOT 1.0)", res.AmountUSD.FloatString(8))
	}
}

// -----------------------------------------------------------------------------
// coverage log
// -----------------------------------------------------------------------------

func TestCostSettler_ListEffectivePricingCoverageIncludesSeedRows(t *testing.T) {
	t.Parallel()
	fx := newSettlerFixture(t)
	ctx := context.Background()

	got, err := fx.settler.ListEffectivePricingCoverage(ctx, time.Now())
	if err != nil {
		t.Fatalf("coverage: %v", err)
	}
	have := map[string]bool{}
	for _, e := range got {
		have[e.Kind+":"+e.Name] = true
	}
	for _, want := range []string{
		"llm:claude-opus-4-7", "llm:claude-sonnet-4-6", "llm:claude-haiku-4-5",
		"tool:oss_fs", "compute:worker",
	} {
		if !have[want] {
			t.Errorf("coverage missing %s", want)
		}
	}
}

// -----------------------------------------------------------------------------
// silence "unused" warnings for cross-package types referenced only here
// -----------------------------------------------------------------------------

var (
	_ = prometheus.NewCounter
	_ = (*dto.Metric)(nil)
	_ = pgx.ErrNoRows
)
