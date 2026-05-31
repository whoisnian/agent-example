package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"

	costdomain "github.com/whoisnian/agent-example/api/internal/domain/cost"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// CostSettler is the domain seam the cost-ingest handler drives. The concrete
// implementation is *application/cost.Settler. The interface keeps the
// handler unit-testable with a fake.
type CostSettler interface {
	Settle(ctx context.Context, in costdomain.CostEventInput) (costdomain.SettleResult, error)
}

// costEventEnvelope is the worker's CostEvent JSON shape (see
// worker/core/messages.py::CostEvent and worker-messaging §"Cost Event
// Publisher"). The body's `kind` (NOT the routing key) is the authoritative
// branch label.
type costEventEnvelope struct {
	TaskID       string    `json:"task_id"`
	VersionID    string    `json:"version_id"`
	RunID        string    `json:"run_id"`
	Seq          int64     `json:"seq"`
	Kind         string    `json:"kind"`
	ResourceName string    `json:"resource_name"`
	InputTokens  *int64    `json:"input_tokens"`
	OutputTokens *int64    `json:"output_tokens"`
	CachedTokens *int64    `json:"cached_tokens"`
	Calls        *int32    `json:"calls"`
	DurationMs   *int64    `json:"duration_ms"`
	OccurredAt   time.Time `json:"occurred_at"`
}

// CostIngestHandler decodes a cost-event delivery and drives the settler.
// Delivery outcomes settle per task-cost-ingest §"Delivery Settlement Rules";
// transient pgx errors requeue (reusing `isRetryable` so both consumers
// share one retry policy).
type CostIngestHandler struct {
	settler CostSettler
	logger  *slog.Logger
	metrics *observability.Metrics
}

// NewCostIngestHandler builds the handler.
func NewCostIngestHandler(settler CostSettler, logger *slog.Logger, m *observability.Metrics) *CostIngestHandler {
	return &CostIngestHandler{settler: settler, logger: logger, metrics: m}
}

// Handle implements DeliveryHandler.
//
//nolint:gocritic // hugeParam: amqp.Delivery is the broker's value type and matches the DeliveryHandler signature.
func (h *CostIngestHandler) Handle(ctx context.Context, d amqp.Delivery) error {
	logger := h.logger
	if tp, ok := d.Headers["traceparent"].(string); ok && tp != "" {
		logger = logger.With(slog.String("traceparent", tp))
	}

	in, kind, err := decodeCostEvent(d.Body)
	if err != nil {
		h.metrics.CostEventsSettledTotal.WithLabelValues(safeLabel(kind), "error").Inc()
		logger.Warn("cost_ingest_malformed",
			slog.Int("body_len", len(d.Body)),
			slog.String("err", err.Error()),
		)
		return d.Nack(false, false) // DLQ; never requeue a poison message
	}
	h.metrics.CostEventsConsumedTotal.WithLabelValues(kind).Inc()

	if !isKnownCostKind(kind) {
		h.metrics.CostEventsSettledTotal.WithLabelValues(kind, "error").Inc()
		logger.Warn("cost_ingest_unknown_kind",
			slog.String("kind", kind),
			slog.String("run_id", in.RunID.String()),
			slog.Int64("seq", in.Seq),
		)
		return d.Nack(false, false)
	}

	start := time.Now()
	res, ierr := h.settler.Settle(ctx, in)
	h.metrics.CostEventSettleDurationSeconds.Observe(time.Since(start).Seconds())

	if ierr != nil {
		if isRetryable(ierr) {
			logger.Warn("cost_ingest_transient_error",
				slog.String("kind", kind),
				slog.String("run_id", in.RunID.String()),
				slog.Int64("seq", in.Seq),
				slog.String("err", ierr.Error()),
			)
			return d.Nack(false, true) // requeue
		}
		h.metrics.CostEventsSettledTotal.WithLabelValues(kind, "error").Inc()
		logger.Error("cost_ingest_permanent_error",
			slog.String("kind", kind),
			slog.String("run_id", in.RunID.String()),
			slog.Int64("seq", in.Seq),
			slog.String("err", ierr.Error()),
		)
		return d.Nack(false, false) // DLQ
	}

	switch res.Kind {
	case costdomain.SettleOK:
		h.metrics.CostEventsSettledTotal.WithLabelValues(kind, "ok").Inc()
		if res.AmountUSD != nil {
			if f, exact := res.AmountUSD.Float64(); !exact || !isFinite(f) { //nolint:staticcheck // SA1029 false positive; we check exactness then bind.
				// Fall back to the (possibly inexact) float without panicking — the
				// authoritative value is already in DB; the metric is best-effort.
				if isFinite(f) {
					h.metrics.CostAmountSettledUSDTotal.Add(f)
				}
			} else {
				h.metrics.CostAmountSettledUSDTotal.Add(f)
			}
		}
	case costdomain.SettleDuplicate:
		h.metrics.CostEventsSettledTotal.WithLabelValues(kind, "duplicate").Inc()
	case costdomain.SettleMissingPricing:
		h.metrics.CostEventsSettledTotal.WithLabelValues(kind, "missing_pricing").Inc()
		h.metrics.CostPricingMissingTotal.WithLabelValues(kind, in.ResourceName).Inc()
		logger.Warn("cost_pricing_missing",
			slog.String("kind", kind),
			slog.String("resource", in.ResourceName),
			slog.String("run_id", in.RunID.String()),
			slog.Int64("seq", in.Seq),
		)
	case costdomain.SettleErrorMismatch:
		h.metrics.CostEventsSettledTotal.WithLabelValues(kind, "error").Inc()
		logger.Error("cost_ingest_task_id_mismatch",
			slog.String("task_id", in.TaskID.String()),
			slog.String("version_id", in.VersionID.String()),
			slog.String("run_id", in.RunID.String()),
			slog.Int64("seq", in.Seq),
		)
		return d.Nack(false, false) // DLQ
	}

	logger.Info("cost_event_settled",
		slog.String("kind", kind),
		slog.String("result", string(res.Kind)),
		slog.String("task_id", in.TaskID.String()),
		slog.String("version_id", in.VersionID.String()),
		slog.String("run_id", in.RunID.String()),
		slog.Int64("seq", in.Seq),
	)
	return d.Ack(false)
}

