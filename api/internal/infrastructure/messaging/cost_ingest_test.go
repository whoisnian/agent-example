package messaging

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	amqp "github.com/rabbitmq/amqp091-go"

	costdomain "github.com/whoisnian/agent-example/api/internal/domain/cost"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// counterVecValue reads a labelled counter's current value, fatally if the
// label set is unknown to the vector.
func counterVecValue(t *testing.T, cv *prometheus.CounterVec, labels ...string) float64 {
	t.Helper()
	c, err := cv.GetMetricWithLabelValues(labels...)
	if err != nil {
		t.Fatalf("get counter %v: %v", labels, err)
	}
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatalf("metric write: %v", err)
	}
	return m.GetCounter().GetValue()
}

// fakeSettler is the CostSettler stand-in.
type fakeSettler struct {
	called bool
	gotIn  costdomain.CostEventInput
	res    costdomain.SettleResult
	err    error
}

//nolint:gocritic // hugeParam: in matches the CostSettler interface signature.
func (f *fakeSettler) Settle(_ context.Context, in costdomain.CostEventInput) (costdomain.SettleResult, error) {
	f.called = true
	f.gotIn = in
	return f.res, f.err
}

func newTestCostHandler(s CostSettler) (*CostIngestHandler, *observability.Metrics) {
	m := observability.NewMetrics()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewCostIngestHandler(s, logger, m), m
}

const goodLLMEvent = `{"task_id":"11111111-1111-1111-1111-111111111111",` +
	`"version_id":"22222222-2222-2222-2222-222222222222",` +
	`"run_id":"33333333-3333-3333-3333-333333333333",` +
	`"seq":1,"kind":"llm","resource_name":"claude-opus-4-7",` +
	`"input_tokens":2000,"output_tokens":500,"occurred_at":"2026-05-30T00:00:00Z"}`

func TestCostHandle_MalformedBodyDLQ(t *testing.T) {
	t.Parallel()
	s := &fakeSettler{}
	h, m := newTestCostHandler(s)
	d, ack := deliveryWith(`{not json`)

	if err := h.Handle(context.Background(), d); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if s.called {
		t.Error("settler should not be called on malformed body")
	}
	if !ack.nacked || ack.nackRequeue {
		t.Errorf("want nack(requeue=false); got nacked=%v requeue=%v", ack.nacked, ack.nackRequeue)
	}
	if v := counterVecValue(t, m.CostEventsSettledTotal, "unknown", "error"); v != 1 {
		t.Errorf("settled{unknown,error} = %v, want 1", v)
	}
}

func TestCostHandle_MissingKindDLQ(t *testing.T) {
	t.Parallel()
	s := &fakeSettler{}
	h, m := newTestCostHandler(s)
	// Valid JSON but kind is empty → decode error → DLQ.
	d, ack := deliveryWith(`{"task_id":"11111111-1111-1111-1111-111111111111","version_id":"22222222-2222-2222-2222-222222222222","run_id":"33333333-3333-3333-3333-333333333333","seq":1,"resource_name":"x","occurred_at":"2026-05-30T00:00:00Z"}`)

	_ = h.Handle(context.Background(), d)
	if s.called {
		t.Error("settler must not be called")
	}
	if !ack.nacked || ack.nackRequeue {
		t.Errorf("want nack(requeue=false)")
	}
	if v := counterVecValue(t, m.CostEventsSettledTotal, "unknown", "error"); v != 1 {
		t.Errorf("settled{unknown,error} = %v, want 1", v)
	}
}

func TestCostHandle_UnknownKindDLQ(t *testing.T) {
	t.Parallel()
	s := &fakeSettler{}
	h, m := newTestCostHandler(s)
	d, ack := deliveryWith(`{"task_id":"11111111-1111-1111-1111-111111111111","version_id":"22222222-2222-2222-2222-222222222222","run_id":"33333333-3333-3333-3333-333333333333","seq":1,"kind":"bogus","resource_name":"x","occurred_at":"2026-05-30T00:00:00Z"}`)

	_ = h.Handle(context.Background(), d)
	if s.called {
		t.Error("settler must not be called on unknown kind")
	}
	if !ack.nacked || ack.nackRequeue {
		t.Errorf("want nack(requeue=false)")
	}
	if v := counterVecValue(t, m.CostEventsSettledTotal, "bogus", "error"); v != 1 {
		t.Errorf("settled{bogus,error} = %v, want 1", v)
	}
	// kind is parseable so the consumed counter still bumps.
	if v := counterVecValue(t, m.CostEventsConsumedTotal, "bogus"); v != 1 {
		t.Errorf("consumed{bogus} = %v, want 1", v)
	}
}

