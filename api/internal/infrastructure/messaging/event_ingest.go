package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	amqp "github.com/rabbitmq/amqp091-go"

	taskdomain "github.com/whoisnian/agent-example/api/internal/domain/task"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// EventIngester is the domain seam the handler drives. *task.Service satisfies
// it. Kept as an interface so the handler is unit-testable with a fake.
type EventIngester interface {
	IngestEvent(ctx context.Context, in taskdomain.IngestEventInput) (bool, error)
}

// taskEventEnvelope is the worker's *bare* TaskEvent JSON (worker/core/
// publisher.py). Both directions are bare: the Relayer also publishes the flat
// payload as the body (ARCHITECTURE §5.3), so messaging.Envelope is only an
// internal relayer→publisher carrier, never a wire shape. Dedupe is on the
// body's (run_id, seq), not the AMQP idempotency_key header.
type taskEventEnvelope struct {
	TaskID    string          `json:"task_id"`
	VersionID string          `json:"version_id"`
	RunID     string          `json:"run_id"`
	Seq       int64           `json:"seq"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
}

// EventIngestHandler decodes a task-event delivery and drives the domain
// ingest, settling the delivery per outcome:
//   - malformed body / missing required field → Nack(requeue=false) → DLQ;
//   - transient DB error (isRetryable)         → Nack(requeue=true)  → retry;
//   - permanent DB error                       → Nack(requeue=false) → DLQ;
//   - success                                  → Ack.
type EventIngestHandler struct {
	ingester EventIngester
	logger   *slog.Logger
	metrics  *observability.Metrics
}

// NewEventIngestHandler builds the handler.
func NewEventIngestHandler(ingester EventIngester, logger *slog.Logger, m *observability.Metrics) *EventIngestHandler {
	return &EventIngestHandler{ingester: ingester, logger: logger, metrics: m}
}

// Handle implements DeliveryHandler.
//
//nolint:gocritic // hugeParam: amqp.Delivery is the broker's value type and matches the DeliveryHandler signature.
func (h *EventIngestHandler) Handle(ctx context.Context, d amqp.Delivery) error {
	in, err := decodeEvent(d.Body)
	if err != nil {
		h.metrics.EventIngestMalformedTotal.Inc()
		h.logger.Warn("event_ingest_malformed", slog.String("err", err.Error()))
		return d.Nack(false, false) // DLQ; never requeue a poison message
	}

	transitioned, ierr := h.ingester.IngestEvent(ctx, in)
	if ierr != nil {
		if isRetryable(ierr) {
			h.logger.Warn("event_ingest_transient_error",
				slog.String("run_id", in.RunID.String()),
				slog.Int64("seq", in.Seq),
				slog.String("err", ierr.Error()),
			)
			return d.Nack(false, true) // requeue
		}
		h.logger.Error("event_ingest_permanent_error",
			slog.String("run_id", in.RunID.String()),
			slog.Int64("seq", in.Seq),
			slog.String("err", ierr.Error()),
		)
		return d.Nack(false, false) // DLQ
	}

	h.metrics.EventsIngestedTotal.WithLabelValues(in.Kind).Inc()
	if transitioned {
		h.metrics.EventStatusTransitionsTotal.Inc()
	}
	h.logger.Info("event_ingested",
		slog.String("task_id", in.TaskID.String()),
		slog.String("version_id", in.VersionID.String()),
		slog.String("run_id", in.RunID.String()),
		slog.Int64("seq", in.Seq),
		slog.String("kind", in.Kind),
		slog.Bool("transitioned", transitioned),
	)
	return d.Ack(false)
}

// decodeEvent parses the worker envelope and converts ids. Any missing/invalid
// required field is a permanent decode error → DLQ.
func decodeEvent(body []byte) (taskdomain.IngestEventInput, error) {
	var e taskEventEnvelope
	if err := json.Unmarshal(body, &e); err != nil {
		return taskdomain.IngestEventInput{}, err
	}
	if e.Kind == "" {
		return taskdomain.IngestEventInput{}, errors.New("missing kind")
	}
	taskID, err := uuid.Parse(e.TaskID)
	if err != nil {
		return taskdomain.IngestEventInput{}, errors.New("invalid task_id")
	}
	versionID, err := uuid.Parse(e.VersionID)
	if err != nil {
		return taskdomain.IngestEventInput{}, errors.New("invalid version_id")
	}
	runID, err := uuid.Parse(e.RunID)
	if err != nil {
		return taskdomain.IngestEventInput{}, errors.New("invalid run_id")
	}
	return taskdomain.IngestEventInput{
		TaskID:    taskID,
		VersionID: versionID,
		RunID:     runID,
		Seq:       e.Seq,
		Kind:      e.Kind,
		Payload:   e.Payload,
	}, nil
}

// isRetryable reports whether a processing error is worth requeueing. Only
// genuinely transient faults qualify; everything else (including constraint
// violations, SQLSTATE class 23) defaults to DLQ so a poison message can never
// loop forever.
func isRetryable(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case strings.HasPrefix(pgErr.Code, "08"): // connection exception
			return true
		case strings.HasPrefix(pgErr.Code, "53"): // insufficient resources
			return true
		case pgErr.Code == "40001": // serialization_failure
			return true
		case pgErr.Code == "40P01": // deadlock_detected
			return true
		default:
			return false
		}
	}
	// Default for an unclassifiable error is non-retryable → DLQ. This is the
	// deliberate safe choice (design D8): never let a poison message loop
	// forever. A rare transient fault not surfaced as a PgError above lands in
	// the DLQ for manual replay, which is acceptable for MVP.
	return false
}
