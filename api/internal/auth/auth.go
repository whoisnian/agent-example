// Package auth issues and verifies the API's short-lived access tokens and
// carries the authenticated caller identity (Principal) through request context.
//
// The tokens are symmetric HS256 JWTs signed with a single configured secret
// (AUTH_JWT_SECRET). Verification PINS the accepted algorithm to HS256 (so an
// `alg=none` or asymmetric-key-confusion token is rejected) and validates the
// issuer + expiry. Every verification failure collapses to the single sentinel
// ErrInvalidToken so a caller can never branch on — or leak — which check failed.
package auth

import (
	"context"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// tokenIssuer is the fixed `iss` claim: signed by the Issuer and required by the
// Verifier. A single in-process issuer needs no configuration knob.
const tokenIssuer = "agent-api"

// signingMethod is the only algorithm accepted on the verify path.
const signingMethod = "HS256"

// ErrInvalidToken is the single sentinel Verifier.Parse returns for every
// failure reason (bad signature, expired, malformed, wrong alg, wrong issuer).
var ErrInvalidToken = errors.New("invalid token")

// Principal is the authenticated caller identity resolved from a token's claims.
type Principal struct {
	TenantID uuid.UUID
	UserID   uuid.UUID
}

// claims is the token payload: the standard registered claims plus the tenant
// id (`tid`). The user id rides in the standard `sub`.
type claims struct {
	Tenant string `json:"tid"`
	jwt.RegisteredClaims
}

// Issuer signs short-lived HS256 access tokens for a principal.
type Issuer struct {
	secret []byte
	ttl    time.Duration
}

// NewIssuer builds an Issuer from the configured secret and token lifetime.
func NewIssuer(secret string, ttl time.Duration) *Issuer {
	return &Issuer{secret: []byte(secret), ttl: ttl}
}

// Issue returns a signed token for the principal and its absolute UTC expiry
// (issue instant + TTL).
func (i *Issuer) Issue(tenantID, userID uuid.UUID) (token string, expiresAt time.Time, err error) {
	now := time.Now().UTC()
	exp := now.Add(i.ttl)
	c := claims{
		Tenant: tenantID.String(),
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			Issuer:    tokenIssuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(i.secret)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, exp, nil
}

// Verifier validates HS256 tokens and resolves their principal.
type Verifier struct {
	secret []byte
}

// NewVerifier builds a Verifier from the configured secret.
func NewVerifier(secret string) *Verifier {
	return &Verifier{secret: []byte(secret)}
}

// Parse verifies the signature, the `HS256` algorithm, the issuer, and the
// (required) expiry, then resolves the Principal from the claims. Any failure —
// including malformed input or a non-UUID claim — maps to ErrInvalidToken so the
// reason cannot leak. A small leeway absorbs clock skew on `exp`.
func (v *Verifier) Parse(token string) (Principal, error) {
	var c claims
	_, err := jwt.ParseWithClaims(token, &c,
		func(*jwt.Token) (any, error) { return v.secret, nil },
		jwt.WithValidMethods([]string{signingMethod}),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30*time.Second),
	)
	if err != nil {
		return Principal{}, ErrInvalidToken
	}
	tenantID, terr := uuid.Parse(c.Tenant)
	userID, uerr := uuid.Parse(c.Subject)
	if terr != nil || uerr != nil {
		return Principal{}, ErrInvalidToken
	}
	return Principal{TenantID: tenantID, UserID: userID}, nil
}

// principalCtxKey is the unexported context key for the request principal.
type principalCtxKey struct{}

// WithPrincipal returns a child context carrying the authenticated principal.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalCtxKey{}, p)
}

// PrincipalFromContext returns the principal placed by the auth middleware, and
// ok=false when none is present (callers MUST fail closed, never treat a missing
// principal as a zero-UUID owner).
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalCtxKey{}).(Principal)
	return p, ok
}
