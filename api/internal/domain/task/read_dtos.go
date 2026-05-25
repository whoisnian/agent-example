package task

import (
	"encoding/json"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
)

// amountScale is the fixed fractional scale of `amount_usd`, matching the
// NUMERIC(18,8) money columns. The wire value is always rendered to exactly
// this many decimal places.
const amountScale = 8

// zeroAmount is the canonical "no cost yet" string (see numericToDecimalString).
const zeroAmount = "0.00000000"

// CostSummary is the embedded best-effort cost block (design D4/D5). `AmountUSD`
// is a decimal string preserving full 8-dp scale — never a JSON number — so an
// 8-dp money value is not rounded by float64. The remaining fields are integers.
type CostSummary struct {
	AmountUSD    string `json:"amount_usd"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	CachedTokens int64  `json:"cached_tokens"`
	ToolCalls    int32  `json:"tool_calls"`
	WallTimeMs   int64  `json:"wall_time_ms"`
}

// zeroCost is the all-zero summary used when a scope has no task_costs row.
func zeroCost() CostSummary {
	return CostSummary{AmountUSD: zeroAmount}
}

// TaskSummary is one row of the task list.
type TaskSummary struct {
	ID             uuid.UUID   `json:"id"`
	Title          string      `json:"title"`
	TaskType       string      `json:"task_type"`
	Status         string      `json:"status"`
	CurrentVersion *uuid.UUID  `json:"current_version"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
	Cost           CostSummary `json:"cost"`
}

