package task

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
)

// IngestEventInput is the decoded worker `TaskEvent` envelope handed to the
// domain by the messaging layer. Ids arrive as strings (worker JSON) and are
// parsed here; `Payload` is the worker's exact bytes, stored verbatim.
type IngestEventInput struct {
	TaskID    uuid.UUID
	VersionID uuid.UUID
	RunID     uuid.UUID
	Seq       int64
	Kind      string
	Payload   json.RawMessage
}

// statusPayload is the minimal shape the ingest path reads from a
// `kind=status` event body. Other kinds (`error`, `plan`, `step`) never read
// it — `error` is a failure trigger by kind alone, the rest are persist-only.
type statusPayload struct {
	Status string `json:"status"`
}

// titlePayload is the shape of a `kind=title` event body (add-semantic-task-title).
type titlePayload struct {
	Title string `json:"title"`
}

// summaryPayload is the shape of a `kind=summary` event body
// (refactor-task-conversation-continuity).
type summaryPayload struct {
	Summary string `json:"summary"`
}

// IngestEvent persists one worker event and, for `status`/`error` events,
// drives the version + task state machine in the same transaction
// (add-event-ingest-status-sync). It is the authoritative writer of
// task_versions.status and tasks.status; task_runs.status stays worker-owned.
//
// The whole thing is one tx so "event recorded" and "state advanced" are
// atomic. Persistence is idempotent on (run_id, seq); every status write is a
// guarded CAS (terminal guard + IS DISTINCT FROM), so a redelivered or
// out-of-order event re-runs to a no-op. `transitioned` reports whether a CAS
// actually moved a row, so the caller can keep the transition metric honest.
//
//nolint:gocritic // hugeParam: value semantics intentional for a read-only input command.
func (s *Service) IngestEvent(ctx context.Context, in IngestEventInput) (transitioned bool, err error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.Queries.WithTx(tx)

	inserted, err := q.InsertTaskEvent(ctx, sqlc.InsertTaskEventParams{
		TaskID:    toPgUUID(in.TaskID),
		VersionID: toPgUUID(in.VersionID),
		RunID:     toPgUUID(in.RunID),
		Seq:       in.Seq,
		Kind:      in.Kind,
		Payload:   []byte(in.Payload),
	})
	if err != nil {
		return false, fmt.Errorf("insert task_event: %w", err)
	}

	// `kind=title` updates tasks.title in the same tx (add-semantic-task-title).
	// A duplicate delivery (inserted == 0) must not re-apply; a fresh event is
	// last-write-wins with no terminal guard. Not a state-machine transition.
	if in.Kind == "title" {
		applied := false
		if inserted > 0 {
			var p titlePayload
			// Malformed/absent payload.title → sanitizer yields "" → skipped;
			// the event row above is still committed.
			_ = json.Unmarshal(in.Payload, &p)
			applied, err = s.ApplyGeneratedTitle(ctx, q, in.TaskID, p.Title)
			if err != nil {
				return false, err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("commit: %w", err)
		}
		return applied, nil
	}

	// `kind=summary` updates task_versions.summary in the same tx
	// (refactor-task-conversation-continuity). Same discipline as title: a
	// duplicate delivery (inserted == 0) must not re-apply; a fresh event is
	// last-write-wins with no terminal guard (the summary event races the
	// trailing status event at run end). Not a state-machine transition.
	if in.Kind == "summary" {
		applied := false
		if inserted > 0 {
			var p summaryPayload
			// Malformed/absent payload.summary → sanitizer yields "" →
			// skipped; the event row above is still committed.
			_ = json.Unmarshal(in.Payload, &p)
			applied, err = s.ApplyVersionSummary(ctx, q, in.VersionID, p.Summary)
			if err != nil {
				return false, err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("commit: %w", err)
		}
		return applied, nil
	}

	// Resolve the version status this event drives, if any. Branch on kind
	// first: error → failed; status → payload.status; else persist-only.
	var payloadStatus string
	if in.Kind == "status" {
		var p statusPayload
		// A malformed status payload is treated as "no recognised status":
		// the event is still persisted, just no transition applied.
		_ = json.Unmarshal(in.Payload, &p)
		payloadStatus = p.Status
	}
	versionStatus, ok := versionTargetStatus(in.Kind, payloadStatus)
	if !ok {
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("commit: %w", err)
		}
		return false, nil
	}

	versionRows, err := q.UpdateVersionStatus(ctx, sqlc.UpdateVersionStatusParams{
		ID:     toPgUUID(in.VersionID),
		Status: string(versionStatus),
	})
	if err != nil {
		return false, fmt.Errorf("update version status: %w", err)
	}

	var taskRows int64
	if taskStatus, mapped := taskStatusFromVersion(versionStatus); mapped {
		taskRows, err = q.UpdateTaskStatus(ctx, sqlc.UpdateTaskStatusParams{
			ID:             toPgUUID(in.TaskID),
			Status:         string(taskStatus),
			CurrentVersion: toPgUUID(in.VersionID),
		})
		if err != nil {
			return false, fmt.Errorf("update task status: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	return versionRows > 0 || taskRows > 0, nil
}

// ApplyGeneratedTitle sanitizes a worker-generated semantic title and persists
// it on the tasks row through the dedicated query — never an ad-hoc UPDATE
// (spec: task-event-ingest → "Title events update the task title"). An empty
// or all-whitespace title is silently skipped. The caller passes the
// tx-bound queries so the write shares the event-insert transaction.
func (s *Service) ApplyGeneratedTitle(
	ctx context.Context,
	q *sqlc.Queries,
	taskID uuid.UUID,
	raw string,
) (bool, error) {
	title := sanitizeGeneratedTitle(raw)
	if title == "" {
		return false, nil
	}
	rows, err := q.UpdateTaskTitle(ctx, sqlc.UpdateTaskTitleParams{
		ID:    toPgUUID(taskID),
		Title: title,
	})
	if err != nil {
		return false, fmt.Errorf("update task title: %w", err)
	}
	return rows > 0, nil
}

// ApplyVersionSummary sanitizes a worker-generated run summary and persists it
// on the task_versions row through the dedicated query — never an ad-hoc
// UPDATE (spec: task-event-ingest → "Summary events update the version
// summary"). An empty or all-whitespace summary is silently skipped. The
// caller passes the tx-bound queries so the write shares the event-insert
// transaction.
func (s *Service) ApplyVersionSummary(
	ctx context.Context,
	q *sqlc.Queries,
	versionID uuid.UUID,
	raw string,
) (bool, error) {
	summary := sanitizeVersionSummary(raw)
	if summary == "" {
		return false, nil
	}
	rows, err := q.UpdateVersionSummary(ctx, sqlc.UpdateVersionSummaryParams{
		ID:      toPgUUID(versionID),
		Summary: &summary,
	})
	if err != nil {
		return false, fmt.Errorf("update version summary: %w", err)
	}
	return rows > 0, nil
}
