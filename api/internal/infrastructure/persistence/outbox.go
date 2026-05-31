package persistence

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// OutboxRow mirrors the `outbox` table. Used by the Relayer's scan/update loop.
//
// Per api-persistence spec, raw pgx access is permitted ONLY in the relayer
// (this file) and the matching test fixtures. Everywhere else must go through
// sqlc-generated code (see internal/infrastructure/persistence/sqlc/).
//
// `Exchange` was added by add-task-control-api migration 0006_outbox_exchange
// so the relayer can publish each row to its destination exchange (per-row,
// not per-instance). Reviewer S3 — this file is HAND-WRITTEN, not sqlc-
// generated; the field must be added in three places: struct, SELECT list
// in ScanPending, and the rows.Scan call.
type OutboxRow struct {
	ID           int64
	Aggregate    string
	AggregateID  uuid.UUID
	Topic        string
	Payload      json.RawMessage
	Status       string
	Attempts     int
	NextRetryAt  *time.Time
	CreatedAt    time.Time
	Exchange     string
}

// OutboxStore is the relayer's persistence interface over outbox rows.
type OutboxStore interface {
	TryAdvisoryLock(ctx context.Context, lockID int64) (bool, error)
	UnlockAdvisory(ctx context.Context, lockID int64) error
	ScanPending(ctx context.Context, limit int) ([]OutboxRow, error)
	MarkSent(ctx context.Context, id int64) error
	IncrementAttempt(ctx context.Context, id int64, nextRetry time.Time) error
	MarkFailed(ctx context.Context, id int64) error
	CountPending(ctx context.Context) (int64, error)
}

// PgxOutboxStore implements OutboxStore on top of pgxpool.
type PgxOutboxStore struct {
	pool *Pool
}

// NewOutboxStore builds an OutboxStore over the given pool.
func NewOutboxStore(pool *Pool) *PgxOutboxStore {
	return &PgxOutboxStore{pool: pool}
}

// TryAdvisoryLock attempts a non-blocking session-level advisory lock. Result
// true means we acquired it; false means another session holds it.
func (s *PgxOutboxStore) TryAdvisoryLock(ctx context.Context, lockID int64) (bool, error) {
	var acquired bool
	if err := s.pool.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", lockID).Scan(&acquired); err != nil {
		return false, err
	}
	return acquired, nil
}

// UnlockAdvisory releases the lock acquired by TryAdvisoryLock. Logging
// "false" return is not necessary; the lock auto-releases on connection drop.
func (s *PgxOutboxStore) UnlockAdvisory(ctx context.Context, lockID int64) error {
	_, err := s.pool.Exec(ctx, "SELECT pg_advisory_unlock($1)", lockID)
	return err
}

// ScanPending fetches up to `limit` pending rows whose retry window has elapsed.
func (s *PgxOutboxStore) ScanPending(ctx context.Context, limit int) ([]OutboxRow, error) {
	const q = `
		SELECT id, aggregate, aggregate_id, topic, payload, status, attempts, next_retry_at, created_at, exchange
		FROM outbox
		WHERE status = 'pending'
		  AND (next_retry_at IS NULL OR next_retry_at <= now())
		ORDER BY id
		LIMIT $1`
	rows, err := s.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []OutboxRow
	for rows.Next() {
		var r OutboxRow
		if err := rows.Scan(&r.ID, &r.Aggregate, &r.AggregateID, &r.Topic, &r.Payload, &r.Status, &r.Attempts, &r.NextRetryAt, &r.CreatedAt, &r.Exchange); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkSent flips a row to status='sent' and bumps attempts.
func (s *PgxOutboxStore) MarkSent(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE outbox SET status='sent', attempts=attempts+1 WHERE id=$1`, id)
	return err
}

// IncrementAttempt bumps attempts and schedules the next retry.
func (s *PgxOutboxStore) IncrementAttempt(ctx context.Context, id int64, nextRetry time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE outbox SET attempts=attempts+1, next_retry_at=$2 WHERE id=$1`, id, nextRetry)
	return err
}

// MarkFailed flips a row to status='failed' after exhausting retries.
func (s *PgxOutboxStore) MarkFailed(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE outbox SET status='failed', attempts=attempts+1 WHERE id=$1`, id)
	return err
}

// CountPending returns the current count of pending rows for metrics.
func (s *PgxOutboxStore) CountPending(ctx context.Context) (int64, error) {
	var n int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE status='pending'`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// silence unused import warnings for pgx when this file is consulted alone.
var _ = pgx.ErrNoRows