// decodeCostEvent parses the worker envelope. Returns the decoded input and
// the `kind` string (for metrics labelling) separately so a malformed body
// without a parsable `kind` still labels the error metric correctly.
func decodeCostEvent(body []byte) (costdomain.CostEventInput, string, error) {
	var e costEventEnvelope
	if err := json.Unmarshal(body, &e); err != nil {
		return costdomain.CostEventInput{}, "", err
	}
	if e.Kind == "" {
		return costdomain.CostEventInput{}, "", errors.New("missing kind")
	}
	if e.ResourceName == "" {
		return costdomain.CostEventInput{}, e.Kind, errors.New("missing resource_name")
	}
	taskID, err := uuid.Parse(e.TaskID)
	if err != nil {
		return costdomain.CostEventInput{}, e.Kind, errors.New("invalid task_id")
	}
	versionID, err := uuid.Parse(e.VersionID)
	if err != nil {
		return costdomain.CostEventInput{}, e.Kind, errors.New("invalid version_id")
	}
	runID, err := uuid.Parse(e.RunID)
	if err != nil {
		return costdomain.CostEventInput{}, e.Kind, errors.New("invalid run_id")
	}
	if e.OccurredAt.IsZero() {
		return costdomain.CostEventInput{}, e.Kind, errors.New("missing occurred_at")
	}
	return costdomain.CostEventInput{
		TaskID:       taskID,
		VersionID:    versionID,
		RunID:        runID,
		Seq:          e.Seq,
		Kind:         e.Kind,
		ResourceName: e.ResourceName,
		InputTokens:  e.InputTokens,
		OutputTokens: e.OutputTokens,
		CachedTokens: e.CachedTokens,
		Calls:        e.Calls,
		DurationMs:   e.DurationMs,
		OccurredAt:   e.OccurredAt,
	}, e.Kind, nil
}

func isKnownCostKind(k string) bool {
	switch k {
	case "llm", "tool", "compute":
		return true
	}
	return false
}

// safeLabel substitutes an `unknown` placeholder for an empty metric label
// value (Prometheus tolerates empty strings but they read badly in dashboards).
func safeLabel(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func isFinite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}
