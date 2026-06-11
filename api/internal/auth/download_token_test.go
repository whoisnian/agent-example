package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestDownloadIssueParseRoundTrip(t *testing.T) {
	t.Parallel()
	artifactID := uuid.New()
	tok, exp, err := NewDownloadIssuer(testSecret, 5*time.Minute).Issue(artifactID)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// expiresAt is second-granularity (mint truncated) and ~ttl out.
	if exp != exp.Truncate(time.Second) {
		t.Errorf("expiry %v not truncated to whole seconds", exp)
	}
	if d := time.Until(exp); d < 4*time.Minute || d > 5*time.Minute+time.Second {
		t.Errorf("expiry %v not ~5m out", d)
	}
	if err := NewDownloadVerifier(testSecret).Parse(tok, artifactID); err != nil {
		t.Errorf("parse: %v", err)
	}
}

func TestDownloadParseRejectsExpired(t *testing.T) {
	t.Parallel()
	// Negative TTL → already expired beyond the 30s leeway.
	artifactID := uuid.New()
	tok, _, err := NewDownloadIssuer(testSecret, -time.Hour).Issue(artifactID)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if err := NewDownloadVerifier(testSecret).Parse(tok, artifactID); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("err=%v, want ErrInvalidToken for expired", err)
	}
}

func TestDownloadParseRejectsArtifactMismatch(t *testing.T) {
	t.Parallel()
	tok, _, err := NewDownloadIssuer(testSecret, 5*time.Minute).Issue(uuid.New())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if err := NewDownloadVerifier(testSecret).Parse(tok, uuid.New()); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("err=%v, want ErrInvalidToken for sub/path mismatch", err)
	}
}

func TestDownloadParseRejectsAccessToken(t *testing.T) {
	t.Parallel()
	// A valid login access token (no aud) must fail the audience requirement.
	tok, _, err := NewIssuer(testSecret, time.Hour).Issue(uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("issue access token: %v", err)
	}
	if err := NewDownloadVerifier(testSecret).Parse(tok, uuid.New()); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("err=%v, want ErrInvalidToken for access token (missing aud)", err)
	}
}

func TestDownloadParseRejectsWrongOrMissingIssuer(t *testing.T) {
	t.Parallel()
	artifactID := uuid.New()
	for name, issuer := range map[string]string{"wrong": "someone-else", "missing": ""} {
		c := jwt.RegisteredClaims{
			Subject:   artifactID.String(),
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{downloadAudience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		}
		tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(testSecret))
		if err := NewDownloadVerifier(testSecret).Parse(tok, artifactID); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("%s issuer: err=%v, want ErrInvalidToken", name, err)
		}
	}
}

func TestDownloadParseRejectsAlgNone(t *testing.T) {
	t.Parallel()
	artifactID := uuid.New()
	c := jwt.RegisteredClaims{
		Subject:   artifactID.String(),
		Issuer:    tokenIssuer,
		Audience:  jwt.ClaimStrings{downloadAudience},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodNone, c).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("forge: %v", err)
	}
	if err := NewDownloadVerifier(testSecret).Parse(tok, artifactID); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("err=%v, want ErrInvalidToken for alg=none", err)
	}
}

func TestDownloadParseRejectsWrongSecret(t *testing.T) {
	t.Parallel()
	artifactID := uuid.New()
	tok, _, _ := NewDownloadIssuer(testSecret, 5*time.Minute).Issue(artifactID)
	if err := NewDownloadVerifier("a-different-secret").Parse(tok, artifactID); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("err=%v, want ErrInvalidToken for bad signature", err)
	}
}

func TestAccessParseRejectsDownloadToken(t *testing.T) {
	t.Parallel()
	// A download token must never pass the Bearer (access) verifier.
	tok, _, err := NewDownloadIssuer(testSecret, 5*time.Minute).Issue(uuid.New())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := NewVerifier(testSecret).Parse(tok); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("err=%v, want ErrInvalidToken for download token on access path", err)
	}
}

func TestAccessParseRejectsAudEvenWithAccessShape(t *testing.T) {
	t.Parallel()
	// Forge a token carrying BOTH valid access-token claims (tid + uuid sub)
	// AND an audience: the explicit non-empty-aud rejection must fire — the
	// access path must not depend on download tokens lacking `tid`.
	c := claims{
		Tenant: uuid.New().String(),
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   uuid.New().String(),
			Issuer:    tokenIssuer,
			Audience:  jwt.ClaimStrings{downloadAudience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(testSecret))
	if _, err := NewVerifier(testSecret).Parse(tok); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("err=%v, want ErrInvalidToken for aud-bearing access-shaped token", err)
	}
}
