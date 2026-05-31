package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
)

// Control action enum (add-task-control-api). The HTTP layer parses the raw
// JSON string into one of these; anything else is 400 invalid_input.
type ControlAction string

const (
	ControlPause  ControlAction = "pause"
	ControlResume ControlAction = "resume"
	ControlCancel ControlAction = "cancel"
)

// IsValidControlAction reports whether `s` is one of the three accepted
// actions. The HTTP layer uses this for the 400 path; the domain layer
// re-asserts it as a programmer-error guard in Apply.
func IsValidControlAction(s string) bool {
	switch ControlAction(s) {
	case ControlPause, ControlResume, ControlCancel:
		return true
	}
	return false
}

// MaxControlReasonLen matches the existing `task.title` validation cap so the
// audit-text field doesn't sprawl. Reviewer S13.
const MaxControlReasonLen = 200

// ControlResult is what ControlService.Apply returns. Fields:
//   - OutboxID:  the inserted outbox row's id (internal; the HTTP handler
//                surfaces this in the access-log only — not in the response
//                body, per reviewer S7).
//   - HasActiveRun: true when a non-null run_id was resolved; the handler
//                uses this to choose `effective ∈ {queued, best_effort}`.
//   - VersionID / RunID: nullable echoes of what was written into the
//                outbox payload (handy for tests and logging).
type ControlResult struct {
	OutboxID     int64
	HasActiveRun bool
	VersionID    *uuid.UUID
	RunID        *uuid.UUID
}

// controlPayload is the JSON written into outbox.payload (spec §"Outbox
// Payload Shape"). Field names match the spec exactly.
type controlPayload struct {
	TaskID    string  `json:"task_id"`
	VersionID *string `json:"version_id"`
	RunID     *string `json:"run_id"`
	Action    string  `json:"action"`
	Reason    string  `json:"reason"`
	IssuedAt  string  `json:"issued_at"`
}

// ControlService runs the control transaction end-to-end:
//   1. Lock the task row with the owner predicate.
//   2. Validate the action against the current tasks.status.
//   3. Resolve the active run (latest attempt; possibly null for pre-claim).
//   4. INSERT one outbox row with exchange=task.control, topic=task.<id>.
//
// All four steps share a single transaction so concurrent control requests
// for the same task serialise on the FOR UPDATE lock and observe each other's
// committed outcomes.
type ControlService struct {
	Pool    *pgxpool.Pool
	Queries *sqlc.Queries
	Clock   Clock
}

// NewControlService constructs the service.
func NewControlService(pool *pgxpool.Pool, q *sqlc.Queries, clock Clock) *ControlService {
	return &ControlService{Pool: pool, Queries: q, Clock: clock}
}

// Apply executes the control request. Returns:
//   - (*ControlResult, nil): accepted (HTTP 202).
//   - (nil, ErrTaskNotFound): unknown OR unowned task — HTTP layer 404s.
//   - (nil, *ErrInvalidInput): only the action / reason validation failure
//     paths that re-assert at the domain boundary (the HTTP layer is the
//     primary guard).
//   - (nil, ErrInvalidState wrapped with the current status): state guard
//     tripped — HTTP layer 409s.
//   - (nil, otherErr): wrapped DB / commit error.
//
//nolint:gocritic // hugeParam: value semantics intentional for a read-only input command.
func (s *ControlService) Apply(ctx context.Context, owner Owner, taskID uuid.UUID, action ControlAction, reason string) (*ControlResult, error) {
	if !IsValidControlAction(string(action)) {
		return nil, newInvalidInput("action", "must be one of pause/resume/cancel")
	}
	reason = strings.TrimRight(reason, " \t\n\r")
	if len(reason) > MaxControlReasonLen {
		return nil, newInvalidInput("reason", fmt.Sprintf("exceeds %d characters", MaxControlReasonLen))
	}

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.Queries.WithTx(tx)

	// 1. lock + owner check
	locked, err := q.LockTaskForControl(ctx, sqlc.LockTaskForControlParams{
		ID:       toPgUUID(taskID),
		TenantID: toPgUUID(owner.TenantID),
		UserID:   toPgUUID(owner.UserID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTaskNotFound
		}
		return nil, fmt.Errorf("lock task: %w", err)
	}

	// 2. state guard
	if err := checkControlPrecondition(action, locked.Status); err != nil {
		return nil, err
	}

	// 3. resolve active run (nullable)
	var runIDPtr *uuid.UUID
	if runRow, err := q.GetActiveRunIDForTask(ctx, toPgUUID(taskID)); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("resolve active run: %w", err)
		}
		// no rows → pre-claim state; runIDPtr stays nil
	} else {
		id := uuid.UUID(runRow.Bytes)
		runIDPtr = &id
	}

	var versionIDPtr *uuid.UUID
	if locked.CurrentVersion.Valid {
		v := uuid.UUID(locked.CurrentVersion.Bytes)
		versionIDPtr = &v
	}

	// 4. build payload + insert outbox
	payload := controlPayload{
		TaskID:   taskID.String(),
		Action:   string(action),
		Reason:   reason,
		IssuedAt: s.Clock.Now().UTC().Format(time.RFC3339Nano),
	}
	if versionIDPtr != nil {
		s := versionIDPtr.String()
		payload.VersionID = &s
	}
	if runIDPtr != nil {
		s := runIDPtr.String()
		payload.RunID = &s
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal control payload: %w", err)
	}

	row, err := q.InsertOutbox(ctx, sqlc.InsertOutboxParams{
		Aggregate:   "task",
		AggregateID: toPgUUID(taskID),
		Topic:       "task." + taskID.String(),
		Payload:     body,
		Exchange:    "task.control",
	})
	if err != nil {
		return nil, fmt.Errorf("insert outbox: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return &ControlResult{
		OutboxID:     row.ID,
		HasActiveRun: runIDPtr != nil,
		VersionID:    versionIDPtr,
		RunID:        runIDPtr,
	}, nil
}

// checkControlPrecondition implements the state-machine guards from spec
// §"State-Machine Preconditions". Reviewer S8 documented in design D13: the
// 409 message includes the current status verbatim so the HTTP layer can
// pass it through.
func checkControlPrecondition(action ControlAction, currentStatus string) error {
	switch action {
	case ControlPause:
		if currentStatus != string(StatusPending) && currentStatus != string(StatusRunning) {
			return fmt.Errorf("%w: cannot pause task in status %q", ErrInvalidState, currentStatus)
		}
	case ControlResume:
		if currentStatus != string(StatusPaused) {
			return fmt.Errorf("%w: cannot resume task in status %q", ErrInvalidState, currentStatus)
		}
	case ControlCancel:
		if currentStatus == string(StatusCancelled) ||
			currentStatus == string(StatusSucceeded) ||
			currentStatus == string(StatusFailed) {
			return fmt.Errorf("%w: cannot cancel task in terminal status %q", ErrInvalidState, currentStatus)
		}
	default:
		// unreachable — Apply re-checks IsValidControlAction first
		return fmt.Errorf("%w: unknown action %q", ErrInvalidState, action)
	}
	return nil
}
