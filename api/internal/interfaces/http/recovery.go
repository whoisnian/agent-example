package httpapi

import (
	"fmt"
	"log/slog"
	"runtime/debug"

	"github.com/gin-gonic/gin"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// recoveryMiddleware intercepts panics inside handlers, logs the stack with
// the request's trace id, increments `http_panics_total`, and responds with
// the standard internal_error envelope.
func recoveryMiddleware(logger *slog.Logger, m *observability.Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				m.HTTPPanicsTotal.Inc()
				stack := debug.Stack()
				logger.LogAttrs(c.Request.Context(), slog.LevelError, "http_panic",
					slog.String("error", fmt.Sprintf("%v", rec)),
					slog.String("stack", string(stack)),
					slog.String("method", c.Request.Method),
					slog.String("path", c.Request.URL.Path),
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
