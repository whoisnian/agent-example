package task

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ExecutePayload is the wire-shape of an `execute.<task_type>.<lane>`
// message, identical to docs/ARCHITECTURE.md §5.3. The struct exists so the
// builder can produce deterministic JSON via the marshaller; the worker side
// will decode against an equivalent type.
type ExecutePayload struct {
	MsgID              uuid.UUID       `json:"msg_id"`
	IdempotencyKey     string          `json:"idempotency_key"`
	TaskID             uuid.UUID       `json:"task_id"`
	VersionID          uuid.UUID       `json:"version_id"`
	RunID              uuid.UUID       `json:"run_id"`
	AttemptNo          int             `json:"attempt_no"`
	TaskType           string          `json:"task_type"`
	Prompt             string          `json:"prompt"`
	Params             json.RawMessage `json:"params"`
	Lane               string          `json:"lane"`
	ParentVersionID    *uuid.UUID      `json:"parent_version_id"`
	ParentArtifactRoot *string         `json:"parent_artifact_root"`
	DeadlineTS         int64           `json:"deadline_ts"`
	// GenTitle is a create-only whitelist flag: true only when the create path
	// derived a placeholder title, asking the worker to generate a semantic one.
	// Iterate / rollback / republish never set it; absent means false.
	GenTitle bool `json:"gen_title,omitempty"`
}

// buildExecutePayload assembles the payload bytes. The function is the single
// place that knows the wire shape; D8 changes go through here.
//
// `parentVersionID` and `parentArtifactRoot` may both be nil (for `CreateTask`,
// where no parent exists). The DeadlineTS is computed as `now + deadline`
// using the injected Clock.
func buildExecutePayload(
	msgID, taskID, versionID, runID uuid.UUID,
	taskType, prompt, lane string,
	params []byte,
	parentVersionID *uuid.UUID,
	parentArtifactRoot *string,
	now time.Time,
	deadline time.Duration,
	genTitle bool,
) ([]byte, error) {
	pl := ExecutePayload{
		MsgID:              msgID,
		IdempotencyKey:     runID.String(),
		TaskID:             taskID,
		VersionID:          versionID,
		RunID:              runID,
		AttemptNo:          1,
		TaskType:           taskType,
		Prompt:             prompt,
		Params:             json.RawMessage(params),
		Lane:               lane,
		ParentVersionID:    parentVersionID,
		ParentArtifactRoot: parentArtifactRoot,
		DeadlineTS:         now.Add(deadline).Unix(),
		GenTitle:           genTitle,
	}
	return json.Marshal(pl)
}
