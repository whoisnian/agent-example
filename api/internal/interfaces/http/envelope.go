// Package httpapi contains the HTTP interface layer: gin engine, middleware,
// envelope helpers, and health/metrics handlers.
//
// The package name is `httpapi` (not `http`) to avoid colliding with the stdlib
// `net/http`. Import path remains `.../internal/interfaces/http`.
package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// Envelope is the unified business response shape: {code, message, data, trace_id}.
//
// `Code` is `0` (number) on success, or a string error code (e.g. "not_found")
// on failure; using `any` keeps both representations natural.
type Envelope struct {
	Code    any    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
	TraceID string `json:"trace_id"`
}

// OK writes a 200 success envelope with the given payload.
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Envelope{
		Code:    0,
		Message: "ok",
		Data:    data,
		TraceID: observability.TraceIDFromContext(c.Request.Context()),
	})
}

// JSON writes an arbitrary status + envelope. Used for non-200 successes
// (e.g. 201 Created).
func JSON(c *gin.Context, status int, data any) {
	c.JSON(status, Envelope{
		Code:    0,
		Message: "ok",
		Data:    data,
		TraceID: observability.TraceIDFromContext(c.Request.Context()),
	})
}

// Error writes an error envelope with the given HTTP status, business code,
// and human-readable message. `data` is always null on errors.
func Error(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, Envelope{
		Code:    code,
		Message: message,
		Data:    nil,
		TraceID: observability.TraceIDFromContext(c.Request.Context()),
	})
}
