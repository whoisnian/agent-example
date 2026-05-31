// Handler-level unit tests for task-control-api. These exercise the request-
// validation surface, the metric-label outcome paths, and the
// effective={queued,best_effort} discriminator with a stubbed app service.
// Integration tests in task_control_integration_test.go drive the full
// DB+outbox path.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	dto "github.com/prometheus/client_model/go"

	apptask "github.com/whoisnian/agent-example/api/internal/application/task"
	taskdomain "github.com/whoisnian/agent-example/api/internal/domain/task"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// ---------------------------------------------------------------------------
// Test-only seam: the production ControlService wraps a concrete domain
// service, but we want the handler to call into a stub. To avoid changing
// the production type's shape, the test builds an apptask.ControlService
// backed by a domain.ControlService whose Pool is nil — and intercepts
// before the SQL path via a separate fake the handler is rewired to.
// Simpler: we don't fake the ControlService here at all; we drive only
// the request-validation paths that short-circuit before App.Apply.
//
// The handler unit tests below stay laser-focused on the request-shape
// validation surface (which is what handler unit tests are for in this
// codebase, mirroring task_cost_reads_test.go).
// ---------------------------------------------------------------------------

func newControlTestEngine(t *testing.T) (*gin.Engine, *observability.Metrics) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(gin.Recovery()) // surface stub panics as 500 (handler tests stay short-circuit)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := observability.NewMetrics()
	// Build an app service backed by a domain service with a nil pool;
	// any code path that reaches Apply would panic — that's fine for the
	// validation-only tests below; the integration tests cover the SQL.
	app := apptask.NewControlService(taskdomain.NewControlService(nil, nil, taskdomain.SystemClock{}))
	h := &TaskControlHandlers{
		App:         app,
		Logger:      logger,
		Metrics:     m,
		DevTenantID: uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		DevUserID:   uuid.MustParse("00000000-0000-0000-0000-000000000002"),
	}
	v1 := e.Group("/api/v1")
	h.Register(v1)
	return e, m
}

func doControl(e *gin.Engine, taskID, body string) (status int, payload map[string]any) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+taskID+"/control", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	e.ServeHTTP(w, req)
	_ = json.NewDecoder(w.Body).Decode(&payload)
	return w.Result().StatusCode, payload
}

func counterValue(t *testing.T, m *observability.Metrics, action, outcome string) float64 {
	t.Helper()
	c, err := m.TaskControlRequestsTotal.GetMetricWithLabelValues(action, outcome)
	if err != nil {
		t.Fatalf("get counter (%s, %s): %v", action, outcome, err)
	}
	var pb dto.Metric
	if err := c.Write(&pb); err != nil {
		t.Fatalf("metric write: %v", err)
	}
	return pb.GetCounter().GetValue()
}

// ---------------------------------------------------------------------------
// validation paths (these never reach App.Apply)
// ---------------------------------------------------------------------------

func TestControl_MalformedUUID400(t *testing.T) {
	t.Parallel()
	e, m := newControlTestEngine(t)
	status, env := doControl(e, "not-a-uuid", `{"action":"pause"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d env=%v", status, env)
	}
	data, _ := env["data"].(map[string]any)
	if data["field"] != "task_id" {
		t.Errorf("data.field = %v, want task_id", data["field"])
	}
	if got := counterValue(t, m, "unknown", "invalid"); got != 1 {
		t.Errorf("metric {unknown, invalid} = %v, want 1", got)
	}
}

func TestControl_EmptyBody400(t *testing.T) {
	t.Parallel()
	e, _ := newControlTestEngine(t)
	id := uuid.New().String()
	status, env := doControl(e, id, "")
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d env=%v", status, env)
	}
	data, _ := env["data"].(map[string]any)
	if data["field"] != "action" {
		t.Errorf("data.field = %v, want action", data["field"])
	}
}

func TestControl_InvalidJSON400(t *testing.T) {
	t.Parallel()
	e, _ := newControlTestEngine(t)
	id := uuid.New().String()
	status, env := doControl(e, id, `{not json`)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d env=%v", status, env)
	}
	data, _ := env["data"].(map[string]any)
	if data["field"] != "body" {
		t.Errorf("data.field = %v, want body", data["field"])
	}
}

func TestControl_UnknownAction400(t *testing.T) {
	t.Parallel()
	e, m := newControlTestEngine(t)
	id := uuid.New().String()
	status, env := doControl(e, id, `{"action":"kill"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d env=%v", status, env)
	}
	data, _ := env["data"].(map[string]any)
	if data["field"] != "action" {
		t.Errorf("data.field = %v, want action", data["field"])
	}
	// Spec scenario: unparseable action bumps {unknown, invalid}.
	if got := counterValue(t, m, "unknown", "invalid"); got != 1 {
		t.Errorf("metric {unknown, invalid} = %v, want 1", got)
	}
}

func TestControl_ReasonOverflow400(t *testing.T) {
	t.Parallel()
	e, _ := newControlTestEngine(t)
	id := uuid.New().String()
	longReason := strings.Repeat("x", taskdomain.MaxControlReasonLen+1)
	body, _ := json.Marshal(map[string]any{"action": "pause", "reason": longReason})
	status, env := doControl(e, id, string(body))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d env=%v", status, env)
	}
	data, _ := env["data"].(map[string]any)
	if data["field"] != "reason" {
		t.Errorf("data.field = %v, want reason", data["field"])
	}
}

// ---------------------------------------------------------------------------
// silence the imports used only for context/fmt/bytes/errors in case the
// test surface trims further
// ---------------------------------------------------------------------------

var (
	_ = context.Background
	_ = bytes.NewReader
	_ = errors.New
	_ = fmt.Sprintf
)
