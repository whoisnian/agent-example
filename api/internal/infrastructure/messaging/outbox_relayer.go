package messaging

import (
	"context"
	"log/slog"
	"math"
	"math/rand"
	"sync/atomic"
	"time"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence"
)

// RelayerConfig captures the tuning knobs for the Outbox Relayer.
type RelayerConfig struct {
	TickInterval time.Duration
	BatchSize    int
	MaxAttempts  int
	LockID       int64 // pg_try_advisory_lock argument; one per database
}

// Relayer is the singleton-per-database background worker that drains the
// outbox table to RabbitMQ via the Publisher abstraction. Exactly one Relayer
// per database is enforced via PostgreSQL session-level advisory locks
// (design D8). Multiple API replicas can race for the lock cheaply at each
// tick; the loser logs at debug and tries again next tick.
type Relayer struct {
	cfg       RelayerConfig
	store     persistence.OutboxStore
	publisher Publisher
	logger    *slog.Logger
	metrics   *observability.Metrics

	stopped atomic.Bool
}

// NewRelayer constructs a Relayer. Each outbox row carries its own destination
// exchange in `outbox.exchange` (added by add-task-control-api migration
// 0006_outbox_exchange); the relayer no longer holds an implicit constant.
// The per-row routing key is still taken from outbox.topic.
func NewRelayer(
	cfg RelayerConfig,
	store persistence.OutboxStore,
	pub Publisher,
	logger *slog.Logger,
	m *observability.Metrics,
) *Relayer {
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = time.Second
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 10
	}
	return &Relayer{
		cfg:       cfg,
		store:     store,
		publisher: pub,
		logger:    logger,
		metrics:   m,
	}
}

// Run blocks until ctx is cancelled, ticking the scan/publish loop. Errors
// inside a tick are logged and the loop continues — durability is provided by
// the outbox table itself.
func (r *Relayer) Run(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.TickInterval)
	defer ticker.Stop()

	for {
		if r.stopped.Load() {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.tick(ctx)
		}
	}
}

// Stop signals the run loop to exit at the next iteration.
func (r *Relayer) Stop() {
	r.stopped.Store(true)
}

// tick runs one scan/publish/update cycle, gated by the advisory lock.
func (r *Relayer) tick(ctx context.Context) {
	acquired, err := r.store.TryAdvisoryLock(ctx, r.cfg.LockID)
	if err != nil {
		r.logger.Warn("outbox_lock_acquire_failed", slog.String("err", err.Error()))
		r.metrics.OutboxRelayerLeader.Set(0)
		return
	}
	if !acquired {
		r.metrics.OutboxRelayerLeader.Set(0)
		r.logger.Debug("outbox_lock_held_elsewhere")
		return
	}
	r.metrics.OutboxRelayerLeader.Set(1)
	defer func() {
		if err := r.store.UnlockAdvisory(ctx, r.cfg.LockID); err != nil {
			r.logger.Warn("outbox_lock_release_failed", slog.String("err", err.Error()))
		}
		r.metrics.OutboxRelayerLeader.Set(0)
	}()

	// Refresh gauge before scan.
	if n, err := r.store.CountPending(ctx); err == nil {
		r.metrics.MQOutboxPending.Set(float64(n))
	}

	rows, err := r.store.ScanPending(ctx, r.cfg.BatchSize)
	if err != nil {
		r.logger.Warn("outbox_scan_failed", slog.String("err", err.Error()))
		return
	}

	for i := range rows {
		r.publishRow(ctx, &rows[i])
	}
}

// publishRow handles a single outbox row: publish, then mark sent / retry /
// failed depending on outcome.
func (r *Relayer) publishRow(ctx context.Context, row *persistence.OutboxRow) {
	env := Envelope{
		MsgID:          row.AggregateID.String(),
		IdempotencyKey: row.AggregateID.String(),
		Payload:        row.Payload,
		OccurredAt:     row.CreatedAt,
	}
	err := r.publisher.Publish(ctx, row.Exchange, row.Topic, env)
	if err == nil {
		if uerr := r.store.MarkSent(ctx, row.ID); uerr != nil {
			r.logger.Warn("outbox_mark_sent_failed",
				slog.Int64("outbox_id", row.ID),
				slog.String("err", uerr.Error()),
			)
			return
		}
		r.metrics.MQOutboxPublished.Inc()
		return
	}

	nextAttempts := row.Attempts + 1
	if nextAttempts >= r.cfg.MaxAttempts {
		if uerr := r.store.MarkFailed(ctx, row.ID); uerr != nil {
			r.logger.Warn("outbox_mark_failed_failed",
				slog.Int64("outbox_id", row.ID),
				slog.String("err", uerr.Error()),
			)
			return
		}
		r.metrics.OutboxFailedTotal.Inc()
		r.logger.Warn("outbox_row_failed_permanent",
			slog.Int64("outbox_id", row.ID),
			slog.Int("attempts", nextAttempts),
			slog.String("publish_err", err.Error()),
		)
		return
	}

	next := time.Now().Add(Backoff(nextAttempts))
	if uerr := r.store.IncrementAttempt(ctx, row.ID, next); uerr != nil {
		r.logger.Warn("outbox_increment_attempt_failed",
			slog.Int64("outbox_id", row.ID),
			slog.String("err", uerr.Error()),
		)
	}
}

// Backoff returns the delay before the next retry for the given (already
// incremented) attempt count. Full-jitter exponential, base 2s, cap 5m, per
// api-messaging spec.
//
// For attempts=1 the window is [0, 2s); attempts=2 -> [0, 4s); ...; cap 5m.
func Backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	const base = 2 * time.Second
	const maxBackoff = 5 * time.Minute
	// 2^(attempts-1) * base, capped.
	multiplier := math.Pow(2, float64(attempts-1))
	d := time.Duration(multiplier * float64(base))
	if d <= 0 || d > maxBackoff {
		d = maxBackoff
	}
	// Full jitter: uniform [0, d).
	//nolint:gosec // jitter does not require crypto-grade randomness
	return time.Duration(rand.Int63n(int64(d)) + 1)
}
