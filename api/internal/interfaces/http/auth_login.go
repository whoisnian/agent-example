package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/whoisnian/agent-example/api/internal/auth"
	"github.com/whoisnian/agent-example/api/internal/domain/identity"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// AuthHandlers serves the public login endpoint. Credentials are verified
// against the persistent users table via the Authenticator (add-api-user-store,
// replacing the former in-memory dev-principal comparison); a successful login
// issues a token for the looked-up user's tenant/user id. The password is never
// logged.
type AuthHandlers struct {
	Issuer        *auth.Issuer
	Authenticator *identity.Authenticator
	Logger        *slog.Logger
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

// login verifies credentials against the user store and issues a signed JWT. An
// unknown email and a wrong password are indistinguishable (both 401
// invalid_credentials); a malformed body is 400 invalid_input. The password
// never enters a log field.
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

	user, err := h.Authenticator.Verify(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, identity.ErrInvalidCredentials) {
			Error(c, http.StatusUnauthorized, "invalid_credentials", "email or password is incorrect")
			return
		}
		Error(c, http.StatusInternalServerError, "internal_error", "could not verify credentials")
		h.Logger.LogAttrs(c.Request.Context(), slog.LevelError, "auth_login_verify_failed",
			slog.String("trace_id", observability.TraceIDFromContext(c.Request.Context())),
			slog.String("err", err.Error()),
		)
		return
	}

	token, expiresAt, err := h.Issuer.Issue(user.TenantID, user.ID)
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
		slog.String("user_id", user.ID.String()),
	)
	OK(c, loginResponse{
		Token:     token,
		ExpiresAt: expiresAt.Format("2006-01-02T15:04:05Z07:00"),
		User:      loginUser{ID: user.ID, TenantID: user.TenantID, Email: user.Email},
	})
}
