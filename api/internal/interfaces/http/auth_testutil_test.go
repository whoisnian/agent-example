package httpapi

import (
	"context"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/whoisnian/agent-example/api/internal/auth"
	"github.com/whoisnian/agent-example/api/internal/domain/identity"
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

// fakeUserRepo is an in-memory identity.Repository keyed on normalized email,
// letting handler tests build a real identity.Authenticator without a database.
type fakeUserRepo struct {
	byEmail map[string]identity.User
}

func (f *fakeUserRepo) FindByEmail(_ context.Context, normalizedEmail string) (identity.User, error) {
	u, ok := f.byEmail[normalizedEmail]
	if !ok {
		return identity.User{}, identity.ErrUserNotFound
	}
	return u, nil
}

// newTestAuthenticator builds an Authenticator over a one-user repo whose stored
// hash is bcrypt(password) at MinCost (fast). The email is stored normalized so
// the lookup key agrees with the verifier. Returns the seeded user too.
func newTestAuthenticator(t *testing.T, email, password string) (*identity.Authenticator, identity.User) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u := identity.User{
		ID:           uuid.MustParse("00000000-0000-0000-0000-000000000002"),
		TenantID:     uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		Email:        identity.NormalizeEmail(email),
		PasswordHash: string(hash),
	}
	repo := &fakeUserRepo{byEmail: map[string]identity.User{u.Email: u}}
	return identity.NewAuthenticator(repo), u
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
