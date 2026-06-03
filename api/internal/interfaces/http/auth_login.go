package httpapi

import (
	"crypto/subtle"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/whoisnian/agent-example/api/internal/auth"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// AuthHandlers serves the public login endpoint. For MVP the credential source
// is a single configured dev principal (no user store yet — see
// add-api-user-store); a successful login issues a token for DevTenantID /
// DevUserID. The password is verified constant-time and is never logged.
type AuthHandlers struct {
	Issuer      *auth.Issuer
	Logger      *slog.Logger
	DevEmail    string
	DevPassword string
	DevTenantID uuid.UUID
	DevUserID   uuid.UUID
}

// Register mounts POST /api/v1/auth/login. This route is PUBLIC — the auth
// middleware allowlists it (a caller has no token yet).
func (h *AuthHandlers) Register(r *gin.RouterGroup) {
	r.POST("/auth/login", h.login)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginUser struct {
	ID       uuid.UUID `json:"id"`
	TenantID uuid.UUID `json:"tenant_id"`
	Email    string    `json:"email"`
}

type loginResponse struct {
	Token     string    `json:"token"`
	ExpiresAt string    `json:"expires_at"`
	User      loginUser `json:"user"`
}

// login verifies credentials and issues a signed JWT. An unknown email and a
// wrong password are indistinguishable (both 401 invalid_credentials); a
// malformed body is 400 invalid_input. The password never enters a log field.
func (h *AuthHandlers) login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, "invalid_input", "body must be valid JSON with email and password")
		return
	}
	if req.Email == "" || req.Password == "" {
		Error(c, http.StatusBadRequest, "invalid_input", "email and password are required")
		return
	}

	// Constant-time on BOTH fields, combined so the response never reveals which
	// one mismatched.
	emailOK := subtle.ConstantTimeCompare([]byte(req.Email), []byte(h.DevEmail))
	passOK := subtle.ConstantTimeCompare([]byte(req.Password), []byte(h.DevPassword))
	if emailOK&passOK != 1 {
		Error(c, http.StatusUnauthorized, "invalid_credentials", "email or password is incorrect")
		return
	}

	token, expiresAt, err := h.Issuer.Issue(h.DevTenantID, h.DevUserID)
	if err != nil {
		Error(c, http.StatusInternalServerError, "internal_error", "could not issue token")
		h.Logger.LogAttrs(c.Request.Context(), slog.LevelError, "auth_login_issue_failed",
			slog.String("trace_id", observability.TraceIDFromContext(c.Request.Context())),
			slog.String("err", err.Error()),
		)
		return
	}

	h.Logger.LogAttrs(c.Request.Context(), slog.LevelInfo, "auth_login_ok",
		slog.String("trace_id", observability.TraceIDFromContext(c.Request.Context())),
		slog.String("user_id", h.DevUserID.String()),
	)
	OK(c, loginResponse{
		Token:     token,
		ExpiresAt: expiresAt.Format("2006-01-02T15:04:05Z07:00"),
		User:      loginUser{ID: h.DevUserID, TenantID: h.DevTenantID, Email: h.DevEmail},
	})
}
