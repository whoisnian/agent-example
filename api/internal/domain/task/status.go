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
