package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// newTestEngine builds a fresh engine with default deps for table-style tests.
func newTestEngine(t *testing.T) (*ServerDeps, http.Handler) {
	t.Helper()
	logger := observability.NewLogger("error", nil) // suppress noise during tests
	m := observability.NewMetrics()
	probes := NewProbeRegistry(0)
	deps := ServerDeps{Logger: logger, Metrics: m, Probes: probes}
	return &deps, NewEngine(deps)
}

func TestHealthz_AlwaysOK(t *testing.T) {
	_, h := newTestEngine(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestReadyz_OKWhenNoProbes(t *testing.T) {
	_, h := newTestEngine(t)
	req := httptest.NewRequest(http.MethodGet, "/readyz", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestReadyz_503WhenProbeFails(t *testing.T) {
	deps, h := newTestEngine(t)
	deps.Probes.Register("postgres", func(_ context.Context) error {
		return errors.New("connection refused")
	})
	req := httptest.NewRequest(http.MethodGet, "/readyz", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "postgres") {
		t.Errorf("expected failed list to mention postgres, got %s", rec.Body.String())
	}
}

func TestMetrics_Exposes_RequestsTotal(t *testing.T) {
	_, h := newTestEngine(t)
	// First hit /healthz to ensure http_requests_total has a series.
	req1 := httptest.NewRequest(http.MethodGet, "/healthz", http.NoBody)
	h.ServeHTTP(httptest.NewRecorder(), req1)

	req := httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "http_requests_total") {
		t.Errorf("expected http_requests_total in /metrics output")
	}
}

func TestPanicRecovery_Returns500Envelope(t *testing.T) {
	deps, _ := newTestEngine(t)
	engine := NewEngine(*deps)
	engine.GET("/boom", func(_ *gin.Context) { panic("kaboom") })
	req := httptest.NewRequest(http.MethodGet, "/boom", http.NoBody)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var env Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("invalid envelope: %v body=%s", err, rec.Body.String())
	}
	if env.Code != "internal_error" {
		t.Errorf("code = %v, want internal_error", env.Code)
	}
	if env.Data != nil {
		t.Errorf("data = %v, want nil", env.Data)
	}
}

func TestEnvelope_SuccessShape(t *testing.T) {
	deps, _ := newTestEngine(t)
	engine := NewEngine(*deps)
	engine.GET("/ok", func(c *gin.Context) {
		OK(c, map[string]int{"v": 1})
	})
	req := httptest.NewRequest(http.MethodGet, "/ok", http.NoBody)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var env Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("invalid envelope: %v", err)
	}
	// code is JSON-numeric on success; encoding/json decodes 0 as float64(0).
	if v, ok := env.Code.(float64); !ok || v != 0 {
		t.Errorf("code = %v (%T), want 0", env.Code, env.Code)
	}
	if env.Message != "ok" {
		t.Errorf("message = %q", env.Message)
	}
}
