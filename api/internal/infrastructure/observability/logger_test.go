package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestLogger_AttachesCorrelationIDsFromContext(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("info", &buf)

	ctx := context.Background()
	ctx = WithTraceID(ctx, "trace-abc")
	ctx = WithRequestID(ctx, "req-xyz")
	ctx = WithTaskID(ctx, "task-1")

	logger.InfoContext(ctx, "hello")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, buf.String())
	}
	if entry["trace_id"] != "trace-abc" {
		t.Errorf("trace_id = %v", entry["trace_id"])
	}
	if entry["request_id"] != "req-xyz" {
		t.Errorf("request_id = %v", entry["request_id"])
	}
	if entry["task_id"] != "task-1" {
		t.Errorf("task_id = %v", entry["task_id"])
	}
	if _, ok := entry["ts"]; !ok {
		t.Error("expected ts field")
	}
}

func TestLogger_RespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("warn", &buf)
	logger.Info("should not appear")
	if buf.Len() != 0 {
		t.Errorf("expected info filtered out, got %q", buf.String())
	}
	logger.Warn("should appear")
	if !strings.Contains(buf.String(), "should appear") {
		t.Errorf("expected warn passed through, got %q", buf.String())
	}
}
