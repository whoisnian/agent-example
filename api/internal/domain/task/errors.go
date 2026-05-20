// Package task contains the Task aggregate's domain logic: status semantics,
// validation rules, the canonical execute payload, and the two write
// operations (`CreateTask`, `IterateTask`) shared by add-task-create-api.
//
// Boundaries:
//   - Persistence is reached only through sqlc.Queries / pgx.Tx.
//   - The package never imports the http layer; handlers translate these
//     errors to envelope codes via the table in interfaces/http/errors.go.
package task

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ErrTaskNotFound signals that the iterate-target task has no row. The HTTP
// layer maps this to 404 task_not_found.
var ErrTaskNotFound = errors.New("task not found")

// ErrVersionNotFound signals that `base_version_id` either does not exist or
// does not belong to the path `task_id`; also raised when iterate falls back
// to `tasks.current_version` but that column is NULL. Maps to 404
// version_not_found.
var ErrVersionNotFound = errors.New("version not found")

// ErrActiveVersionExists carries the active version's id + status so the
// 409 envelope can populate `data.active_version_id` / `data.active_version_status`
// per design D2 / D7. The error is produced both by the app-level pre-check
// and by the savepoint-based SQLSTATE 23505 fallback.
type ErrActiveVersionExists struct {
	ActiveVersionID     uuid.UUID
	ActiveVersionStatus string
}

func (e *ErrActiveVersionExists) Error() string {
	return fmt.Sprintf("task has an active version %s in status %s",
		e.ActiveVersionID, e.ActiveVersionStatus)
}

// ErrInvalidInput names the offending field and the reason it failed
// validation. Maps to 400 invalid_input.
type ErrInvalidInput struct {
	Field  string
	Reason string
}

func (e *ErrInvalidInput) Error() string {
	return fmt.Sprintf("invalid_input: %s: %s", e.Field, e.Reason)
}

// newInvalidInput is a small helper so call sites stay terse.
func newInvalidInput(field, reason string) *ErrInvalidInput {
	return &ErrInvalidInput{Field: field, Reason: reason}
}
