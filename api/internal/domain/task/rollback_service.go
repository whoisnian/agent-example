package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
)

// RollbackMode selects the rollback behaviour (add-task-rollback-api §6.5).
type RollbackMode string

const (
	// RollbackBranch creates a new version whose parent is the target historical
	// version and executes it — identical to iterate with a pinned base, under
	// the task-level mutex.
	RollbackBranch RollbackMode = "branch"
	// RollbackSwitch repoints tasks.current_version at the target version only:
	// no new version, no run, no execute message, and crucially no tasks.status
	// write (event-ingest stays the sole run-driven status writer).
	RollbackSwitch RollbackMode = "switch"
)

// IsValidRollbackMode reports whether s is one of the two accepted modes. The
// HTTP layer is the primary 400 guard; RollbackTask re-asserts it.
func IsValidRollbackMode(s string) bool {
	switch RollbackMode(s) {
	case RollbackBranch, RollbackSwitch:
		return true
	}
	return false
}

// RollbackInput is the per-request input for POST /api/v1/tasks/{id}/rollback.
type RollbackInput struct {
	TaskID          uuid.UUID
	TargetVersionID uuid.UUID
	Mode            RollbackMode
	Prompt          string // branch only; auto-filled when empty
	Params          json.RawMessage
	Lane            *string
}

// RollbackOutput is the response mirror. For branch, VersionID is the newly
// created version; for switch it is the target now pointed at by current_version.
type RollbackOutput struct {
	VersionID uuid.UUID
	VersionNo int32
	Status    Status
	Mode      RollbackMode
}

