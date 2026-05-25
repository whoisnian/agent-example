package task

// Status names the lifecycle states a task or version may carry. The string
// values match the DB CHECK constraints in migration 0002.
type Status string

const (
	StatusPending    Status = "pending"
	StatusQueued     Status = "queued"
	StatusRunning    Status = "running"
	StatusPaused     Status = "paused"
	StatusCancelling Status = "cancelling"
	StatusCancelled  Status = "cancelled"
	StatusSucceeded  Status = "succeeded"
	StatusFailed     Status = "failed"
)

// activeStatuses is the single source of truth for D11.5: the set of states
// that count against the task-level mutex. Order matches state-machine
// progression so a casual reader can follow the lifecycle.
var activeStatuses = map[Status]struct{}{
	StatusPending:    {},
	StatusQueued:     {},
	StatusRunning:    {},
	StatusPaused:     {},
	StatusCancelling: {},
}

// IsActive reports whether `s` belongs to the active set. Used by the
// app-level mutex pre-check before attempting INSERT.
func IsActive(s string) bool {
	_, ok := activeStatuses[Status(s)]
	return ok
}

// taskStatuses is the set of statuses a *task* (not a version) may carry —
// the six values from the tasks_status_check constraint in migration 0002.
// Note this is NOT activeStatuses: the version-only `queued` / `cancelling`
// are absent. Used by the read API to validate the `status` list filter.
var taskStatuses = map[Status]struct{}{
	StatusPending:   {},
	StatusRunning:   {},
	StatusPaused:    {},
	StatusCancelled: {},
	StatusSucceeded: {},
	StatusFailed:    {},
}

// IsValidTaskStatus reports whether `s` is one of the six task statuses. The
// read API rejects any other `status` query value with 400 invalid_input
// rather than silently returning an empty page.
func IsValidTaskStatus(s string) bool {
	_, ok := taskStatuses[Status(s)]
	return ok
}
