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

	if err := q.InsertTaskEvent(ctx, sqlc.InsertTaskEventParams{
		TaskID:    toPgUUID(in.TaskID),
		VersionID: toPgUUID(in.VersionID),
		RunID:     toPgUUID(in.RunID),
		Seq:       in.Seq,
		Kind:      in.Kind,
		Payload:   []byte(in.Payload),
	}); err != nil {
		return false, fmt.Errorf("insert task_event: %w", err)
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
