package cost

import (
	"math/big"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
)

// Unit names for the pricing.unit column. Mirrors docs/ARCHITECTURE.md §11
// and the task-cost-data-model spec; centralised here so the settler doesn't
// scatter string literals.
const (
	UnitPer1kInputTokens  = "per_1k_input_tokens"  //nolint:gosec // G101: column-value enum, not a credential
	UnitPer1kOutputTokens = "per_1k_output_tokens" //nolint:gosec // G101: column-value enum, not a credential
	UnitPer1kCachedTokens = "per_1k_cached_tokens" //nolint:gosec // G101: column-value enum, not a credential
	UnitPerCall           = "per_call"
	UnitPerSecond         = "per_second"
)

// ComputeAmount runs the per-kind settlement formula from
// task-cost-ingest §"Cost Event Settlement Math".
//
// `rows` is the full list returned by `ListEffectivePricings`, already
// ordered by `effective_at DESC` (so the FIRST row per unit is the
// winner). The function picks at most one row per unit and returns:
//
//   * `amount`  - the total amount in USD as exact rational arithmetic;
//   * `dominantID` - the pricing_id to write into cost_events per
//     task-cost-data-model §"Pricing Reference Convention for Multi-Unit
//     Cost Events"; nil when no pricing row matched any applicable unit
//     for the event's kind.
//
// All NULL quantity fields contribute exactly 0 to their term (the term is
// silently dropped), matching the spec's "NULL quantity treated as zero"
// scenario.
func ComputeAmount(ev *CostEventInput, rows []sqlc.Pricing) (amount *big.Rat, dominantID *uuid.UUID) {
	amount = new(big.Rat) // zero
	dominant := pickDominant(ev.Kind, rows)
	if dominant != nil {
		id := uuid.UUID(dominant.ID.Bytes)
		dominantID = &id
	}

	switch ev.Kind {
	case "llm":
		addTokensTerm(amount, ev.InputTokens, firstByUnit(rows, UnitPer1kInputTokens))
		addTokensTerm(amount, ev.OutputTokens, firstByUnit(rows, UnitPer1kOutputTokens))
		addTokensTerm(amount, ev.CachedTokens, firstByUnit(rows, UnitPer1kCachedTokens))
	case "tool":
		addCallsTerm(amount, ev.Calls, firstByUnit(rows, UnitPerCall))
		addDurationTerm(amount, ev.DurationMs, firstByUnit(rows, UnitPerSecond))
	case "compute":
		addDurationTerm(amount, ev.DurationMs, firstByUnit(rows, UnitPerSecond))
	}
	return amount, dominantID
}

// pickDominant implements the per-kind dominant-id selection rule from
// task-cost-data-model §"Pricing Reference Convention for Multi-Unit Cost
// Events". Returns nil when no relevant pricing row matched.
func pickDominant(kind string, rows []sqlc.Pricing) *sqlc.Pricing {
	switch kind {
	case "llm":
		if r := firstByUnit(rows, UnitPer1kInputTokens); r != nil {
			return r
		}
		if r := firstByUnit(rows, UnitPer1kOutputTokens); r != nil {
			return r
		}
		return firstByUnit(rows, UnitPer1kCachedTokens)
	case "tool":
		if r := firstByUnit(rows, UnitPerCall); r != nil {
			return r
		}
		return firstByUnit(rows, UnitPerSecond)
	case "compute":
		return firstByUnit(rows, UnitPerSecond)
	}
	return nil
}

// firstByUnit returns the first row whose `unit` matches, or nil. The caller
// supplies rows already sorted by effective_at DESC, so "first match" is
// equivalent to "latest effective_at" — the spec's tie-break.
func firstByUnit(rows []sqlc.Pricing, unit string) *sqlc.Pricing {
	for i := range rows {
		if rows[i].Unit == unit {
			return &rows[i]
		}
	}
	return nil
}

// addTokensTerm adds `(tokens / 1000) × unit_price_usd` to `total`. Treats
// NULL tokens and a missing pricing row as 0 (no-op).
func addTokensTerm(total *big.Rat, tokens *int64, row *sqlc.Pricing) {
	if tokens == nil || *tokens == 0 || row == nil {
		return
	}
	price := numericToBigRat(row.UnitPriceUsd)
	if price == nil {
		return
	}
	term := new(big.Rat).SetInt64(*tokens)
	term.Quo(term, big.NewRat(1000, 1))
	term.Mul(term, price)
	total.Add(total, term)
}

// addCallsTerm adds `(calls ?? 1) × unit_price_usd` to `total` when a
// per_call price exists. Per the spec: "if a pricing row exists for ...
// per_call → += (calls ?? 1) × unit_price_usd"; a missing pricing row is a
// no-op (the term is silently dropped).
func addCallsTerm(total *big.Rat, calls *int32, row *sqlc.Pricing) {
	if row == nil {
		return
	}
	c := int64(1)
	if calls != nil {
		c = int64(*calls)
	}
	price := numericToBigRat(row.UnitPriceUsd)
	if price == nil {
		return
	}
	term := new(big.Rat).SetInt64(c)
	term.Mul(term, price)
	total.Add(total, term)
}

// addDurationTerm adds `(duration_ms / 1000) × unit_price_usd` to `total`.
// Uses exact rational arithmetic so sub-second durations contribute their
// fractional value (e.g. 800ms × $0.01/s = $0.008 exactly).
func addDurationTerm(total *big.Rat, durationMs *int64, row *sqlc.Pricing) {
	if durationMs == nil || *durationMs == 0 || row == nil {
		return
	}
	price := numericToBigRat(row.UnitPriceUsd)
	if price == nil {
		return
	}
	term := new(big.Rat).SetInt64(*durationMs)
	term.Quo(term, big.NewRat(1000, 1))
	term.Mul(term, price)
	total.Add(total, term)
}

// numericToBigRat converts a pgtype.Numeric to a *big.Rat preserving the
// scale. Returns nil for invalid / NaN / infinite values (defensive — those
// values should not appear in a `pricing` row, but if they do we don't want
// to silently treat them as zero in arithmetic; the caller's no-op branch
// kicks in instead).
func numericToBigRat(n pgtype.Numeric) *big.Rat {
	if !n.Valid || n.NaN || n.Int == nil || n.InfinityModifier != pgtype.Finite {
		return nil
	}
	num := new(big.Int).Set(n.Int)
	// value = num × 10^exp
	if n.Exp >= 0 {
		ten := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n.Exp)), nil)
		num.Mul(num, ten)
		return new(big.Rat).SetInt(num)
	}
	den := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-n.Exp)), nil)
	return new(big.Rat).SetFrac(num, den)
}
