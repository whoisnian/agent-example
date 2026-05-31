package task

import (
	"time"

	"github.com/google/uuid"
)

// --- /tasks/{id}/cost ------------------------------------------------------

// TaskCostDetail is the payload of GET /api/v1/tasks/{task_id}/cost. `Total`
// is the across-versions aggregate; `ByVersion` is the per-version breakdown
// ordered by `version_no` ascending. A version with no settled events yet
// still appears with an all-zero `CostSummary` (LEFT JOIN, zero-fill).
type TaskCostDetail struct {
	TaskID    uuid.UUID              `json:"task_id"`
	Total     CostSummary            `json:"total"`
	ByVersion []VersionCostBreakdown `json:"by_version"`
}

// VersionCostBreakdown is one row of the by_version array.
type VersionCostBreakdown struct {
	VersionID uuid.UUID   `json:"version_id"`
	VersionNo int32       `json:"version_no"`
	CreatedAt time.Time   `json:"created_at"`
	Cost      CostSummary `json:"cost"`
}

// --- /versions/{id}/cost ---------------------------------------------------

// VersionCostDetail is the payload of GET /api/v1/versions/{version_id}/cost.
// `UpdatedAt` is nil (JSON null) when no settled events have been written
// for this version yet (i.e., no `task_costs` row exists).
type VersionCostDetail struct {
	VersionID uuid.UUID   `json:"version_id"`
	TaskID    uuid.UUID   `json:"task_id"`
	VersionNo int32       `json:"version_no"`
	Cost      CostSummary `json:"cost"`
	UpdatedAt *time.Time  `json:"updated_at"`
}

// --- /me/cost --------------------------------------------------------------

// OwnerCostTotal is the no-group_by payload of /api/v1/me/cost. The two
// rollup shapes are split into distinct types so each marshals to a clean
// discriminated JSON shape — no omitempty gymnastics, no zero-value `Total`
// leaking into the grouped branch.
type OwnerCostTotal struct {
	Total CostSummary `json:"total"`
}

// OwnerCostGrouped is the grouped-rollup payload. Items is ordered by Key
// ascending. An empty result emits Items as `[]` (callers MUST initialise
// the slice — never leave it nil).
type OwnerCostGrouped struct {
	GroupBy string            `json:"group_by"`
	Items   []OwnerCostGroup  `json:"items"`
}

// OwnerCostGroup is a single bucket in OwnerCostGrouped.Items.
type OwnerCostGroup struct {
	Key    string      `json:"key"`
	Totals CostSummary `json:"totals"`
}

// GroupBy enum values accepted by /me/cost's `group_by` query parameter.
const (
	GroupByDay      = "day"
	GroupByTaskType = "task_type"
	GroupByModel    = "model"
)

// --- /pricing --------------------------------------------------------------

// PricingEntry is one row of the /api/v1/pricing response. `UnitPriceUSD` is
// rendered with the same decimal-string convention as `amount_usd` so a
// consumer that compares prices across endpoints sees one wire format.
// `ExpiresAt` is nil (JSON null) for open-ended pricing windows.
type PricingEntry struct {
	ID            uuid.UUID  `json:"id"`
	ResourceKind  string     `json:"resource_kind"`
	ResourceName  string     `json:"resource_name"`
	Unit          string     `json:"unit"`
	UnitPriceUSD  string     `json:"unit_price_usd"`
	EffectiveAt   time.Time  `json:"effective_at"`
	ExpiresAt     *time.Time `json:"expires_at"`
}

// PricingList is the GET /api/v1/pricing payload.
type PricingList struct {
	Items []PricingEntry `json:"items"`
}
