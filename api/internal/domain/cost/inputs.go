package cost

import (
	"math/big"
	"time"

	"github.com/google/uuid"
)

// CostEventInput is the decoded worker `CostEvent` envelope handed to the
// Cost Service settler by the messaging layer. Field semantics mirror
// worker/core/messages.py::CostEvent; nullable quantity fields use pointer
// types so the absence of a number (e.g., a streaming LLM call with no
// token totals) is distinguishable from explicit zero.
type CostEventInput struct {
	TaskID       uuid.UUID
	VersionID    uuid.UUID
	RunID        uuid.UUID
	Seq          int64
	Kind         string // "llm" | "tool" | "compute"
	ResourceName string

	InputTokens  *int64
	OutputTokens *int64
	CachedTokens *int64
	Calls        *int32
	DurationMs   *int64

	OccurredAt time.Time
}

// SettleResultKind enumerates the four outcomes of a settle attempt — drives
// the consumer's ack/nack + metric labels. Keep in sync with the spec's
// "Delivery Settlement Rules" table.
type SettleResultKind string

const (
	SettleOK              SettleResultKind = "ok"
	SettleDuplicate       SettleResultKind = "duplicate"
	SettleMissingPricing  SettleResultKind = "missing_pricing"
	SettleErrorMismatch   SettleResultKind = "error_mismatch"
)

// SettleResult is what the settler reports back. AmountUSD is *big.Rat so the
// caller can feed exact values to the cost_amount_settled_usd_total counter
// without losing precision in the persisted value; on Duplicate /
// MissingPricing / ErrorMismatch it may be nil.
type SettleResult struct {
	Kind      SettleResultKind
	AmountUSD *big.Rat
}
