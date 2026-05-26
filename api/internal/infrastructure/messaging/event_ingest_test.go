package messaging

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	amqp "github.com/rabbitmq/amqp091-go"

	taskdomain "github.com/whoisnian/agent-example/api/internal/domain/task"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// metricValue reads the current value of a single (unlabelled) counter via the
// dto Write API — avoids pulling in the prometheus testutil's extra deps.
func metricValue(t *testing.T, c prometheus.Metric) float64 {
	t.Helper()
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatalf("metric write: %v", err)
	}
	return m.GetCounter().GetValue()
}

// fakeAck records the settlement decision the handler made on a delivery.
type fakeAck struct {
	acked       bool
	nacked      bool
	nackRequeue bool
	rejected    bool
	rejectReque bool
}

func (f *fakeAck) Ack(uint64, bool) error { f.acked = true; return nil }
func (f *fakeAck) Nack(_ uint64, _, r bool) error {
	f.nacked = true
	f.nackRequeue = r
	return nil
}
func (f *fakeAck) Reject(_ uint64, r bool) error { f.rejected = true; f.rejectReque = r; return nil }

// fakeIngester returns a canned (transitioned, err).
type fakeIngester struct {
	transitioned bool
	err          error
	called       bool
	gotKind      string
}

//nolint:gocritic // hugeParam: in matches the EventIngester interface signature.
func (f *fakeIngester) IngestEvent(_ context.Context, in taskdomain.IngestEventInput) (bool, error) {
	f.called = true
	f.gotKind = in.Kind
	return f.transitioned, f.err
}

func newTestHandler(ing EventIngester) (*EventIngestHandler, *observability.Metrics) {
	m := observability.NewMetrics()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewEventIngestHandler(ing, logger, m), m
}

func deliveryWith(body string) (amqp.Delivery, *fakeAck) {
	ack := &fakeAck{}
	return amqp.Delivery{Acknowledger: ack, DeliveryTag: 1, Body: []byte(body)}, ack
}

const goodStatusEvent = `{"task_id":"11111111-1111-1111-1111-111111111111",` +
	`"version_id":"22222222-2222-2222-2222-222222222222",` +
	`"run_id":"33333333-3333-3333-3333-333333333333",` +
	`"seq":1,"kind":"status","payload":{"status":"running"},"ts":"2026-05-26T00:00:00Z"}`

func TestHandle_MalformedToDLQ(t *testing.T) {
	ing := &fakeIngester{}
	h, m := newTestHandler(ing)
	d, ack := deliveryWith(`{not json`)

	if err := h.Handle(context.Background(), d); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if ing.called {
		t.Error("ingester should not be called on malformed body")
	}
	if !ack.nacked || ack.nackRequeue {
		t.Errorf("want nack(requeue=false); got nacked=%v requeue=%v", ack.nacked, ack.nackRequeue)
	}
	if got := metricValue(t, m.EventIngestMalformedTotal); got != 1 {
		t.Errorf("malformed counter = %v, want 1", got)
	}
}

func TestHandle_MissingRequiredFieldToDLQ(t *testing.T) {
	ing := &fakeIngester{}
	h, _ := newTestHandler(ing)
	// Valid JSON but missing kind + bad uuids.
	d, ack := deliveryWith(`{"task_id":"nope","seq":1}`)

	_ = h.Handle(context.Background(), d)
	if ing.called {
		t.Error("ingester should not be called when decode fails")
	}
	if !ack.nacked || ack.nackRequeue {
		t.Errorf("want nack(requeue=false) for invalid fields")
	}
}

func TestHandle_SuccessAcks(t *testing.T) {
	ing := &fakeIngester{transitioned: true}
	h, m := newTestHandler(ing)
	d, ack := deliveryWith(goodStatusEvent)

	if err := h.Handle(context.Background(), d); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !ing.called || ing.gotKind != "status" {
		t.Errorf("ingester called=%v kind=%q", ing.called, ing.gotKind)
	}
	if !ack.acked {
		t.Error("want ack on success")
	}
	if got := metricValue(t, m.EventStatusTransitionsTotal); got != 1 {
		t.Errorf("transitions counter = %v, want 1", got)
	}
}

func TestHandle_NoTransitionStillAcksNoTransitionMetric(t *testing.T) {
	ing := &fakeIngester{transitioned: false}
	h, m := newTestHandler(ing)
	d, ack := deliveryWith(goodStatusEvent)

	_ = h.Handle(context.Background(), d)
	if !ack.acked {
		t.Error("want ack")
	}
	if got := metricValue(t, m.EventStatusTransitionsTotal); got != 0 {
		t.Errorf("transitions counter = %v, want 0", got)
	}
}

func TestHandle_TransientErrorRequeues(t *testing.T) {
	ing := &fakeIngester{err: &pgconn.PgError{Code: "40001"}} // serialization_failure
	h, _ := newTestHandler(ing)
	d, ack := deliveryWith(goodStatusEvent)

	_ = h.Handle(context.Background(), d)
	if !ack.nacked || !ack.nackRequeue {
		t.Errorf("want nack(requeue=true) for transient; got nacked=%v requeue=%v", ack.nacked, ack.nackRequeue)
	}
}

func TestHandle_PermanentErrorToDLQ(t *testing.T) {
	ing := &fakeIngester{err: &pgconn.PgError{Code: "23514"}} // check_violation
	h, _ := newTestHandler(ing)
	d, ack := deliveryWith(goodStatusEvent)

	_ = h.Handle(context.Background(), d)
	if !ack.nacked || ack.nackRequeue {
		t.Errorf("want nack(requeue=false) for permanent; got nacked=%v requeue=%v", ack.nacked, ack.nackRequeue)
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"connection 08006", &pgconn.PgError{Code: "08006"}, true},
		{"insufficient resources 53300", &pgconn.PgError{Code: "53300"}, true},
		{"serialization 40001", &pgconn.PgError{Code: "40001"}, true},
		{"deadlock 40P01", &pgconn.PgError{Code: "40P01"}, true},
		{"check violation 23514", &pgconn.PgError{Code: "23514"}, false},
		{"not null 23502", &pgconn.PgError{Code: "23502"}, false},
		{"deadline exceeded", context.DeadlineExceeded, true},
		{"plain error", errors.New("boom"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryable(tt.err); got != tt.want {
				t.Errorf("isRetryable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
