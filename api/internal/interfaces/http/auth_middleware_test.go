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

	"github.com/whoisnian/agent-example/api/internal/auth"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// newAuthEngine builds a real NewEngine (with the Bearer middleware) plus a
// protected probe route that records whether it ran and echoes the principal.
func newAuthEngine(t *testing.T) (engine *gin.Engine, handlerRan *bool) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ran := new(bool)
	deps := ServerDeps{
		Logger:   logger,
		Metrics:  observability.NewMetrics(),
		Probes:   NewProbeRegistry(0),
		Verifier: testVerifier(),
		AuthHandlers: &AuthHandlers{
			Issuer: auth.NewIssuer(testJWTSecret, time.Hour), Logger: logger,
			DevEmail: "d@e.com", DevPassword: "pw",
			DevTenantID: uuid.New(), DevUserID: uuid.New(),
		},
	}
	e := NewEngine(&deps)
	e.GET("/api/v1/_whoami", func(c *gin.Context) {
		*ran = true
		p, ok := principalOrAbort(c)
		if !ok {
			return
		}
		OK(c, gin.H{"tenant": p.TenantID.String(), "user": p.UserID.String()})
	})
	return e, ran
}

func doReq(e *gin.Engine, method, path, authHeader string) (status int, body string) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, http.NoBody)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	e.ServeHTTP(w, req)
	return w.Result().StatusCode, w.Body.String()
}

func TestAuth_ValidTokenInjectsPrincipalAndRunsHandler(t *testing.T) {
	e, ran := newAuthEngine(t)
	tenant, user := uuid.New(), uuid.New()
	tok, _, err := auth.NewIssuer(testJWTSecret, time.Hour).Issue(tenant, user)
	if err != nil {
		t.Fatal(err)
	}
	status, body := doReq(e, http.MethodGet, "/api/v1/_whoami", "Bearer "+tok)
	if status != http.StatusOK || !*ran {
		t.Fatalf("status=%d ran=%v body=%s", status, *ran, body)
	}
	var env struct {
		Data map[string]string `json:"data"`
	}
	_ = json.Unmarshal([]byte(body), &env)
	if env.Data["tenant"] != tenant.String() || env.Data["user"] != user.String() {
		t.Errorf("principal not injected: %v", env.Data)
	}
}

func TestAuth_RejectedTokens401AndHandlerNotRun(t *testing.T) {
	expired, _, _ := auth.NewIssuer(testJWTSecret, -time.Hour).Issue(uuid.New(), uuid.New())
	wrongSig, _, _ := auth.NewIssuer("a-different-secret", time.Hour).Issue(uuid.New(), uuid.New())
	cases := map[string]string{
		"missing":    "",
		"non-bearer": "Token abc",
		"garbage":    "Bearer not-a-jwt",
		"wrong-sig":  "Bearer " + wrongSig,
		"expired":    "Bearer " + expired,
	}
	for name, header := range cases {
		e, ran := newAuthEngine(t)
		status, body := doReq(e, http.MethodGet, "/api/v1/_whoami", header)
		if status != http.StatusUnauthorized || !strings.Contains(body, "unauthenticated") {
			t.Errorf("%s: status=%d body=%s, want 401 unauthenticated", name, status, body)
		}
		if *ran {
			t.Errorf("%s: handler ran despite rejected token", name)
		}
	}
}

func TestAuth_401EnvelopeShape(t *testing.T) {
	e, _ := newAuthEngine(t)
	status, body := doReq(e, http.MethodGet, "/api/v1/_whoami", "")
	if status != http.StatusUnauthorized {
		t.Fatalf("status=%d", status)
	}
	var env Envelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("invalid envelope: %v", err)
	}
	if env.Code != "unauthenticated" || env.Data != nil {
		t.Errorf("envelope=%+v, want code=unauthenticated data=nil", env)
	}
}

func TestAuth_PublicRoutesBypass(t *testing.T) {
	e, _ := newAuthEngine(t)
	// Health/metrics + login are reachable with NO token (not 401).
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/healthz"},
		{http.MethodGet, "/readyz"},
		{http.MethodGet, "/metrics"},
	} {
		if status, body := doReq(e, tc.method, tc.path, ""); status == http.StatusUnauthorized {
			t.Errorf("%s %s returned 401, want public; body=%s", tc.method, tc.path, body)
		}
	}
	// The login route is allowlisted: no token, and it must NOT 401 (it 400s on
	// the empty body instead — proving the middleware let it through).
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	e.ServeHTTP(w, req)
	if w.Code == http.StatusUnauthorized {
		t.Errorf("login route hit 401, want public (got through to handler)")
	}
}
