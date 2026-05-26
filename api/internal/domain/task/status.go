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

// versionStatuses is the full set of statuses a *version* may carry — the
// eight values from the task_versions_status_check constraint in migration
// 0002. Unlike tasks, versions add `queued` and `cancelling`. Used by the
// event-ingest mapping to reject an unknown `payload.status` before it can
// reach the DB.
var versionStatuses = map[Status]struct{}{
	StatusPending:    {},
	StatusQueued:     {},
	StatusRunning:    {},
	StatusPaused:     {},
	StatusCancelling: {},
	StatusCancelled:  {},
	StatusSucceeded:  {},
	StatusFailed:     {},
}

// versionTargetStatus maps an inbound worker event to the version status it
// should drive (add-event-ingest-status-sync, design D5/D6). It branches on
// `kind` first: a `kind=error` event is a failure trigger regardless of its
// payload (the worker's error path emits no trailing `status:failed`); a
// `kind=status` event carries the target in `payloadStatus`, accepted only if
// it is a known version status. Any other kind (`plan`/`step`/…) or an
// unrecognised status returns ok=false → persist the event but do not
// transition.
func versionTargetStatus(kind, payloadStatus string) (Status, bool) {
	switch kind {
	case "error":
		return StatusFailed, true
	case "status":
		s := Status(payloadStatus)
		if _, ok := versionStatuses[s]; ok {
			return s, true
		}
		return "", false
	default:
		return "", false
	}
}

// taskStatusFromVersion maps a version status onto the task status it implies
// for the task's *current* version (ARCHITECTURE §4.3). The version and task
// status domains differ: `task_versions_status_check` permits `queued` and
// `cancelling`, but `tasks_status_check` does not. So `queued` collapses to
// the task's `pending`, and `cancelling` has no task equivalent → ok=false so
// the caller skips the tasks UPDATE rather than tripping the CHECK constraint.
func taskStatusFromVersion(v Status) (Status, bool) {
	switch v {
	case StatusQueued:
		return StatusPending, true
	case StatusCancelling:
		return "", false
	case StatusPending, StatusRunning, StatusPaused,
		StatusCancelled, StatusSucceeded, StatusFailed:
		return v, true
	default:
		return "", false
	}
}
