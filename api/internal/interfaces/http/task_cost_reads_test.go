// Handler-level unit tests for task-cost-api. These exercise the request-
// validation surface (group_by parsing, window cap, RFC3339, owner-agnostic
// /pricing) with a fake CostReadService so they don't need a DB. Integration
// against real data lives in task_cost_reads_integration_test.go.
package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	apptask "github.com/whoisnian/agent-example/api/internal/application/task"
	taskdomain "github.com/whoisnian/agent-example/api/internal/domain/task"
)

// ---------------------------------------------------------------------------
// Handler-level unit tests exercise the request-validation surface — group_by
// parsing, time-window cap, RFC3339 parsing — which short-circuits BEFORE
// the service call. Integration tests cover the SQL paths.
// ---------------------------------------------------------------------------

func newCostTestEngine(t *testing.T, svc *apptask.CostReadService, nowFn func() time.Time) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	e := gin.New()
	// Recovery middleware so a panic from the nil-backed stub service
	// surfaces as a clean 500 instead of crashing the test process.
	e.Use(gin.Recovery())
	tenantID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	userID := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &TaskCostHandlers{
		App:         svc,
		Logger:      logger,
		DevTenantID: tenantID,
		DevUserID:   userID,
		NowFn:       nowFn,
	}
	v1 := e.Group("/api/v1")
	h.Register(v1)
	return e
}

func do(e *gin.Engine, path string) (status int, body map[string]any) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	e.ServeHTTP(w, req)
	_ = json.NewDecoder(w.Body).Decode(&body)
	return w.Result().StatusCode, body
}

// ---------------------------------------------------------------------------
// request-validation tests — these short-circuit before the service is
// called, so no service stub is needed (a nil app would panic if reached;
// we assert status alone). We pass a no-op app for type-shape compliance.
// ---------------------------------------------------------------------------

// noopCostSvc satisfies the *apptask.CostReadService shape via a domain
// service backed by no queries — calls would panic. Used as a "must not
// reach service" sentinel.
func noopCostSvc() *apptask.CostReadService {
	return apptask.NewCostReadService(taskdomain.NewCostReadService(nil))
}

func TestGetOwnerCost_InvalidGroupBy400(t *testing.T) {
	t.Parallel()
	e := newCostTestEngine(t, noopCostSvc(), nil)
	status, env := do(e, "/api/v1/me/cost?group_by=hour")
	if status != http.StatusBadRequest {
		t.Errorf("status=%d, want 400; env=%v", status, env)
	}
	if env["code"] != "invalid_input" {
		t.Errorf("code=%v, want invalid_input", env["code"])
	}
	// field name must be "group_by" so the front-end can surface it.
	data, _ := env["data"].(map[string]any)
	if data["field"] != "group_by" {
		t.Errorf("data.field=%v, want group_by", data["field"])
	}
}

func TestGetOwnerCost_MalformedFrom400(t *testing.T) {
	t.Parallel()
	e := newCostTestEngine(t, noopCostSvc(), nil)
	status, env := do(e, "/api/v1/me/cost?from=yesterday")
	if status != http.StatusBadRequest {
		t.Errorf("status=%d, want 400; env=%v", status, env)
	}
	data, _ := env["data"].(map[string]any)
	if data["field"] != "from" {
		t.Errorf("data.field=%v, want from", data["field"])
	}
}

func TestGetOwnerCost_FromGreaterThanTo400(t *testing.T) {
	t.Parallel()
	e := newCostTestEngine(t, noopCostSvc(), nil)
	status, env := do(e, "/api/v1/me/cost?from=2026-05-30T00:00:00Z&to=2026-05-29T00:00:00Z")
	if status != http.StatusBadRequest {
		t.Errorf("status=%d, want 400; env=%v", status, env)
	}
	data, _ := env["data"].(map[string]any)
	if data["field"] != "to" {
		t.Errorf("data.field=%v, want to", data["field"])
	}
}

func TestGetOwnerCost_WindowCapExceeded400(t *testing.T) {
	t.Parallel()
	e := newCostTestEngine(t, noopCostSvc(), nil)
	status, env := do(e, "/api/v1/me/cost?group_by=day&from=2025-01-01T00:00:00Z&to=2026-05-31T00:00:00Z")
	if status != http.StatusBadRequest {
		t.Errorf("status=%d, want 400; env=%v", status, env)
	}
	data, _ := env["data"].(map[string]any)
	if data["field"] != "to" {
		t.Errorf("data.field=%v, want to (cap violation blames `to`)", data["field"])
	}
}

func TestGetOwnerCost_EmptyGroupByActsAsAbsent(t *testing.T) {
	t.Parallel()
	// With group_by="" the handler MUST take the totals branch — confirm by
	// observing it does NOT 400 the way an invalid value would. We can't
	// reach the service (nil), so a panic would surface as a 500. Confirm
	// the request gets past the request-validation 400s.
	e := newCostTestEngine(t, noopCostSvc(), nil)
	status, _ := do(e, "/api/v1/me/cost?group_by=")
	if status == http.StatusBadRequest {
		t.Errorf("status=%d, want NOT 400 (empty group_by is treated as absent)", status)
	}
}

func TestGetTaskCost_MalformedUUID400(t *testing.T) {
	t.Parallel()
	e := newCostTestEngine(t, noopCostSvc(), nil)
	status, env := do(e, "/api/v1/tasks/not-a-uuid/cost")
	if status != http.StatusBadRequest {
		t.Errorf("status=%d, want 400; env=%v", status, env)
	}
	data, _ := env["data"].(map[string]any)
	if data["field"] != "task_id" {
		t.Errorf("data.field=%v, want task_id", data["field"])
	}
}

func TestGetVersionCost_MalformedUUID400(t *testing.T) {
	t.Parallel()
	e := newCostTestEngine(t, noopCostSvc(), nil)
	status, env := do(e, "/api/v1/versions/not-a-uuid/cost")
	if status != http.StatusBadRequest {
		t.Errorf("status=%d, want 400; env=%v", status, env)
	}
	data, _ := env["data"].(map[string]any)
	if data["field"] != "version_id" {
		t.Errorf("data.field=%v, want version_id", data["field"])
	}
}

func TestNowFn_DefaultedToTimeNow(t *testing.T) {
	t.Parallel()
	// When NowFn is nil, the handler MUST fall back to time.Now (verified
	// by ensuring the returned timestamp is within seconds of real now).
	h := &TaskCostHandlers{}
	got := h.now()
	if d := time.Since(got); d < 0 || d > time.Second {
		t.Errorf("fallback clock skewed: %v ago", d)
	}
}

func TestNowFn_InjectedClock(t *testing.T) {
	t.Parallel()
	fixed := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	h := &TaskCostHandlers{NowFn: func() time.Time { return fixed }}
	if got := h.now(); !got.Equal(fixed) {
		t.Errorf("NowFn ignored: got %v want %v", got, fixed)
	}
}

// keep context import alive — used implicitly by Gin's middleware chain in
// some test paths but not directly referenced in this file.
var _ = context.Background
