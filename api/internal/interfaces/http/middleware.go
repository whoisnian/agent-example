package httpapi

import (
	"log/slog"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

const (
	headerRequestID  = "X-Request-ID"
	headerTraceParent = "traceparent"
	tracerName       = "github.com/whoisnian/agent-example/api/http"
)

// requestIDMiddleware extracts or generates a request id, stores it in
// the request context, and echoes it in the response header.
func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader(headerRequestID)
		if rid == "" {
			rid = uuid.NewString()
		}
		ctx := observability.WithRequestID(c.Request.Context(), rid)
		c.Request = c.Request.WithContext(ctx)
		c.Writer.Header().Set(headerRequestID, rid)
		c.Next()
	}
}

// tracingMiddleware starts a span per request, propagates W3C headers, and
// stuffs the trace id into the request context so log records and the response
// envelope can reference it.
func tracingMiddleware() gin.HandlerFunc {
	tracer := otel.Tracer(tracerName)
	prop := otel.GetTextMapPropagator()
	return func(c *gin.Context) {
		ctx := prop.Extract(c.Request.Context(), propagation.HeaderCarrier(c.Request.Header))
		route := c.FullPath()
		if route == "" {
			route = c.Request.URL.Path
		}
		spanName := "HTTP " + c.Request.Method + " " + route

		ctx, span := tracer.Start(ctx, spanName,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				semconv.HTTPRequestMethodKey.String(c.Request.Method),
				semconv.HTTPRouteKey.String(route),
			),
		)
		defer span.End()

		if sc := span.SpanContext(); sc.HasTraceID() {
			ctx = observability.WithTraceID(ctx, sc.TraceID().String())
		}
		c.Request = c.Request.WithContext(ctx)

		c.Next()

		span.SetAttributes(semconv.HTTPResponseStatusCodeKey.Int(c.Writer.Status()))
	}
}

// metricsMiddleware records request count and duration into the observability
// registry. Routes without a matching template fall back to "unmatched" so
// label cardinality stays bounded.
func metricsMiddleware(m *observability.Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		status := strconv.Itoa(c.Writer.Status())
		m.HTTPRequestsTotal.WithLabelValues(route, c.Request.Method, status).Inc()
		m.HTTPRequestDuration.WithLabelValues(route, c.Request.Method).Observe(time.Since(start).Seconds())
	}
}

// accessLogMiddleware emits one structured log entry per completed request.
// Correlation IDs are pulled from context by the slog handler automatically.
func accessLogMiddleware(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.LogAttrs(c.Request.Context(), slog.LevelInfo, "http_access",
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.Int("status", c.Writer.Status()),
			slog.Duration("duration", time.Since(start)),
			slog.String("remote", c.ClientIP()),
		)
	}
}

// authMiddleware is the pass-through stub reserved by design. Real auth
// arrives via the `add-api-auth` proposal; placing the slot here keeps the
// chain order stable.
func authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
	}
}
