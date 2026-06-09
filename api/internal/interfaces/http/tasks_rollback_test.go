// Handler-level unit tests for the rollback endpoint's request-validation
// surface (the paths that short-circuit before App.RollbackTask). The full
// branch/switch DB+outbox behaviour is covered by the integration suite. The
// app service is backed by a nil-pool domain Service: any path reaching the
// SQL layer would panic, which is fine since these tests only drive 400s.
package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	dto "github.com/prometheus/client_model/go"

	apptask "github.com/whoisnian/agent-example/api/internal/application/task"
	taskdomain "github.com/whoisnian/agent-example/api/internal/domain/task"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

func newRollbackTestEngine(t *testing.T) (*gin.Engine, *observability.Metrics) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(gin.Recovery())
	e.Use(injectPrincipal(
		uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		uuid.MustParse("00000000-0000-0000-0000-000000000002"),
	))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := observability.NewMetrics()
	app := apptask.NewService(taskdomain.NewService(nil, nil, taskdomain.SystemClock{}, taskdomain.UUIDv7Gen{}, "default", time.Minute))
	h := &TaskHandlers{App: app, Logger: logger, Metrics: m}
	v1 := e.Group("/api/v1")
	h.Register(v1)
	return e, m
}

func doRollback(e *gin.Engine, taskID, body string) (status int, payload map[string]any) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+taskID+"/rollback", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	e.ServeHTTP(w, req)
	_ = json.NewDecoder(w.Body).Decode(&payload)
	return w.Result().StatusCode, payload
}

func rollbackCounter(t *testing.T, m *observability.Metrics, mode, outcome string) float64 {
	t.Helper()
	c, err := m.TasksRolledBackTotal.GetMetricWithLabelValues(mode, outcome)
	if err != nil {
		t.Fatalf("get counter (%s, %s): %v", mode, outcome, err)
	}
	var pb dto.Metric
	if err := c.Write(&pb); err != nil {
		t.Fatalf("metric write: %v", err)
	}
	return pb.GetCounter().GetValue()
}

func validTarget() string {
	return uuid.NewString()
}

func TestRollback_MalformedTaskID400(t *testing.T) {
	t.Parallel()
	e, m := newRollbackTestEngine(t)
	status, env := doRollback(e, "not-a-uuid", `{"target_version_id":"`+validTarget()+`","mode":"branch"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d env=%v", status, env)
	}
	// pre-mode-parse failure → mode="unknown".
	if got := rollbackCounter(t, m, "unknown", "invalid"); got != 1 {
		t.Errorf("metric {unknown, invalid} = %v, want 1", got)
	}
}

func TestRollback_InvalidJSON400(t *testing.T) {
	t.Parallel()
	e, _ := newRollbackTestEngine(t)
	status, env := doRollback(e, uuid.NewString(), `{not json`)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d env=%v", status, env)
	}
}

func TestRollback_MissingTarget400(t *testing.T) {
	t.Parallel()
	e, _ := newRollbackTestEngine(t)
	status, env := doRollback(e, uuid.NewString(), `{"mode":"branch"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d env=%v", status, env)
	}
	data, _ := env["data"].(map[string]any)
	if data["field"] != "target_version_id" {
		t.Errorf("data.field = %v, want target_version_id", data["field"])
	}
}

func TestRollback_InvalidMode400(t *testing.T) {
	t.Parallel()
	e, m := newRollbackTestEngine(t)
	status, env := doRollback(e, uuid.NewString(), `{"target_version_id":"`+validTarget()+`","mode":"merge"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d env=%v", status, env)
	}
	data, _ := env["data"].(map[string]any)
	if data["field"] != "mode" {
		t.Errorf("data.field = %v, want mode", data["field"])
	}
	// mode label stays "unknown" — set only after IsValidRollbackMode passes.
	if got := rollbackCounter(t, m, "unknown", "invalid"); got != 1 {
		t.Errorf("metric {unknown, invalid} = %v, want 1", got)
	}
}