// TaskListPage is the paginated list result. Page / PageSize echo the effective
// (post-clamp) values.
type TaskListPage struct {
	Items    []TaskSummary `json:"items"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
	Total    int64         `json:"total"`
}

// TaskInfo is the full task row returned by the detail endpoint.
type TaskInfo struct {
	ID             uuid.UUID  `json:"id"`
	TenantID       uuid.UUID  `json:"tenant_id"`
	UserID         uuid.UUID  `json:"user_id"`
	Title          string     `json:"title"`
	TaskType       string     `json:"task_type"`
	Status         string     `json:"status"`
	CurrentVersion *uuid.UUID `json:"current_version"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// TaskDetail is the GET /tasks/{id} payload. CurrentVersion is nil (JSON null)
// when tasks.current_version is NULL.
type TaskDetail struct {
	Task           TaskInfo     `json:"task"`
	CurrentVersion *VersionNode `json:"current_version"`
	Cost           CostSummary  `json:"cost"`
}

// VersionNode is a lightweight version-tree node: it deliberately omits
// `prompt` / `params` (those live in version detail). ParentID is nil for the
// root; IsActive is always a concrete boolean.
type VersionNode struct {
	ID           uuid.UUID   `json:"id"`
	ParentID     *uuid.UUID  `json:"parent_id"`
	VersionNo    int32       `json:"version_no"`
	Status       string      `json:"status"`
	IsActive     bool        `json:"is_active"`
	ArtifactRoot *string     `json:"artifact_root"`
	CreatedAt    time.Time   `json:"created_at"`
	Cost         CostSummary `json:"cost"`
}

// VersionTree wraps the flat, version_no-ordered node array.
type VersionTree struct {
	Items []VersionNode `json:"items"`
}

// VersionFull is the full version row for the detail endpoint, including the
// heavyweight `prompt` / `params` the tree node omits. Params is raw JSON.
type VersionFull struct {
	ID           uuid.UUID       `json:"id"`
	TaskID       uuid.UUID       `json:"task_id"`
	ParentID     *uuid.UUID      `json:"parent_id"`
	VersionNo    int32           `json:"version_no"`
	Prompt       string          `json:"prompt"`
	Params       json.RawMessage `json:"params"`
	Status       string          `json:"status"`
	IsActive     bool            `json:"is_active"`
	ArtifactRoot *string         `json:"artifact_root"`
	CreatedAt    time.Time       `json:"created_at"`
}

// RunSummary is one execution attempt. Error is raw JSON and renders as JSON
// null when the column is NULL.
type RunSummary struct {
	ID            uuid.UUID       `json:"id"`
	AttemptNo     int32           `json:"attempt_no"`
	Status        string          `json:"status"`
	StartedAt     *time.Time      `json:"started_at"`
	EndedAt       *time.Time      `json:"ended_at"`
	LastHeartbeat *time.Time      `json:"last_heartbeat"`
	Error         json.RawMessage `json:"error"`
}

// VersionDetail is the GET /versions/{id} payload. Runs is always an array.
type VersionDetail struct {
	Version VersionFull  `json:"version"`
	Runs    []RunSummary `json:"runs"`
	Cost    CostSummary  `json:"cost"`
}

// EventItem is one task_events row. RunID is nullable; both `id` (the global
// cursor) and `seq` (the per-run frame number) are exposed so a realtime client
// can reconcile. Payload is raw JSON, never base64.
type EventItem struct {
	ID        int64           `json:"id"`
	VersionID uuid.UUID       `json:"version_id"`
	RunID     *uuid.UUID      `json:"run_id"`
	Seq       int64           `json:"seq"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

// EventPage is the backfill result. NextAfterID is the last returned event id,
// or the input after_id when empty, so a client can resume polling.
type EventPage struct {
	Items       []EventItem `json:"items"`
	NextAfterID int64       `json:"next_after_id"`
}

// ---------------------------------------------------------------------------
// mapping helpers
// ---------------------------------------------------------------------------

// numericToDecimalString renders a pgtype.Numeric as a decimal string with
// exactly amountScale fractional digits (e.g. "0.62000000"). Invalid, NaN, or
// infinite values degrade to zeroAmount so a read never fails on cost data.
func numericToDecimalString(n pgtype.Numeric) string {
	if !n.Valid || n.NaN || n.Int == nil || n.InfinityModifier != pgtype.Finite {
		return zeroAmount
	}

	// value = Int * 10^Exp; we want it scaled to amountScale fractional digits,
	// i.e. the integer (value * 10^amountScale).
	scaled := new(big.Int).Set(n.Int)
	exp := int(n.Exp) + amountScale
	switch {
	case exp > 0:
		scaled.Mul(scaled, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exp)), nil))
	case exp < 0:
		scaled.Quo(scaled, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-exp)), nil))
	}

	neg := scaled.Sign() < 0
	if neg {
		scaled.Neg(scaled)
	}
	digits := scaled.String()
	if len(digits) <= amountScale {
		digits = strings.Repeat("0", amountScale-len(digits)+1) + digits
	}
	out := digits[:len(digits)-amountScale] + "." + digits[len(digits)-amountScale:]
	if neg {
		out = "-" + out
	}
	return out
}

// costFromTaskCost maps a per-version task_costs row to the summary DTO.
func costFromTaskCost(c *sqlc.TaskCost) CostSummary {
	return CostSummary{
		AmountUSD:    numericToDecimalString(c.AmountUsd),
		InputTokens:  c.InputTokens,
		OutputTokens: c.OutputTokens,
		CachedTokens: c.CachedTokens,
		ToolCalls:    c.ToolCalls,
		WallTimeMs:   c.WallTimeMs,
	}
}

// costFromTaskAgg maps the task-level GetTaskCost aggregate row to the DTO.
func costFromTaskAgg(c *sqlc.GetTaskCostRow) CostSummary {
	return CostSummary{
		AmountUSD:    numericToDecimalString(c.AmountUsd),
		InputTokens:  c.InputTokens,
		OutputTokens: c.OutputTokens,
		CachedTokens: c.CachedTokens,
		ToolCalls:    c.ToolCalls,
		WallTimeMs:   c.WallTimeMs,
	}
}

// costFromListRow maps a batched ListTaskCostsByTasks row to the DTO.
func costFromListRow(c *sqlc.ListTaskCostsByTasksRow) CostSummary {
	return CostSummary{
		AmountUSD:    numericToDecimalString(c.AmountUsd),
		InputTokens:  c.InputTokens,
		OutputTokens: c.OutputTokens,
		CachedTokens: c.CachedTokens,
		ToolCalls:    c.ToolCalls,
		WallTimeMs:   c.WallTimeMs,
	}
}

// versionNodeFromRow builds the lightweight tree node (cost filled by caller).
func versionNodeFromRow(v *sqlc.TaskVersion) VersionNode {
	return VersionNode{
		ID:           fromPgUUID(v.ID),
		ParentID:     pgUUIDToPtr(v.ParentID),
		VersionNo:    v.VersionNo,
		Status:       v.Status,
		IsActive:     derefBool(v.IsActive),
		ArtifactRoot: v.ArtifactRoot,
		CreatedAt:    v.CreatedAt.Time,
	}
}

// versionFullFromRow builds the full version DTO for the detail endpoint.
func versionFullFromRow(v *sqlc.TaskVersion) VersionFull {
	return VersionFull{
		ID:           fromPgUUID(v.ID),
		TaskID:       fromPgUUID(v.TaskID),
		ParentID:     pgUUIDToPtr(v.ParentID),
		VersionNo:    v.VersionNo,
		Prompt:       v.Prompt,
		Params:       json.RawMessage(v.Params),
		Status:       v.Status,
		IsActive:     derefBool(v.IsActive),
		ArtifactRoot: v.ArtifactRoot,
		CreatedAt:    v.CreatedAt.Time,
	}
}

// runSummaryFromRow maps a task_runs row; Error passes through as raw JSON.
func runSummaryFromRow(r *sqlc.TaskRun) RunSummary {
	return RunSummary{
		ID:            fromPgUUID(r.ID),
		AttemptNo:     r.AttemptNo,
		Status:        r.Status,
		StartedAt:     pgTimePtr(r.StartedAt),
		EndedAt:       pgTimePtr(r.EndedAt),
		LastHeartbeat: pgTimePtr(r.LastHeartbeat),
		Error:         json.RawMessage(r.Error),
	}
}

// eventItemFromRow maps a task_events row; RunID is nil when NULL, Payload is
// raw JSON.
func eventItemFromRow(e *sqlc.TaskEvent) EventItem {
	return EventItem{
		ID:        e.ID,
		VersionID: fromPgUUID(e.VersionID),
		RunID:     pgUUIDToPtr(e.RunID),
		Seq:       e.Seq,
		Kind:      e.Kind,
		Payload:   json.RawMessage(e.Payload),
		CreatedAt: e.CreatedAt.Time,
	}
}

// pgUUIDToPtr returns nil for an invalid (NULL) pgtype.UUID, else a pointer to
// the stdlib uuid.
func pgUUIDToPtr(u pgtype.UUID) *uuid.UUID {
	if !u.Valid {
		return nil
	}
	id := uuid.UUID(u.Bytes)
	return &id
}

// pgTimePtr returns nil for a NULL timestamptz, else a pointer to the time.
func pgTimePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	tt := t.Time
	return &tt
}

// derefBool reads a *bool (sqlc's type for the GENERATED is_active column) as a
// concrete boolean, treating nil as false.
func derefBool(b *bool) bool {
	return b != nil && *b
}
