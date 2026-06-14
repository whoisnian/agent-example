package task

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
)

// DeleteService runs the task soft-delete transaction (add-task-deletion).
// Soft delete only stamps tasks.deleted_at; it never deletes task_versions /
// task_runs / artifacts / cost_events rows (those are retained for audit and
// cost-settlement integrity). The operation does not cross a service boundary
// (no MQ / outbox): a single owner-scoped task-table write.
type DeleteService struct {
	Pool    *pgxpool.Pool
	Queries *sqlc.Queries
}

// NewDeleteService constructs the domain delete service.
func NewDeleteService(pool *pgxpool.Pool, q *sqlc.Queries) *DeleteService {
	return &DeleteService{Pool: pool, Queries: q}
}

// SoftDelete soft-deletes a task the owner owns. Returns:
//   - nil: the task was live and is now soft-deleted.
//   - ErrTaskNotFound: unknown, unowned, or already soft-deleted task — the
//     HTTP layer 404s identically for all three (no existence leak; idempotent).
//   - *ErrActiveVersionExists: the task has an active version; the HTTP layer
//     409s with the same conflict shape as iterate/rollback (delete the active
//     run first via cancel).
//   - otherErr: wrapped DB / commit error.
//
// The owner check and the active-version guard reuse the established control
// idiom (LockTaskForControl → IsActive → GetActiveVersionByTask), all inside
// one transaction so the active check and the soft-delete write are atomic.
func (s *DeleteService) SoftDelete(ctx context.Context, owner Owner, taskID uuid.UUID) error {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.Queries.WithTx(tx)

	// 1. lock + owner check (no rows → unknown/unowned → 404). Note: this lock
	// does NOT filter deleted_at, so an already-deleted row still locks here;
	// the guarded SoftDeleteTask exec below makes the repeat case idempotent.
	locked, err := q.LockTaskForControl(ctx, sqlc.LockTaskForControlParams{
		ID:       toPgUUID(taskID),
		TenantID: toPgUUID(owner.TenantID),
		UserID:   toPgUUID(owner.UserID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTaskNotFound
		}
		return fmt.Errorf("lock task: %w", err)
	}

	// 2. active-version guard (same conflict as iterate/rollback).
	if IsActive(locked.Status) {
		active, qerr := q.GetActiveVersionByTask(ctx, toPgUUID(taskID))
		if qerr != nil {
			return fmt.Errorf("lookup active version: %w", qerr)
		}
		return &ErrActiveVersionExists{
			ActiveVersionID:     fromPgUUID(active.ID),
			ActiveVersionStatus: active.Status,
		}
	}

	// 3. soft-delete (guarded by deleted_at IS NULL → 0 rows means it was
	// already deleted between the lock and now / or the lock saw a deleted row).
	rows, err := q.SoftDeleteTask(ctx, toPgUUID(taskID))
	if err != nil {
		return fmt.Errorf("soft delete task: %w", err)
	}
	if rows == 0 {
		return ErrTaskNotFound
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
