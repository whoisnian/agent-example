package cost

import (
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
)

// pricingRow builds a sqlc.Pricing for the test fixtures. priceStr is decimal
// (e.g. "0.015") so test expectations stay readable; we round-trip it through
// pgtype.Numeric via the protocol the database would have used.
func pricingRow(t *testing.T, id uuid.UUID, kind, name, unit, priceStr string, effective time.Time) sqlc.Pricing {
	t.Helper()
	var n pgtype.Numeric
	if err := n.Scan(priceStr); err != nil {
		t.Fatalf("scan numeric %q: %v", priceStr, err)
	}
	return sqlc.Pricing{
		ID:           pgtype.UUID{Bytes: id, Valid: true},
		ResourceKind: kind,
		ResourceName: name,
		Unit:         unit,
		UnitPriceUsd: n,
		EffectiveAt:  pgtype.Timestamptz{Time: effective, Valid: true},
		ExpiresAt:    pgtype.Timestamptz{},
	}
}

func i64(v int64) *int64 { return &v }
func i32(v int32) *int32 { return &v }

func ratEq(t *testing.T, got *big.Rat, wantStr string) {
	t.Helper()
	want, ok := new(big.Rat).SetString(wantStr)
	if !ok {
		t.Fatalf("bad want %q", wantStr)
	}
	if got.Cmp(want) != 0 {
		t.Errorf("amount = %s, want %s", got.FloatString(8), want.FloatString(8))
	}
}

func TestComputeAmount_LLM_InputOnly(t *testing.T) {
	t.Parallel()
	inputID := uuid.New()
	rows := []sqlc.Pricing{
		pricingRow(t, inputID, "llm", "opus", UnitPer1kInputTokens, "0.015", time.Now()),
	}
	ev := &CostEventInput{Kind: "llm", ResourceName: "opus", InputTokens: i64(2000)}

	amt, dom := ComputeAmount(ev, rows)
	ratEq(t, amt, "0.030") // 2000/1000 × 0.015
	if dom == nil || *dom != inputID {
		t.Errorf("dominant = %v, want %v", dom, inputID)
	}
}

func TestComputeAmount_LLM_InputAndOutput(t *testing.T) {
	t.Parallel()
	inputID, outputID := uuid.New(), uuid.New()
	rows := []sqlc.Pricing{
		pricingRow(t, inputID, "llm", "opus", UnitPer1kInputTokens, "3", time.Now()),
		pricingRow(t, outputID, "llm", "opus", UnitPer1kOutputTokens, "15", time.Now()),
	}
	ev := &CostEventInput{Kind: "llm", ResourceName: "opus", InputTokens: i64(2000), OutputTokens: i64(500)}

	amt, dom := ComputeAmount(ev, rows)
	ratEq(t, amt, "13.5") // 2000/1000×3 + 500/1000×15 = 6 + 7.5
	if dom == nil || *dom != inputID {
		t.Errorf("dominant = %v, want input id %v (dominant-unit rule)", dom, inputID)
	}
}

func TestComputeAmount_LLM_OutputOnly_DominantFallsBack(t *testing.T) {
	t.Parallel()
	outputID := uuid.New()
	rows := []sqlc.Pricing{
		pricingRow(t, outputID, "llm", "opus", UnitPer1kOutputTokens, "15", time.Now()),
	}
	ev := &CostEventInput{Kind: "llm", ResourceName: "opus", OutputTokens: i64(500)}

	_, dom := ComputeAmount(ev, rows)
	if dom == nil || *dom != outputID {
		t.Errorf("dominant = %v, want output id (fallback when no input row)", dom)
	}
}

func TestComputeAmount_LLM_NullCachedTreatedAsZero(t *testing.T) {
	t.Parallel()
	inputID, cachedID := uuid.New(), uuid.New()
	rows := []sqlc.Pricing{
		pricingRow(t, inputID, "llm", "opus", UnitPer1kInputTokens, "3", time.Now()),
		pricingRow(t, cachedID, "llm", "opus", UnitPer1kCachedTokens, "0.3", time.Now()),
	}
	// cached_tokens is nil — its term must contribute 0.
	ev := &CostEventInput{Kind: "llm", ResourceName: "opus", InputTokens: i64(1000), CachedTokens: nil}

	amt, _ := ComputeAmount(ev, rows)
	ratEq(t, amt, "3") // only input contributes
}

