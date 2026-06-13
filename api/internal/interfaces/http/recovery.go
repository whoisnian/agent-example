package httpapi

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// recoveryMiddleware intercepts panics inside handlers, logs the stack with
// the request's trace id, increments `http_panics_total`, and responds with
// the standard internal_error envelope. http.ErrAbortHandler is re-panicked
// untouched: it is the stdlib's deliberate abort signal (the download proxy
// uses it to cut a stream mid-body so the client never sees a clean EOF on a
// truncated response) — net/http must see it to close the connection without
// the terminal chunk, and it is not an unexpected panic worth a metric.
func recoveryMiddleware(logger *slog.Logger, m *observability.Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}
				m.HTTPPanicsTotal.Inc()
				stack := debug.Stack()
				logger.LogAttrs(c.Request.Context(), slog.LevelError, "http_panic",
					slog.String("error", fmt.Sprintf("%v", rec)),
					slog.String("stack", string(stack)),
					slog.String("method", c.Request.Method),
					slog.String("path", accessLogPath(c)), // redacts the preview token segment
				)
				if !c.Writer.Written() {
					Error(c, 500, "internal_error", "internal server error")
				} else {
					c.Abort()
				}
			}
		}()
		c.Next()
	}
}