func TestCostHandle_OkSettledAck(t *testing.T) {
	t.Parallel()
	amt, _ := new(big.Rat).SetString("13.5")
	s := &fakeSettler{res: costdomain.SettleResult{Kind: costdomain.SettleOK, AmountUSD: amt}}
	h, m := newTestCostHandler(s)
	d, ack := deliveryWith(goodLLMEvent)

	if err := h.Handle(context.Background(), d); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !s.called {
		t.Error("settler must be called")
	}
	if !ack.acked || ack.nacked {
		t.Errorf("want Ack; got acked=%v nacked=%v", ack.acked, ack.nacked)
	}
	if v := counterVecValue(t, m.CostEventsSettledTotal, "llm", "ok"); v != 1 {
		t.Errorf("settled{llm,ok} = %v, want 1", v)
	}
	if v := counterVecValue(t, m.CostEventsConsumedTotal, "llm"); v != 1 {
		t.Errorf("consumed{llm} = %v, want 1", v)
	}
	// AmountSettledUSDTotal is unlabelled; read directly.
	var dm dto.Metric
	if err := m.CostAmountSettledUSDTotal.Write(&dm); err != nil {
		t.Fatalf("amount write: %v", err)
	}
	if got := dm.GetCounter().GetValue(); got != 13.5 {
		t.Errorf("CostAmountSettledUSDTotal = %v, want 13.5", got)
	}
}

func TestCostHandle_DuplicateAck(t *testing.T) {
	t.Parallel()
	s := &fakeSettler{res: costdomain.SettleResult{Kind: costdomain.SettleDuplicate}}
	h, m := newTestCostHandler(s)
	d, ack := deliveryWith(goodLLMEvent)

	_ = h.Handle(context.Background(), d)
	if !ack.acked {
		t.Errorf("want Ack on duplicate")
	}
	if v := counterVecValue(t, m.CostEventsSettledTotal, "llm", "duplicate"); v != 1 {
		t.Errorf("settled{llm,duplicate} = %v, want 1", v)
	}
}

func TestCostHandle_MissingPricingAckWithCounter(t *testing.T) {
	t.Parallel()
	s := &fakeSettler{res: costdomain.SettleResult{Kind: costdomain.SettleMissingPricing}}
	h, m := newTestCostHandler(s)
	d, ack := deliveryWith(goodLLMEvent)

	_ = h.Handle(context.Background(), d)
	if !ack.acked {
		t.Errorf("want Ack on missing pricing (data is preserved)")
	}
	if v := counterVecValue(t, m.CostEventsSettledTotal, "llm", "missing_pricing"); v != 1 {
		t.Errorf("settled{llm,missing_pricing} = %v, want 1", v)
	}
	if v := counterVecValue(t, m.CostPricingMissingTotal, "llm", "claude-opus-4-7"); v != 1 {
		t.Errorf("pricing_missing{llm,claude-opus-4-7} = %v, want 1", v)
	}
}

func TestCostHandle_TaskIDMismatchDLQ(t *testing.T) {
	t.Parallel()
	s := &fakeSettler{res: costdomain.SettleResult{Kind: costdomain.SettleErrorMismatch}}
	h, m := newTestCostHandler(s)
	d, ack := deliveryWith(goodLLMEvent)

	_ = h.Handle(context.Background(), d)
	if !ack.nacked || ack.nackRequeue {
		t.Errorf("want nack(requeue=false); got nacked=%v requeue=%v", ack.nacked, ack.nackRequeue)
	}
	if v := counterVecValue(t, m.CostEventsSettledTotal, "llm", "error"); v != 1 {
		t.Errorf("settled{llm,error} = %v, want 1", v)
	}
}

func TestCostHandle_TransientErrorRequeue(t *testing.T) {
	t.Parallel()
	transient := &pgconn.PgError{Code: "40001"} // serialization_failure
	s := &fakeSettler{err: transient}
	h, _ := newTestCostHandler(s)
	d, ack := deliveryWith(goodLLMEvent)

	_ = h.Handle(context.Background(), d)
	if !ack.nacked || !ack.nackRequeue {
		t.Errorf("want nack(requeue=true) for transient; got nacked=%v requeue=%v", ack.nacked, ack.nackRequeue)
	}
}

func TestCostHandle_PermanentErrorDLQ(t *testing.T) {
	t.Parallel()
	permanent := errors.New("some unrecognised failure")
	s := &fakeSettler{err: permanent}
	h, m := newTestCostHandler(s)
	d, ack := deliveryWith(goodLLMEvent)

	_ = h.Handle(context.Background(), d)
	if !ack.nacked || ack.nackRequeue {
		t.Errorf("want nack(requeue=false) for permanent; got nacked=%v requeue=%v", ack.nacked, ack.nackRequeue)
	}
	if v := counterVecValue(t, m.CostEventsSettledTotal, "llm", "error"); v != 1 {
		t.Errorf("settled{llm,error} = %v, want 1", v)
	}
}

func TestCostHandle_TraceparentBoundOnLog(t *testing.T) {
	// Not asserting log output (the codebase uses io.Discard); the value here
	// is that the code path doesn't panic when the header is set. Smoke test
	// to lock in the contract from spec §"Trace Propagation".
	t.Parallel()
	s := &fakeSettler{res: costdomain.SettleResult{Kind: costdomain.SettleOK}}
	h, _ := newTestCostHandler(s)
	d, _ := deliveryWith(goodLLMEvent)
	d.Headers = amqp.Table{"traceparent": "00-aaaa-bbbb-01"}

	if err := h.Handle(context.Background(), d); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !s.called {
		t.Error("settler must be called")
	}
}
