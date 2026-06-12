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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
)

// activeVersionConstraint is the partial-unique index name that enforces the
// task-level mutex; we match on the constraint name so we don't mis-translate
// unrelated SQLSTATE 23505 errors.
const activeVersionConstraint = "one_active_version_per_task"

// Service owns the two write flows defined by add-task-create-api. The struct
// depends on Clock / IDGenerator interfaces so unit tests can drive
// deterministic ids and timestamps.
type Service struct {
	Pool            *pgxpool.Pool
	Queries         *sqlc.Queries
	Clock           Clock
	IDGen           IDGenerator
	DefaultLane     string
	DefaultDeadline time.Duration
}

// NewService is a thin constructor that fills in production defaults when
// callers leave the optional fields blank.
func NewService(pool *pgxpool.Pool, q *sqlc.Queries, clock Clock, idgen IDGenerator,
	defaultLane string, defaultDeadline time.Duration,
) *Service {
	if clock == nil {
		clock = SystemClock{}
	}
	if idgen == nil {
		idgen = UUIDv7Gen{}
	}
	if defaultLane == "" {
		defaultLane = "default"
	}
	if defaultDeadline <= 0 {
		defaultDeadline = 60 * time.Minute
	}
	return &Service{
		Pool:            pool,
		Queries:         q,
		Clock:           clock,
		IDGen:           idgen,
		DefaultLane:     defaultLane,
		DefaultDeadline: defaultDeadline,
	}
}

// CreateInput is the per-request input for POST /api/v1/tasks.
type CreateInput struct {
	TenantID uuid.UUID
	UserID   uuid.UUID
	Title    string
	TaskType string
	Prompt   string
	Params   json.RawMessage // optional; nil → "{}"
	Lane     *string         // optional; nil → DefaultLane
}

// CreateOutput is what the handler relays back to the client envelope.
type CreateOutput struct {
	TaskID    uuid.UUID
	VersionID uuid.UUID
	VersionNo int32
	Status    Status
}

// IterateInput is the per-request input for POST /api/v1/tasks/{id}/iterate.
type IterateInput struct {
	TaskID        uuid.UUID
	BaseVersionID *uuid.UUID // optional; nil → tasks.current_version
	Prompt        string
	Params        json.RawMessage
	Lane          *string
}

// IterateOutput is the response payload mirror. The History* fields are
// assembly observability for the handler's structured log, not response data.
type IterateOutput struct {
	VersionID           uuid.UUID
	VersionNo           int32
	Status              Status
	HistoryTurns        int
	HistoryDroppedSize  int
	HistoryDepthClipped bool
}

// activeVersionParams carries everything `createActiveVersion` needs that
// CreateTask vs IterateTask differ on.
type activeVersionParams struct {
	taskID             uuid.UUID
	taskType           string
	prompt             string
	paramsJSON         []byte
	lane               string
	parentVersionID    *uuid.UUID
	parentArtifactRoot *string
	versionNo          int32
	// genTitle marks the execute payload for worker-side semantic title
	// generation. Only CreateTask's derived-title path sets it (whitelist
	// semantics) — iterate / rollback rely on the zero value.
	genTitle bool
	// history is the assembled conversation history for the execute payload
	// (task-conversation-history). Iterate / rollback-branch set it; create
	// relies on the zero value (nil → field omitted from the payload).
	history []HistoryTurn
}

