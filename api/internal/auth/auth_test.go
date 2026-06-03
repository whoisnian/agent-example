package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const testSecret = "test-secret-please-ignore"

func TestIssueParseRoundTrip(t *testing.T) {
	t.Parallel()
	tenant, user := uuid.New(), uuid.New()
	iss := NewIssuer(testSecret, time.Hour)
	tok, exp, err := iss.Issue(tenant, user)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// expires_at == iat + ttl (within a second of now+ttl).
	if d := time.Until(exp); d < 59*time.Minute || d > time.Hour+time.Second {
		t.Errorf("expiry %v not ~1h out", d)
	}

	p, err := NewVerifier(testSecret).Parse(tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.TenantID != tenant || p.UserID != user {
		t.Errorf("principal=%+v, want tenant=%s user=%s", p, tenant, user)
	}
}

func TestParseRejectsExpired(t *testing.T) {
	t.Parallel()
	// A negative TTL yields an already-expired token (beyond the 30s leeway).
	tok, _, err := NewIssuer(testSecret, -time.Hour).Issue(uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := NewVerifier(testSecret).Parse(tok); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("err=%v, want ErrInvalidToken for expired", err)
	}
}

func TestParseRejectsWrongSecret(t *testing.T) {
	t.Parallel()
	tok, _, _ := NewIssuer(testSecret, time.Hour).Issue(uuid.New(), uuid.New())
	if _, err := NewVerifier("a-different-secret").Parse(tok); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("err=%v, want ErrInvalidToken for bad signature", err)
	}
}

func TestParseRejectsAlgNone(t *testing.T) {
	t.Parallel()
	// Forge an unsigned token (alg=none) for a valid-looking principal.
	c := claims{
		Tenant: uuid.New().String(),
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   uuid.New().String(),
			Issuer:    tokenIssuer,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodNone, c).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("forge: %v", err)
	}
	if _, err := NewVerifier(testSecret).Parse(tok); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("err=%v, want ErrInvalidToken for alg=none", err)
	}
}

func TestParseRejectsWrongIssuer(t *testing.T) {
	t.Parallel()
	c := claims{
		Tenant: uuid.New().String(),
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   uuid.New().String(),
			Issuer:    "someone-else",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(testSecret))
	if _, err := NewVerifier(testSecret).Parse(tok); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("err=%v, want ErrInvalidToken for wrong issuer", err)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	t.Parallel()
	if _, err := NewVerifier(testSecret).Parse("not.a.jwt"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("err=%v, want ErrInvalidToken for malformed", err)
	}
	if _, err := NewVerifier(testSecret).Parse(""); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("err=%v, want ErrInvalidToken for empty", err)
	}
}

func TestContextRoundTrip(t *testing.T) {
	t.Parallel()
	p := Principal{TenantID: uuid.New(), UserID: uuid.New()}
	ctx := WithPrincipal(context.Background(), p)
	got, ok := PrincipalFromContext(ctx)
	if !ok || got != p {
		t.Errorf("got=%+v ok=%v, want %+v true", got, ok, p)
	}
	if _, ok := PrincipalFromContext(context.Background()); ok {
		t.Error("empty context must report ok=false")
	}
}

func TestErrInvalidTokenLeaksNothing(t *testing.T) {
	t.Parallel()
	// The sentinel message must not hint at the failure reason.
	if strings.Contains(ErrInvalidToken.Error(), "expired") ||
		strings.Contains(ErrInvalidToken.Error(), "signature") {
		t.Errorf("sentinel message leaks a reason: %q", ErrInvalidToken.Error())
	}
}
