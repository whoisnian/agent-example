package httpapi

import (
	"errors"
	"net/http"

	taskdomain "github.com/whoisnian/agent-example/api/internal/domain/task"
)

// DomainErrorKind enumerates the generic error catalogue from design D11. Feature
// proposals layer subcodes on top via DomainError.Code.
type DomainErrorKind int

const (
	KindInvalidArgument DomainErrorKind = iota + 1
	KindUnauthenticated
	KindPermissionDenied
	KindNotFound
	KindConflict
	KindFailedPrecondition
	KindResourceExhausted
	KindInternal
	KindUnavailable
)

// DomainError is the canonical error type bubbled up from application/domain.
// Handlers translate it to HTTP via MapError.
type DomainError struct {
	Kind    DomainErrorKind
	Code    string // optional override of the default code for this kind
	Message string
	Cause   error
}

func (e *DomainError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

func (e *DomainError) Unwrap() error { return e.Cause }

// MapError translates an error (DomainError or otherwise) into HTTP status +
// code + message. Unknown errors degrade to 500 / internal_error.
//
// Task-write-api domain errors are recognised here so handlers do not have to
// reach across packages for them; the `data` block (e.g. active_version_id)
// is rendered separately by the handler so MapError stays envelope-agnostic.
func MapError(err error) (status int, code, message string) {
	if err == nil {
		return http.StatusOK, "0", "ok"
	}

	// Domain-specific errors from the task aggregate.
	switch {
	case errors.Is(err, taskdomain.ErrTaskNotFound):
		return http.StatusNotFound, "task_not_found", "task not found"
	case errors.Is(err, taskdomain.ErrVersionNotFound):
		return http.StatusNotFound, "version_not_found", "version not found"
	}
	var ave *taskdomain.ErrActiveVersionExists
	if errors.As(err, &ave) {
		return http.StatusConflict, "active_version_exists",
			"task has an active version; wait for it to finish or cancel it first"
	}
	var iie *taskdomain.ErrInvalidInput
	if errors.As(err, &iie) {
		return http.StatusBadRequest, "invalid_input", iie.Error()
	}

	var de *DomainError
	if errors.As(err, &de) {
		s, defaultCode := kindToHTTP(de.Kind)
		c := de.Code
		if c == "" {
			c = defaultCode
		}
		msg := de.Message
		if msg == "" {
			msg = defaultCode
		}
		return s, c, msg
	}
	return http.StatusInternalServerError, "internal_error", "internal server error"
}

// kindToHTTP maps a DomainErrorKind to (HTTP status, default code) per D11.
func kindToHTTP(k DomainErrorKind) (status int, code string) {
	switch k {
	case KindInvalidArgument:
		return http.StatusBadRequest, "invalid_argument"
	case KindUnauthenticated:
		return http.StatusUnauthorized, "unauthenticated"
	case KindPermissionDenied:
		return http.StatusForbidden, "permission_denied"
	case KindNotFound:
		return http.StatusNotFound, "not_found"
	case KindConflict:
		return http.StatusConflict, "conflict"
	case KindFailedPrecondition:
		return http.StatusPreconditionFailed, "failed_precondition"
	case KindResourceExhausted:
		return http.StatusTooManyRequests, "resource_exhausted"
	case KindUnavailable:
		return http.StatusServiceUnavailable, "unavailable"
	case KindInternal:
		return http.StatusInternalServerError, "internal_error"
	default:
		return http.StatusInternalServerError, "internal_error"
	}
}