func TestComputeAmount_Tool_PerCallOnly(t *testing.T) {
	t.Parallel()
	callID := uuid.New()
	rows := []sqlc.Pricing{
		pricingRow(t, callID, "tool", "oss_fs", UnitPerCall, "0.0001", time.Now()),
	}
	ev := &CostEventInput{Kind: "tool", ResourceName: "oss_fs", Calls: i32(3), DurationMs: i64(120)}

	amt, dom := ComputeAmount(ev, rows)
	ratEq(t, amt, "0.0003") // 3 × 0.0001; per_second term contributes 0
	if dom == nil || *dom != callID {
		t.Errorf("dominant = %v, want call id %v", dom, callID)
	}
}

func TestComputeAmount_Tool_PerSecondOnlyDominant(t *testing.T) {
	t.Parallel()
	secID := uuid.New()
	rows := []sqlc.Pricing{
		pricingRow(t, secID, "tool", "x", UnitPerSecond, "0.10", time.Now()),
	}
	ev := &CostEventInput{Kind: "tool", ResourceName: "x", DurationMs: i64(2500)}

	amt, dom := ComputeAmount(ev, rows)
	ratEq(t, amt, "0.25") // 2.5 × 0.10
	if dom == nil || *dom != secID {
		t.Errorf("dominant = %v, want per_second id (fallback)", dom)
	}
}

func TestComputeAmount_Tool_NullCallsDefaultsToOne(t *testing.T) {
	t.Parallel()
	callID := uuid.New()
	rows := []sqlc.Pricing{
		pricingRow(t, callID, "tool", "x", UnitPerCall, "0.05", time.Now()),
	}
	// Calls nil → spec says "calls ?? 1"
	ev := &CostEventInput{Kind: "tool", ResourceName: "x"}

	amt, _ := ComputeAmount(ev, rows)
	ratEq(t, amt, "0.05") // 1 × 0.05
}

func TestComputeAmount_Compute_SubSecondExact(t *testing.T) {
	t.Parallel()
	secID := uuid.New()
	rows := []sqlc.Pricing{
		pricingRow(t, secID, "compute", "worker", UnitPerSecond, "0.01", time.Now()),
	}
	ev := &CostEventInput{Kind: "compute", ResourceName: "worker", DurationMs: i64(800)}

	amt, _ := ComputeAmount(ev, rows)
	ratEq(t, amt, "0.008") // 800/1000 × 0.01 — exact rational, no truncation
}

func TestComputeAmount_NoMatchingPricing(t *testing.T) {
	t.Parallel()
	ev := &CostEventInput{Kind: "llm", ResourceName: "unknown", InputTokens: i64(1000)}

	amt, dom := ComputeAmount(ev, nil)
	ratEq(t, amt, "0")
	if dom != nil {
		t.Errorf("dominant = %v, want nil when no pricing matched", dom)
	}
}

func TestComputeAmount_LatestEffectiveAtWins(t *testing.T) {
	t.Parallel()
	// Two pricing rows for the same unit; the slice mirrors the DESC ordering
	// the SQL guarantees (newer first). The settler MUST pick the newer.
	newID, oldID := uuid.New(), uuid.New()
	rows := []sqlc.Pricing{
		pricingRow(t, newID, "llm", "opus", UnitPer1kInputTokens, "5", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		pricingRow(t, oldID, "llm", "opus", UnitPer1kInputTokens, "3", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
	}
	ev := &CostEventInput{Kind: "llm", ResourceName: "opus", InputTokens: i64(1000)}

	amt, dom := ComputeAmount(ev, rows)
	ratEq(t, amt, "5") // newer price, not 3
	if dom == nil || *dom != newID {
		t.Errorf("dominant = %v, want newer id %v", dom, newID)
	}
}

func TestComputeAmount_UnknownKind(t *testing.T) {
	t.Parallel()
	ev := &CostEventInput{Kind: "bogus", ResourceName: "x", InputTokens: i64(1000)}
	rows := []sqlc.Pricing{
		pricingRow(t, uuid.New(), "llm", "x", UnitPer1kInputTokens, "1", time.Now()),
	}
	amt, dom := ComputeAmount(ev, rows)
	ratEq(t, amt, "0")
	if dom != nil {
		t.Errorf("dominant = %v, want nil for unknown kind", dom)
	}
}