// RollbackTask owns the rollback transaction end-to-end: owner-scoped lock,
// shared non-active precondition, target validation, then the mode-specific
// write. It reuses createActiveVersion for branch so the mutex / run / execute
// machinery is shared with iterate verbatim.
//
//nolint:gocritic // hugeParam: value semantics intentional for a read-only input command.
func (s *Service) RollbackTask(ctx context.Context, owner Owner, in RollbackInput) (RollbackOutput, error) {
	if !IsValidRollbackMode(string(in.Mode)) {
		return RollbackOutput{}, newInvalidInput("mode", "must be one of branch/switch")
	}

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return RollbackOutput{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.Queries.WithTx(tx)

	// Owner-scoped lock: unknown OR unowned both collapse to ErrTaskNotFound
	// (→404, never 403). The widened query also yields task_type for branch.
	locked, err := q.LockTaskForControl(ctx, sqlc.LockTaskForControlParams{
		ID:       toPgUUID(in.TaskID),
		TenantID: toPgUUID(owner.TenantID),
		UserID:   toPgUUID(owner.UserID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RollbackOutput{}, ErrTaskNotFound
		}
		return RollbackOutput{}, fmt.Errorf("lock task: %w", err)
	}

	// Shared non-active precondition (both modes). For branch this is the mutex
	// fast-path; for switch it prevents moving current_version out from under a
	// run whose status-sync CAS is gated on it.
	if IsActive(locked.Status) {
		active, qerr := q.GetActiveVersionByTask(ctx, toPgUUID(in.TaskID))
		if qerr != nil {
			return RollbackOutput{}, fmt.Errorf("lookup active version: %w", qerr)
		}
		return RollbackOutput{}, &ErrActiveVersionExists{
			ActiveVersionID:     fromPgUUID(active.ID),
			ActiveVersionStatus: active.Status,
		}
	}

	// Target must belong to the path task (else version_not_found → 404).
	target, err := q.GetVersionByTaskAndID(ctx, sqlc.GetVersionByTaskAndIDParams{
		ID:     toPgUUID(in.TargetVersionID),
		TaskID: toPgUUID(in.TaskID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RollbackOutput{}, ErrVersionNotFound
		}
		return RollbackOutput{}, fmt.Errorf("lookup target version: %w", err)
	}

	if in.Mode == RollbackSwitch {
		return s.rollbackSwitch(ctx, tx, q, in.TaskID, target, locked.Status)
	}
	return s.rollbackBranch(ctx, tx, q, locked, target, in)
}

// rollbackSwitch repoints current_version at a terminal target and commits.
//
//nolint:gocritic // hugeParam: the fetched target row is passed by value intentionally.
func (s *Service) rollbackSwitch(
	ctx context.Context,
	tx pgx.Tx,
	q *sqlc.Queries,
	taskID uuid.UUID,
	target sqlc.TaskVersion,
	taskStatus string,
) (RollbackOutput, error) {
	// Explicit terminal assertion via the DB-computed is_active column — do not
	// rely on the non-active precondition to imply target terminality (a
	// `cancelling` version is non-terminal yet maps to no tasks.status write).
	if target.IsActive != nil && *target.IsActive {
		return RollbackOutput{}, fmt.Errorf("%w: cannot switch to a non-terminal version", ErrInvalidState)
	}

	if err := q.SwitchTaskCurrentVersion(ctx, sqlc.SwitchTaskCurrentVersionParams{
		ID:             toPgUUID(taskID),
		CurrentVersion: target.ID,
	}); err != nil {
		return RollbackOutput{}, fmt.Errorf("switch current_version: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RollbackOutput{}, fmt.Errorf("commit: %w", err)
	}
	return RollbackOutput{
		VersionID: fromPgUUID(target.ID),
		VersionNo: target.VersionNo,
		Status:    Status(taskStatus), // unchanged; taken from the locked row
		Mode:      RollbackSwitch,
	}, nil
}

// rollbackBranch creates+executes a new version parented on the target.
//
//nolint:gocritic // hugeParam: value semantics intentional for a read-only input command.
func (s *Service) rollbackBranch(
	ctx context.Context,
	tx pgx.Tx,
	q *sqlc.Queries,
	locked sqlc.LockTaskForControlRow,
	target sqlc.TaskVersion,
	in RollbackInput,
) (RollbackOutput, error) {
	// Auto-fill the prompt BEFORE validation so an omitted prompt is accepted.
	prompt := in.Prompt
	if prompt == "" {
		prompt = fmt.Sprintf("rollback to version %d", target.VersionNo)
	}
	prompt, err := validatePrompt(prompt)
	if err != nil {
		return RollbackOutput{}, err
	}
	paramsJSON, err := validateParams(in.Params)
	if err != nil {
		return RollbackOutput{}, err
	}
	lane, err := resolveLane(in.Lane, s.DefaultLane)
	if err != nil {
		return RollbackOutput{}, err
	}

	maxNo, err := q.MaxVersionNoForTask(ctx, toPgUUID(in.TaskID))
	if err != nil {
		return RollbackOutput{}, fmt.Errorf("max version_no: %w", err)
	}

	targetID := fromPgUUID(target.ID)
	out, err := s.createActiveVersion(ctx, tx, q, activeVersionParams{
		taskID:             in.TaskID,
		taskType:           locked.TaskType, // from the owner-scoped lock, not an unscoped re-read
		prompt:             prompt,
		paramsJSON:         paramsJSON,
		lane:               lane,
		parentVersionID:    &targetID,
		parentArtifactRoot: target.ArtifactRoot,
		versionNo:          maxNo + 1,
	}, nil)
	if err != nil {
		return RollbackOutput{}, err
	}

	if err := q.UpdateTaskCurrentVersion(ctx, sqlc.UpdateTaskCurrentVersionParams{
		ID:             toPgUUID(in.TaskID),
		CurrentVersion: toPgUUID(out.versionID),
	}); err != nil {
		return RollbackOutput{}, fmt.Errorf("update tasks.current_version: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RollbackOutput{}, fmt.Errorf("commit: %w", err)
	}
	return RollbackOutput{
		VersionID: out.versionID,
		VersionNo: out.versionNo,
		Status:    StatusPending,
		Mode:      RollbackBranch,
	}, nil
}
