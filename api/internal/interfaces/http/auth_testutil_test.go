package httpapi

import (
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/whoisnian/agent-example/api/internal/auth"
)

// testJWTSecret signs tokens for tests that exercise the real authMiddleware
// (via NewEngine). Handler-only tests skip the middleware with injectPrincipal.
const testJWTSecret = "httpapi-test-secret"

// testVerifier builds a Verifier over testJWTSecret for NewEngine-based tests.
func testVerifier() *auth.Verifier { return auth.NewVerifier(testJWTSecret) }

// testAuthHeader mints a valid `Bearer <jwt>` for an arbitrary principal,
// signed with testJWTSecret so testVerifier accepts it.
func testAuthHeader(t *testing.T) string {
	t.Helper()
	tok, _, err := auth.NewIssuer(testJWTSecret, time.Hour).Issue(uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("mint test token: %v", err)
	}
	return "Bearer " + tok
}

// injectPrincipal is a test middleware that seats an authenticated principal in
// the request context, standing in for the real Bearer-token authMiddleware so
// owner-scoped handler tests can exercise the handler → app → domain stack
// without minting a JWT. Add it to the test engine after gin.Recovery().
func injectPrincipal(tenant, user uuid.UUID) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(
			auth.WithPrincipal(c.Request.Context(), auth.Principal{TenantID: tenant, UserID: user}),
		)
		c.Next()
	}
}
