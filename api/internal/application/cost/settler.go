// Package cost wires the Cost Service consumer's settle pipeline:
//
//	decode → ownership check → pricing lookup → amount math → tx (insert + UPSERT) → ack/dlq.
//
// The package's only exported type is Settler, which the messaging layer
// holds as an interface. The settler owns no per-delivery state and is safe
// to share across concurrent consumer workers.
package cost

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	costdomain "github.com/whoisnian/agent-example/api/internal/domain/cost"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
)

// Settler is the application-layer orchestrator for the Cost Service.
// One instance is shared by every consumer goroutine; the per-delivery
// `ctx` carries the deadline and trace.
type Settler struct {
	Pool    *pgxpool.Pool
	Queries *sqlc.Queries
}

// NewSettler builds a Settler. Both Pool and Queries must come from the same
// underlying connection set so `Queries.WithTx(tx)` is consistent.
func NewSettler(pool *pgxpool.Pool, q *sqlc.Queries) *Settler {
	return &Settler{Pool: pool, Queries: q}
}

// Settle prices one decoded cost event and persists it atomically. The
// returned SettleResult drives the consumer's ack/nack + metric labels;
// errors are only returned for actual DB / transient problems — every
// well-defined business outcome lives in SettleResult.Kind.
//
//nolint:gocritic // hugeParam: value semantics intentional for a read-only input command (mirrors task.IngestEvent).
func (s *Settler) Settle(ctx context.Context, in costdomain.CostEventInput) (costdomain.SettleResult, error) {
	// 1. ownership check — the version_id MUST map to the supplied task_id
	// (task-cost-data-model §"Task Costs `task_id` is Immutable Per `version_id`").
	ownerTaskID, err := s.Queries.GetVersionOwnerTaskID(ctx, toPgUUID(in.VersionID))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return costdomain.SettleResult{Kind: costdomain.SettleErrorMismatch}, nil
	case err != nil:
		return costdomain.SettleResult{}, fmt.Errorf("lookup version owner: %w", err)
	}
	if !uuidEq(ownerTaskID, toPgUUID(in.TaskID)) {
		return costdomain.SettleResult{Kind: costdomain.SettleErrorMismatch}, nil
	}

	// 2. pricing lookup — one query returns all units for (kind, name) in force.
	rows, err := s.Queries.ListEffectivePricings(ctx, sqlc.ListEffectivePricingsParams{
		ResourceKind: in.Kind,
		ResourceName: in.ResourceName,
		EffectiveAt:  pgtype.Timestamptz{Time: in.OccurredAt, Valid: true},
	})
	if err != nil {
		return costdomain.SettleResult{}, fmt.Errorf("list effective pricings: %w", err)
	}

	// 3. amount math + dominant pricing_id (spec §"Cost Event Settlement Math").
	amount, dominantID := costdomain.ComputeAmount(&in, rows)
	missingPricing := dominantID == nil

	// 4. settle in a single tx — cost_events INSERT (idempotent) + task_costs UPSERT.
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return costdomain.SettleResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.Queries.WithTx(tx)

	var pricingIDParam pgtype.UUID
	if dominantID != nil {
		pricingIDParam = pgtype.UUID{Bytes: *dominantID, Valid: true}
	}

	_, err = q.InsertCostEvent(ctx, sqlc.InsertCostEventParams{
		TaskID:       toPgUUID(in.TaskID),
		VersionID:    toPgUUID(in.VersionID),
		RunID:        toPgUUID(in.RunID),
		Seq:          in.Seq,
		Kind:         in.Kind,
		ResourceName: in.ResourceName,
		InputTokens:  in.InputTokens,
		OutputTokens: in.OutputTokens,
		CachedTokens: in.CachedTokens,
		Calls:        in.Calls,
		DurationMs:   in.DurationMs,
		AmountUsd:    ratToNumeric(amount),
		PricingID:    pricingIDParam,
		OccurredAt:   pgtype.Timestamptz{Time: in.OccurredAt, Valid: true},
	})

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Idempotent path: a row with this (run_id, kind, seq) already exists
		// (ON CONFLICT DO NOTHING returned no row). Skip the aggregate UPSERT
		// to avoid double-counting. Commit anyway so the empty tx releases
		// cleanly.
		if cerr := tx.Commit(ctx); cerr != nil {
			return costdomain.SettleResult{}, fmt.Errorf("commit (duplicate): %w", cerr)
		}
		return costdomain.SettleResult{Kind: costdomain.SettleDuplicate}, nil
	case err != nil:
		return costdomain.SettleResult{}, fmt.Errorf("insert cost_event: %w", err)
	}

	inc := costdomain.ResolveIncrements(&in)
	if err := q.UpsertVersionCost(ctx, sqlc.UpsertVersionCostParams{
		VersionID:      toPgUUID(in.VersionID),
		TaskID:         toPgUUID(in.TaskID),
		InputTokens:    inc.InputTokens,
		OutputTokens:   inc.OutputTokens,
		CachedTokens:   inc.CachedTokens,
		ToolCalls:      inc.ToolCalls,
		WallTimeMs:     inc.WallTimeMs,
		ComputeSeconds: inc.ComputeSeconds,
		AmountUsd:      ratToNumeric(amount),
	}); err != nil {
		return costdomain.SettleResult{}, fmt.Errorf("upsert task_costs: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return costdomain.SettleResult{}, fmt.Errorf("commit: %w", err)
	}

	if missingPricing {
		return costdomain.SettleResult{Kind: costdomain.SettleMissingPricing, AmountUSD: amount}, nil
	}
	return costdomain.SettleResult{Kind: costdomain.SettleOK, AmountUSD: amount}, nil
}

