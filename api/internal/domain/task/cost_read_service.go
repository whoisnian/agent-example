package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
)

// CostReadService is the queries-only read side of the cost endpoints. Like
// ReadService it holds no pool / clock / id-generator — every method is a
// read with ownership enforcement and DTO assembly.
type CostReadService struct {
	Queries *sqlc.Queries
}

// NewCostReadService constructs the cost read service.
func NewCostReadService(q *sqlc.Queries) *CostReadService {
	return &CostReadService{Queries: q}
}

// GetTaskCost returns the task-level aggregate + per-version breakdown for an
// owned task. The owner check is in the SQL (`GetTaskCostWithOwner` predicates
// on tenant+user); pgx.ErrNoRows here means either "no such task" or "exists
// but unowned" — both map to ErrTaskNotFound (identical 404 regardless of
// cause).
func (s *CostReadService) GetTaskCost(ctx context.Context, owner Owner, taskID uuid.UUID) (TaskCostDetail, error) {
	agg, err := s.Queries.GetTaskCostWithOwner(ctx, sqlc.GetTaskCostWithOwnerParams{
		ID:       toPgUUID(taskID),
		TenantID: toPgUUID(owner.TenantID),
		UserID:   toPgUUID(owner.UserID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TaskCostDetail{}, ErrTaskNotFound
		}
		return TaskCostDetail{}, fmt.Errorf("get task cost: %w", err)
	}

	rows, err := s.Queries.ListVersionCostsForTask(ctx, sqlc.ListVersionCostsForTaskParams{
		TaskID:   toPgUUID(taskID),
		TenantID: toPgUUID(owner.TenantID),
		UserID:   toPgUUID(owner.UserID),
	})
	if err != nil {
		return TaskCostDetail{}, fmt.Errorf("list version costs: %w", err)
	}

	by := make([]VersionCostBreakdown, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		by = append(by, versionCostBreakdownFromRow(r))
	}

	return TaskCostDetail{
		TaskID:    uuid.UUID(agg.TaskID.Bytes),
		Total:     costFromTaskCostWithOwnerRow(&agg),
		ByVersion: by,
	}, nil
}

// GetVersionCost returns the single-version detail + owning task_id for an
// owned version. pgx.ErrNoRows → ErrVersionNotFound (unknown OR unowned).
func (s *CostReadService) GetVersionCost(ctx context.Context, owner Owner, versionID uuid.UUID) (VersionCostDetail, error) {
	row, err := s.Queries.GetVersionCostWithOwner(ctx, sqlc.GetVersionCostWithOwnerParams{
		ID:       toPgUUID(versionID),
		TenantID: toPgUUID(owner.TenantID),
		UserID:   toPgUUID(owner.UserID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return VersionCostDetail{}, ErrVersionNotFound
		}
		return VersionCostDetail{}, fmt.Errorf("get version cost: %w", err)
	}
	return versionCostDetailFromRow(&row), nil
}

// GetOwnerCostTotal returns the no-group_by /me/cost rollup. An empty result
// (no tasks, no events, or everything filtered out by the time window) MUST
// produce zeroCost() — never 404, never error.
//
// from / to are optional; pass nil for unbounded.
func (s *CostReadService) GetOwnerCostTotal(ctx context.Context, owner Owner, from, to *time.Time) (OwnerCostTotal, error) {
	row, err := s.Queries.SumOwnerCosts(ctx, sqlc.SumOwnerCostsParams{
		TenantID: toPgUUID(owner.TenantID),
		UserID:   toPgUUID(owner.UserID),
		FromTs:   timestampToPg(from),
		ToTs:     timestampToPg(to),
	})
	if err != nil {
		return OwnerCostTotal{}, fmt.Errorf("sum owner costs: %w", err)
	}
	return OwnerCostTotal{Total: costFromSumOwnerCostsRow(&row)}, nil
}

// GetOwnerCostGrouped returns the grouped /me/cost rollup. groupBy MUST be
// one of GroupByDay / GroupByTaskType / GroupByModel; an unknown value here
// is a programmer error (the HTTP layer validates inputs).
//
// Empty result MUST produce a payload with `Items = []` (initialised
// empty slice, never nil) so the JSON shape stays `{"items": []}` rather
// than `{"items": null}`.
func (s *CostReadService) GetOwnerCostGrouped(ctx context.Context, owner Owner, groupBy string, from, to *time.Time) (OwnerCostGrouped, error) {
	out := OwnerCostGrouped{GroupBy: groupBy, Items: []OwnerCostGroup{}}

	switch groupBy {
	case GroupByDay:
		rows, err := s.Queries.GroupOwnerCostsByDay(ctx, sqlc.GroupOwnerCostsByDayParams{
			TenantID: toPgUUID(owner.TenantID),
			UserID:   toPgUUID(owner.UserID),
			FromTs:   timestampToPg(from),
			ToTs:     timestampToPg(to),
		})
		if err != nil {
			return OwnerCostGrouped{}, fmt.Errorf("group owner costs by day: %w", err)
		}
		for i := range rows {
			r := &rows[i]
			out.Items = append(out.Items, OwnerCostGroup{
				Key:    r.Key,
				Totals: costFromGroupByDayRow(r),
			})
		}
	case GroupByTaskType:
		rows, err := s.Queries.GroupOwnerCostsByTaskType(ctx, sqlc.GroupOwnerCostsByTaskTypeParams{
			TenantID: toPgUUID(owner.TenantID),
			UserID:   toPgUUID(owner.UserID),
			FromTs:   timestampToPg(from),
			ToTs:     timestampToPg(to),
		})
		if err != nil {
			return OwnerCostGrouped{}, fmt.Errorf("group owner costs by task_type: %w", err)
		}
		for i := range rows {
			r := &rows[i]
			out.Items = append(out.Items, OwnerCostGroup{
				Key:    r.Key,
				Totals: costFromGroupByTaskTypeRow(r),
			})
		}
	case GroupByModel:
		rows, err := s.Queries.GroupOwnerCostsByModel(ctx, sqlc.GroupOwnerCostsByModelParams{
			TenantID: toPgUUID(owner.TenantID),
			UserID:   toPgUUID(owner.UserID),
			FromTs:   timestampToPg(from),
			ToTs:     timestampToPg(to),
		})
		if err != nil {
			return OwnerCostGrouped{}, fmt.Errorf("group owner costs by model: %w", err)
		}
		for i := range rows {
			r := &rows[i]
			out.Items = append(out.Items, OwnerCostGroup{
				Key:    r.Key,
				Totals: costFromGroupByModelRow(r),
			})
		}
	default:
		return OwnerCostGrouped{}, fmt.Errorf("unsupported group_by %q (HTTP layer should have rejected this)", groupBy)
	}
	return out, nil
}

// ListPricing returns every pricing row currently in force, owner-agnostic.
// Empty pricing table → Items = [] (initialised empty slice).
func (s *CostReadService) ListPricing(ctx context.Context) (PricingList, error) {
	rows, err := s.Queries.ListCurrentPricing(ctx)
	if err != nil {
		return PricingList{}, fmt.Errorf("list current pricing: %w", err)
	}
	out := PricingList{Items: make([]PricingEntry, 0, len(rows))}
	for i := range rows {
		r := &rows[i]
		out.Items = append(out.Items, pricingEntryFromRow(r))
	}
	return out, nil
}

// --- helpers ---------------------------------------------------------------

// (toPgUUID lives in service.go and is reused here.)

func timestampToPg(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func costFromTaskCostWithOwnerRow(r *sqlc.GetTaskCostWithOwnerRow) CostSummary {
	return CostSummary{
		AmountUSD:    numericToDecimalString(r.AmountUsd),
		InputTokens:  r.InputTokens,
		OutputTokens: r.OutputTokens,
		CachedTokens: r.CachedTokens,
		ToolCalls:    r.ToolCalls,
		WallTimeMs:   r.WallTimeMs,
	}
}

func versionCostBreakdownFromRow(r *sqlc.ListVersionCostsForTaskRow) VersionCostBreakdown {
	return VersionCostBreakdown{
		VersionID: uuid.UUID(r.VersionID.Bytes),
		VersionNo: r.VersionNo,
		CreatedAt: r.CreatedAt.Time,
		Cost: CostSummary{
			AmountUSD:    numericToDecimalString(r.AmountUsd),
			InputTokens:  derefInt64(r.InputTokens),
			OutputTokens: derefInt64(r.OutputTokens),
			CachedTokens: derefInt64(r.CachedTokens),
			ToolCalls:    derefInt32(r.ToolCalls),
			WallTimeMs:   derefInt64(r.WallTimeMs),
		},
	}
}

func versionCostDetailFromRow(r *sqlc.GetVersionCostWithOwnerRow) VersionCostDetail {
	return VersionCostDetail{
		VersionID: uuid.UUID(r.VersionID.Bytes),
		TaskID:    uuid.UUID(r.TaskID.Bytes),
		VersionNo: r.VersionNo,
		Cost: CostSummary{
			AmountUSD:    numericToDecimalString(r.AmountUsd),
			InputTokens:  derefInt64(r.InputTokens),
			OutputTokens: derefInt64(r.OutputTokens),
			CachedTokens: derefInt64(r.CachedTokens),
			ToolCalls:    derefInt32(r.ToolCalls),
			WallTimeMs:   derefInt64(r.WallTimeMs),
		},
		UpdatedAt: pgTimePtr(r.UpdatedAt),
	}
}

func costFromSumOwnerCostsRow(r *sqlc.SumOwnerCostsRow) CostSummary {
	return CostSummary{
		AmountUSD:    numericToDecimalString(r.AmountUsd),
		InputTokens:  r.InputTokens,
		OutputTokens: r.OutputTokens,
		CachedTokens: r.CachedTokens,
		ToolCalls:    r.ToolCalls,
		WallTimeMs:   r.WallTimeMs,
	}
}

func costFromGroupByDayRow(r *sqlc.GroupOwnerCostsByDayRow) CostSummary {
	return CostSummary{
		AmountUSD:    numericToDecimalString(r.AmountUsd),
		InputTokens:  r.InputTokens,
		OutputTokens: r.OutputTokens,
		CachedTokens: r.CachedTokens,
		ToolCalls:    r.ToolCalls,
		WallTimeMs:   r.WallTimeMs,
	}
}

func costFromGroupByTaskTypeRow(r *sqlc.GroupOwnerCostsByTaskTypeRow) CostSummary {
	return CostSummary{
		AmountUSD:    numericToDecimalString(r.AmountUsd),
		InputTokens:  r.InputTokens,
		OutputTokens: r.OutputTokens,
		CachedTokens: r.CachedTokens,
		ToolCalls:    r.ToolCalls,
		WallTimeMs:   r.WallTimeMs,
	}
}

func costFromGroupByModelRow(r *sqlc.GroupOwnerCostsByModelRow) CostSummary {
	return CostSummary{
		AmountUSD:    numericToDecimalString(r.AmountUsd),
		InputTokens:  r.InputTokens,
		OutputTokens: r.OutputTokens,
		CachedTokens: r.CachedTokens,
		ToolCalls:    r.ToolCalls,
		WallTimeMs:   r.WallTimeMs,
	}
}

func pricingEntryFromRow(r *sqlc.Pricing) PricingEntry {
	return PricingEntry{
		ID:           uuid.UUID(r.ID.Bytes),
		ResourceKind: r.ResourceKind,
		ResourceName: r.ResourceName,
		Unit:         r.Unit,
		UnitPriceUSD: numericToDecimalString(r.UnitPriceUsd),
		EffectiveAt:  r.EffectiveAt.Time,
		ExpiresAt:    pgTimePtr(r.ExpiresAt),
	}
}

func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func derefInt32(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}
