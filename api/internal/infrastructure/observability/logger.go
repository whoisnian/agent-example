// Package observability wires logging, tracing, and metrics for the API service.
package observability

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
)

// ContextKey is the type for slog correlation context keys. Centralised so
// middleware and handlers cannot collide with arbitrary string keys.
type ContextKey string

const (
	CtxKeyTraceID   ContextKey = "trace_id"
	CtxKeyRequestID ContextKey = "request_id"
	CtxKeyTaskID    ContextKey = "task_id"
)

// NewLogger builds a JSON slog.Logger at the given level. Unknown levels
// fall back to info.
func NewLogger(level string, out io.Writer) *slog.Logger {
	if out == nil {
		out = os.Stdout
	}
	lvl := parseLevel(level)
	h := slog.NewJSONHandler(out, &slog.HandlerOptions{
		Level: lvl,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			// Standardise time key to `ts` for consistency across services.
			if a.Key == slog.TimeKey {
				a.Key = "ts"
			}
			return a
		},
	})
	return slog.New(&contextHandler{inner: h})
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// contextHandler wraps a slog.Handler and automatically attaches correlation
// IDs (trace_id, request_id, task_id) from context to every log record.
type contextHandler struct {
	inner slog.Handler
}

func (h *contextHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.inner.Enabled(ctx, lvl)
}

// Handle implements slog.Handler. The slog.Record value is large but the
// interface signature is dictated by stdlib, so we cannot take a pointer.
func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error { //nolint:gocritic // slog.Handler interface signature
	for _, k := range []ContextKey{CtxKeyTraceID, CtxKeyRequestID, CtxKeyTaskID} {
		if v, ok := ctx.Value(k).(string); ok && v != "" {
			r.AddAttrs(slog.String(string(k), v))
		}
	}
	return h.inner.Handle(ctx, r)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{inner: h.inner.WithGroup(name)}
}

// WithTraceID returns a child context carrying the given trace id.
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, CtxKeyTraceID, id)
}

// WithRequestID returns a child context carrying the given request id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, CtxKeyRequestID, id)
}

// WithTaskID returns a child context carrying the given task id.
func WithTaskID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, CtxKeyTaskID, id)
}

// TraceIDFromContext returns the trace id stored in ctx (empty if absent).
func TraceIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(CtxKeyTraceID).(string)
	return v
}

// RequestIDFromContext returns the request id stored in ctx (empty if absent).
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(CtxKeyRequestID).(string)
	return v
}