// ListEffectivePricingCoverage returns the distinct (resource_kind,
// resource_name) pairs currently covered by at least one active pricing row.
// Used at startup for the `cost_pricing_coverage` INFO log — the operational
// guardrail against resource_name typos producing silent zero-amount events.
func (s *Settler) ListEffectivePricingCoverage(ctx context.Context, at time.Time) ([]PricingCoverageEntry, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT DISTINCT resource_kind, resource_name
		 FROM pricing
		 WHERE effective_at <= $1
		   AND (expires_at IS NULL OR expires_at > $1)
		 ORDER BY resource_kind, resource_name`,
		at,
	)
	if err != nil {
		return nil, fmt.Errorf("query coverage: %w", err)
	}
	defer rows.Close()
	var out []PricingCoverageEntry
	for rows.Next() {
		var e PricingCoverageEntry
		if err := rows.Scan(&e.Kind, &e.Name); err != nil {
			return nil, fmt.Errorf("scan coverage: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// PricingCoverageEntry is one (resource_kind, resource_name) pair in the
// active pricing set; rendered into the startup INFO log.
type PricingCoverageEntry struct {
	Kind string
	Name string
}

// --- helpers ---------------------------------------------------------------

func toPgUUID(id [16]byte) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func uuidEq(a, b pgtype.UUID) bool {
	if a.Valid != b.Valid {
		return false
	}
	return a.Bytes == b.Bytes
}

// ratToNumeric binds a *big.Rat to a pgtype.Numeric at the cost-domain scale
// (amountScale=8 matches the NUMERIC(18,8) column). Uses big.Rat.FloatString
// → Numeric.Scan so we never pass through a float64.
func ratToNumeric(r *big.Rat) pgtype.Numeric {
	var n pgtype.Numeric
	// FloatString(8) renders e.g. "0.62000000" for 31/50; rounded half-to-even
	// per math/big docs. pgtype.Numeric.Scan parses the decimal text exactly.
	if err := n.Scan(r.FloatString(amountScale)); err != nil {
		// In practice .Scan only fails on un-parsable input; FloatString always
		// returns a parseable decimal. Fall back to invalid Numeric (DB will
		// reject, surfacing the bug loudly rather than silently zeroing).
		return pgtype.Numeric{}
	}
	return n
}

// amountScale matches the read-side `task.amountScale` (8) and the DB
// NUMERIC(18, 8) column. Kept private here so the binder is self-contained.
const amountScale = 8