// CreateTask implements the happy path in design D2 for "no prior task":
// mints fresh ids, INSERTs `tasks` first, then delegates to
// `createActiveVersion` for the version/run/outbox/current_version writes.
//
//nolint:gocritic // hugeParam: value semantics intentional for an input command; the struct is read-only.
func (s *Service) CreateTask(ctx context.Context, in CreateInput) (CreateOutput, error) {
	taskType, err := validateTaskType(in.TaskType)
	if err != nil {
		return CreateOutput{}, err
	}
	// Prompt before title: an absent title is derived from the prompt, so a
	// missing prompt must surface as invalid_input(prompt), not a bad title.
	prompt, err := validatePrompt(in.Prompt)
	if err != nil {
		return CreateOutput{}, err
	}
	var title string
	titleDerived := strings.TrimSpace(in.Title) == ""
	if titleDerived {
		title = deriveTitle(prompt)
	} else if title, err = validateTitle(in.Title); err != nil {
		return CreateOutput{}, err
	}
	paramsJSON, err := validateParams(in.Params)
	if err != nil {
		return CreateOutput{}, err
	}
	lane, err := resolveLane(in.Lane, s.DefaultLane)
	if err != nil {
		return CreateOutput{}, err
	}

	taskID, err := s.IDGen.NewV7()
	if err != nil {
		return CreateOutput{}, fmt.Errorf("idgen task: %w", err)
	}

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CreateOutput{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.Queries.WithTx(tx)

	// We'll point tasks.current_version at the soon-to-be-inserted version.
	// The version id is minted inside createActiveVersion to keep the call
	// site responsible for *all* id-driven side effects, but we need the id
	// up front for the INSERT. Mint it here and pass it through.
	versionID, err := s.IDGen.NewV7()
	if err != nil {
		return CreateOutput{}, fmt.Errorf("idgen version: %w", err)
	}

	if _, err := q.CreateTask(ctx, sqlc.CreateTaskParams{
		ID:             toPgUUID(taskID),
		TenantID:       toPgUUID(in.TenantID),
		UserID:         toPgUUID(in.UserID),
		Title:          title,
		TaskType:       taskType,
		Status:         string(StatusPending),
		CurrentVersion: toPgUUID(versionID),
	}); err != nil {
		return CreateOutput{}, fmt.Errorf("insert tasks: %w", err)
	}

	out, err := s.createActiveVersion(ctx, tx, q, activeVersionParams{
		taskID:             taskID,
		taskType:           taskType,
		prompt:             prompt,
		paramsJSON:         paramsJSON,
		lane:               lane,
		parentVersionID:    nil,
		parentArtifactRoot: nil,
		versionNo:          1,
		genTitle:           titleDerived,
	}, &versionID)
	if err != nil {
		return CreateOutput{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return CreateOutput{}, fmt.Errorf("commit: %w", err)
	}
	return CreateOutput{
		TaskID:    taskID,
		VersionID: out.versionID,
		VersionNo: out.versionNo,
		Status:    StatusPending,
	}, nil
}

// IterateTask implements design D2 in full: lock the tasks row, app-level
// pre-check, base resolution, savepoint-wrapped INSERT, and finally
// UpdateTaskCurrentVersion.
func (s *Service) IterateTask(ctx context.Context, in IterateInput) (IterateOutput, error) {
	prompt, err := validatePrompt(in.Prompt)
	if err != nil {
		return IterateOutput{}, err
	}
	paramsJSON, err := validateParams(in.Params)
	if err != nil {
		return IterateOutput{}, err
	}
	lane, err := resolveLane(in.Lane, s.DefaultLane)
	if err != nil {
		return IterateOutput{}, err
	}

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return IterateOutput{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.Queries.WithTx(tx)

	// Step 2: FOR UPDATE on the tasks row.
	taskRow, err := q.LockTaskRow(ctx, toPgUUID(in.TaskID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return IterateOutput{}, ErrTaskNotFound
		}
		return IterateOutput{}, fmt.Errorf("lock task: %w", err)
	}

	// Step 3: app-level fast-path mutex pre-check.
	if IsActive(taskRow.Status) {
		active, qerr := q.GetActiveVersionByTask(ctx, toPgUUID(in.TaskID))
		if qerr != nil {
			// If something is in an active set but the row vanished, fall
			// through to the SQLSTATE path on insert — that's the resilient
			// answer rather than guessing.
			return IterateOutput{}, fmt.Errorf("lookup active version: %w", qerr)
		}
		return IterateOutput{}, &ErrActiveVersionExists{
			ActiveVersionID:     fromPgUUID(active.ID),
			ActiveVersionStatus: active.Status,
		}
	}

	// Step 4: resolve base version.
	baseID, baseArtifactRoot, err := s.resolveBase(ctx, q, in.TaskID, in.BaseVersionID, taskRow.CurrentVersion)
	if err != nil {
		return IterateOutput{}, err
	}

	// Determine the new version_no.
	maxNo, err := q.MaxVersionNoForTask(ctx, toPgUUID(in.TaskID))
	if err != nil {
		return IterateOutput{}, fmt.Errorf("max version_no: %w", err)
	}
	newVersionNo := maxNo + 1

	taskRowMeta, err := q.GetTaskByID(ctx, toPgUUID(in.TaskID))
	if err != nil {
		return IterateOutput{}, fmt.Errorf("re-read task: %w", err)
	}

	// Conversation history rides the execute payload (task-conversation-history):
	// assembled from the base's parent chain inside this tx, frozen into outbox.
	history, historyStats, err := assembleHistory(ctx, q, baseID)
	if err != nil {
		return IterateOutput{}, err
	}

	out, err := s.createActiveVersion(ctx, tx, q, activeVersionParams{
		taskID:             in.TaskID,
		taskType:           taskRowMeta.TaskType,
		prompt:             prompt,
		paramsJSON:         paramsJSON,
		lane:               lane,
		parentVersionID:    &baseID,
		parentArtifactRoot: baseArtifactRoot,
		versionNo:          newVersionNo,
		history:            history,
	}, nil)
	if err != nil {
		return IterateOutput{}, err
	}

	if err := q.UpdateTaskCurrentVersion(ctx, sqlc.UpdateTaskCurrentVersionParams{
		ID:             toPgUUID(in.TaskID),
		CurrentVersion: toPgUUID(out.versionID),
	}); err != nil {
		return IterateOutput{}, fmt.Errorf("update tasks.current_version: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return IterateOutput{}, fmt.Errorf("commit: %w", err)
	}
	return IterateOutput{
		VersionID:           out.versionID,
		VersionNo:           out.versionNo,
		Status:              StatusPending,
		HistoryTurns:        historyStats.Turns,
		HistoryDroppedSize:  historyStats.DroppedSize,
		HistoryDepthClipped: historyStats.DroppedDepth > 0,
	}, nil
}

// resolveBase returns (baseID, parentArtifactRoot, err) given the request's
// optional `base_version_id` and the task's `current_version` from the
// locked row.
func (s *Service) resolveBase(
	ctx context.Context,
	q *sqlc.Queries,
	taskID uuid.UUID,
	explicit *uuid.UUID,
	currentVersion pgtype.UUID,
) (uuid.UUID, *string, error) {
	if explicit != nil {
		row, err := q.GetVersionByTaskAndID(ctx, sqlc.GetVersionByTaskAndIDParams{
			ID:     toPgUUID(*explicit),
			TaskID: toPgUUID(taskID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return uuid.Nil, nil, ErrVersionNotFound
			}
			return uuid.Nil, nil, fmt.Errorf("lookup base version: %w", err)
		}
		return fromPgUUID(row.ID), row.ArtifactRoot, nil
	}
	if !currentVersion.Valid {
		return uuid.Nil, nil, ErrVersionNotFound
	}
	row, err := q.GetTaskVersionByID(ctx, currentVersion)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, nil, ErrVersionNotFound
		}
		return uuid.Nil, nil, fmt.Errorf("lookup current version: %w", err)
	}
	return fromPgUUID(row.ID), row.ArtifactRoot, nil
}

// activeVersionResult captures the ids minted inside the shared helper so the
// caller can compose the response envelope.
type activeVersionResult struct {
	versionID uuid.UUID
	versionNo int32
	runID     uuid.UUID
}

// createActiveVersion performs the savepoint-wrapped INSERT INTO task_versions
// plus the matching task_runs and outbox rows. The caller passes an
// already-open `pgx.Tx` and the `*sqlc.Queries` bound to it.
//
// `preMintedVersionID` is non-nil for the create-task flow (where we needed
// the id earlier to point `tasks.current_version` at it). For iterate the
// id is minted here.
//nolint:gocritic // hugeParam: value semantics intentional for an internal carrier struct; pointer would obscure ownership.
func (s *Service) createActiveVersion(
	ctx context.Context,
	tx pgx.Tx,
	q *sqlc.Queries,
	p activeVersionParams,
	preMintedVersionID *uuid.UUID,
) (activeVersionResult, error) {
	var versionID uuid.UUID
	if preMintedVersionID != nil {
		versionID = *preMintedVersionID
	} else {
		id, err := s.IDGen.NewV7()
		if err != nil {
			return activeVersionResult{}, fmt.Errorf("idgen version: %w", err)
		}
		versionID = id
	}
	runID, err := s.IDGen.NewV7()
	if err != nil {
		return activeVersionResult{}, fmt.Errorf("idgen run: %w", err)
	}
	msgID, err := s.IDGen.NewV7()
	if err != nil {
		return activeVersionResult{}, fmt.Errorf("idgen msg: %w", err)
	}

	// Savepoint covers the unique-violation. Cleanup is unconditional: on
	// success we RELEASE it so the parent tx keeps its lock semantics; on
	// failure we ROLLBACK TO SAVEPOINT and the parent tx stays usable for
	// the active-version lookup.
	if _, err := tx.Exec(ctx, "SAVEPOINT sp_insert_version"); err != nil {
		return activeVersionResult{}, fmt.Errorf("savepoint: %w", err)
	}
	var parentParam pgtype.UUID
	if p.parentVersionID != nil {
		parentParam = toPgUUID(*p.parentVersionID)
	}
	_, insertErr := q.CreateTaskVersion(ctx, sqlc.CreateTaskVersionParams{
		ID:           toPgUUID(versionID),
		TaskID:       toPgUUID(p.taskID),
		ParentID:     parentParam,
		VersionNo:    p.versionNo,
		Prompt:       p.prompt,
		Params:       p.paramsJSON,
		Status:       string(StatusPending),
		ArtifactRoot: nil,
	})
	if insertErr != nil {
		var pgErr *pgconn.PgError
		if errors.As(insertErr, &pgErr) &&
			pgErr.Code == "23505" &&
			pgErr.ConstraintName == activeVersionConstraint {
			// Release the savepoint so the parent tx can continue.
			if _, rbErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT sp_insert_version"); rbErr != nil {
				return activeVersionResult{}, fmt.Errorf("rollback to savepoint: %w", rbErr)
			}
			active, qerr := q.GetActiveVersionByTask(ctx, toPgUUID(p.taskID))
			if qerr != nil {
				return activeVersionResult{}, fmt.Errorf("lookup active after 23505: %w", qerr)
			}
			return activeVersionResult{}, &ErrActiveVersionExists{
				ActiveVersionID:     fromPgUUID(active.ID),
				ActiveVersionStatus: active.Status,
			}
		}
		return activeVersionResult{}, fmt.Errorf("insert task_version: %w", insertErr)
	}
	if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT sp_insert_version"); err != nil {
		return activeVersionResult{}, fmt.Errorf("release savepoint: %w", err)
	}

	if _, err := q.CreateTaskRun(ctx, sqlc.CreateTaskRunParams{
		ID:             toPgUUID(runID),
		VersionID:      toPgUUID(versionID),
		AttemptNo:      1,
		WorkerRunID:    pgtype.UUID{},
		Status:         string(StatusQueued),
		IdempotencyKey: runID.String(),
	}); err != nil {
		return activeVersionResult{}, fmt.Errorf("insert task_run: %w", err)
	}

	payload, err := buildExecutePayload(
		msgID, p.taskID, versionID, runID,
		p.taskType, p.prompt, p.lane, p.paramsJSON,
		p.parentVersionID, p.parentArtifactRoot,
		s.Clock.Now(), s.DefaultDeadline,
		p.genTitle,
		p.history,
	)
	if err != nil {
		return activeVersionResult{}, fmt.Errorf("build payload: %w", err)
	}

	topic := "execute." + p.taskType + "." + p.lane
	if _, err := q.InsertOutbox(ctx, sqlc.InsertOutboxParams{
		Aggregate:   "task_version",
		AggregateID: toPgUUID(versionID),
		Topic:       topic,
		Payload:     payload,
		Exchange:    "task.exchange", // execute messages always route to the task exchange
	}); err != nil {
		return activeVersionResult{}, fmt.Errorf("insert outbox: %w", err)
	}

	return activeVersionResult{versionID: versionID, versionNo: p.versionNo, runID: runID}, nil
}

// toPgUUID converts a stdlib uuid.UUID into the pgtype wrapper sqlc expects.
func toPgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

// fromPgUUID reads a pgtype.UUID back as a stdlib uuid.UUID. Invalid (NULL)
// inputs yield the zero UUID.
func fromPgUUID(u pgtype.UUID) uuid.UUID {
	if !u.Valid {
		return uuid.Nil
	}
	return u.Bytes
}
