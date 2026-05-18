package messaging

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence"
)

// fakeStore is an in-memory OutboxStore used to test the Relayer state machine
// without a live PostgreSQL. It is intentionally simple — we only care about
// transitions, not concurrency.
type fakeStore struct {
	mu        sync.Mutex
	rows      []persistence.OutboxRow
	locked    bool
	scanCalls int
	marks     map[int64]string // id -> "sent"|"failed"|"retry"
	nextRetry map[int64]time.Time
}

func newFakeStore(rows []persistence.OutboxRow) *fakeStore {
	return &fakeStore{rows: rows, marks: map[int64]string{}, nextRetry: map[int64]time.Time{}}
}

func (s *fakeStore) TryAdvisoryLock(_ context.Context, _ int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locked {
		return false, nil
	}
	s.locked = true
	return true, nil
}

func (s *fakeStore) UnlockAdvisory(_ context.Context, _ int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.locked = false
	return nil
}

func (s *fakeStore) ScanPending(_ context.Context, limit int) ([]persistence.OutboxRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scanCalls++
	pending := make([]persistence.OutboxRow, 0)
	for i := range s.rows {
		if s.rows[i].Status != "pending" {
			continue
		}
		pending = append(pending, s.rows[i])
		if len(pending) == limit {
			break
		}
	}
	return pending, nil
}

func (s *fakeStore) MarkSent(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.rows {
		if s.rows[i].ID == id {
			s.rows[i].Status = "sent"
			s.rows[i].Attempts++
			s.marks[id] = "sent"
			return nil
		}
	}
	return errors.New("not found")
}

func (s *fakeStore) IncrementAttempt(_ context.Context, id int64, t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.rows {
		if s.rows[i].ID != id {
			continue
		}
		s.rows[i].Attempts++
		s.rows[i].NextRetryAt = &t
		s.marks[id] = "retry"
		s.nextRetry[id] = t
		return nil
	}
	return errors.New("not found")
}

func (s *fakeStore) MarkFailed(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.rows {
		if s.rows[i].ID == id {
			s.rows[i].Status = "failed"
			s.rows[i].Attempts++
			s.marks[id] = "failed"
			return nil
		}
	}
	return errors.New("not found")
}

func (s *fakeStore) CountPending(_ context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for i := range s.rows {
		if s.rows[i].Status == "pending" {
			n++
		}
	}
	return n, nil
}

// fakePublisher records calls and replays a queue of canned outcomes.
type fakePublisher struct {
	mu      sync.Mutex
	results []error // per call, in order; runs in cycle when exhausted
	calls   []Envelope
}

func (p *fakePublisher) Publish(_ context.Context, _, _ string, env Envelope) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, env)
	if len(p.results) == 0 {
		return nil
	}
	r := p.results[0]
	p.results = p.results[1:]
	return r
}

func (p *fakePublisher) Close() error { return nil }

func newTestRelayer(t *testing.T, store persistence.OutboxStore, pub Publisher, maxAttempts int) (*Relayer, *observability.Metrics) {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	m := observability.NewMetrics()
	r := NewRelayer(RelayerConfig{
		TickInterval: 50 * time.Millisecond,
		BatchSize:    10,
		MaxAttempts:  maxAttempts,
		LockID:       1,
	}, store, pub, logger, m)
	return r, m
}

func TestRelayer_PublishesPendingRow_MarksSent(t *testing.T) {
	row := persistence.OutboxRow{
		ID:          1,
		Aggregate:   "task",
		AggregateID: uuid.New(),
		Topic:       "task.created",
		Payload:     []byte(`{}`),
		Status:      "pending",
		CreatedAt:   time.Now(),
	}
	store := newFakeStore([]persistence.OutboxRow{row})
	pub := &fakePublisher{}
	r, _ := newTestRelayer(t, store, pub, 10)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.tick(ctx)

	if got := store.marks[1]; got != "sent" {
		t.Errorf("row 1 mark = %q, want sent", got)
	}
	if len(pub.calls) != 1 {
		t.Errorf("publisher calls = %d, want 1", len(pub.calls))
	}
}

func TestRelayer_FailedPublish_BacksOff(t *testing.T) {
	row := persistence.OutboxRow{
		ID: 1, Aggregate: "task", AggregateID: uuid.New(),
		Topic: "task.created", Payload: []byte(`{}`), Status: "pending",
		Attempts: 2, CreatedAt: time.Now(),
	}
	store := newFakeStore([]persistence.OutboxRow{row})
	pub := &fakePublisher{results: []error{errors.New("nack")}}
	r, _ := newTestRelayer(t, store, pub, 10)

	r.tick(context.Background())

	if got := store.marks[1]; got != "retry" {
		t.Errorf("row 1 mark = %q, want retry", got)
	}
	nr, ok := store.nextRetry[1]
	if !ok {
		t.Fatal("expected next_retry_at to be set")
	}
	// attempts now 3 -> base 2s * 2^2 = 8s; jitter window [0, 8s)
	if d := time.Until(nr); d < 0 || d > 8*time.Second {
		t.Errorf("next_retry_at delta = %s, expected within [0, 8s)", d)
	}
}

func TestRelayer_MaxAttemptsMovesToFailed(t *testing.T) {
	row := persistence.OutboxRow{
		ID: 1, Aggregate: "task", AggregateID: uuid.New(),
		Topic: "task.created", Payload: []byte(`{}`), Status: "pending",
		Attempts: 9, CreatedAt: time.Now(),
	}
	store := newFakeStore([]persistence.OutboxRow{row})
	pub := &fakePublisher{results: []error{errors.New("nack")}}
	r, m := newTestRelayer(t, store, pub, 10)

	r.tick(context.Background())

	if got := store.marks[1]; got != "failed" {
		t.Errorf("row 1 mark = %q, want failed", got)
	}
	// `outbox_failed_total` should have ticked.
	count := testCounter(t, m.OutboxFailedTotal)
	if count != 1 {
		t.Errorf("outbox_failed_total = %d, want 1", count)
	}
}

func TestRelayer_SecondInstance_SkipsWhenLockHeld(t *testing.T) {
	store := newFakeStore(nil)
	store.locked = true // simulate lock held by another instance

	pub := &fakePublisher{}
	r, _ := newTestRelayer(t, store, pub, 10)

	r.tick(context.Background())

	// Lock acquisition returned false; scan should never have been called.
	if store.scanCalls != 0 {
		t.Errorf("scanCalls = %d, want 0 (lock contention)", store.scanCalls)
	}
}

func TestBackoff_RespectsCap(t *testing.T) {
	// attempts=20 -> 2^19 * 2s ~= 1.2M seconds; must be clamped to 5m.
	d := Backoff(20)
	if d > 5*time.Minute {
		t.Errorf("backoff %s > 5m cap", d)
	}
}
