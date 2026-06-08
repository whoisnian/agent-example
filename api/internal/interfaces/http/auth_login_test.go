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

	"github.com/whoisnian/agent-example/api/internal/auth"
)

const (
	loginEmail    = "dev@example.com"
	loginPassword = "correct-horse"
)

func newLoginEngine(t *testing.T) (*gin.Engine, *auth.Verifier) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(gin.Recovery())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	authenticator, _ := newTestAuthenticator(t, loginEmail, loginPassword)
	h := &AuthHandlers{
		Issuer:        auth.NewIssuer(testJWTSecret, time.Hour),
		Authenticator: authenticator,
		Logger:        logger,
	}
	v1 := e.Group("/api/v1")
	h.Register(v1)
	return e, auth.NewVerifier(testJWTSecret)
}

func postLogin(e *gin.Engine, body string) (status int, respBody string) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	e.ServeHTTP(w, req)
	return w.Result().StatusCode, w.Body.String()
}

func TestLogin_ValidCredentials(t *testing.T) {
	e, verifier := newLoginEngine(t)
	status, body := postLogin(e, `{"email":"`+loginEmail+`","password":"`+loginPassword+`"}`)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if strings.Contains(body, loginPassword) {
		t.Errorf("response leaked password: %s", body)
	}

	var env struct {
		Data loginResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	// The token verifies and resolves to the dev principal.
	p, err := verifier.Parse(env.Data.Token)
	if err != nil {
		t.Fatalf("token did not verify: %v", err)
	}
	if p.UserID != env.Data.User.ID || p.TenantID != env.Data.User.TenantID {
		t.Errorf("token principal %+v != response user %+v", p, env.Data.User)
	}
	if env.Data.User.Email != loginEmail {
		t.Errorf("user.email=%q, want %q", env.Data.User.Email, loginEmail)
	}
	if env.Data.ExpiresAt == "" {
		t.Error("expires_at is empty")
	}
}

func TestLogin_WrongPasswordAndUnknownEmailIndistinguishable(t *testing.T) {
	e, _ := newLoginEngine(t)
	cases := []string{
		`{"email":"` + loginEmail + `","password":"wrong"}`,
		`{"email":"nobody@example.com","password":"` + loginPassword + `"}`,
	}
	for _, body := range cases {
		status, resp := postLogin(e, body)
		if status != http.StatusUnauthorized || !strings.Contains(resp, "invalid_credentials") {
			t.Errorf("body=%s → status=%d resp=%s, want 401 invalid_credentials", body, status, resp)
		}
	}
}

func TestLogin_MalformedBody(t *testing.T) {
	e, _ := newLoginEngine(t)
	for _, body := range []string{`not json`, `{}`, `{"email":"x"}`} {
		status, resp := postLogin(e, body)
		if status != http.StatusBadRequest || !strings.Contains(resp, "invalid_input") {
			t.Errorf("body=%q → status=%d resp=%s, want 400 invalid_input", body, status, resp)
		}
	}
}
