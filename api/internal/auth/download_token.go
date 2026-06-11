package auth

// This file implements artifact-download tokens: short-lived HS256 JWTs that
// authorize exactly one artifact download via the public download proxy route.
// They ride in a query parameter because <img> / <iframe> / navigation cannot
// carry an Authorization header. Isolation from access tokens is explicit and
// bidirectional: download tokens carry aud=downloadAudience (which
// Verifier.Parse rejects), and DownloadVerifier requires that audience (which
// access tokens lack).

import (
	"net/url"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// downloadAudience is the fixed `aud` claim that marks a token as an artifact
// download grant. Access tokens have no audience; the two verifier paths each
// reject the other kind by design, never by claim-shape accident.
const downloadAudience = "artifact-download"

// DownloadIssuer signs single-artifact download tokens. The artifact id rides
// in `sub`; ownership is checked by the presign endpoint before minting, so
// possession of an unexpired token IS the authorization (same semantics as an
// S3 presigned URL, same blast-radius bound: one object, short TTL).
type DownloadIssuer struct {
	secret []byte
	ttl    time.Duration
}

// NewDownloadIssuer builds a DownloadIssuer from the shared JWT secret and the
// configured download-URL lifetime (OSS_PRESIGN_TTL).
func NewDownloadIssuer(secret string, ttl time.Duration) *DownloadIssuer {
	return &DownloadIssuer{secret: []byte(secret), ttl: ttl}
}

// Issue returns a signed download token for one artifact and its absolute UTC
// expiry. The mint instant is truncated to whole seconds first so the returned
// expiresAt and the token's second-granularity `exp` denote the same instant.
func (i *DownloadIssuer) Issue(artifactID uuid.UUID) (token string, expiresAt time.Time, err error) {
	now := time.Now().UTC().Truncate(time.Second)
	exp := now.Add(i.ttl)
	c := jwt.RegisteredClaims{
		Subject:   artifactID.String(),
		Issuer:    tokenIssuer,
		Audience:  jwt.ClaimStrings{downloadAudience},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(exp),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(i.secret)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, exp, nil
}

// DownloadURLSigner renders the API-relative signed download URL for one
// artifact: `/api/v1/artifacts/{id}/download?token=<jwt>`. It satisfies the
// task domain's ArtifactPresigner interface (structural typing — this package
// never imports the domain). The URL is relative on purpose: the browser
// resolves it against the app origin, so OSS reachability is never assumed.
type DownloadURLSigner struct {
	Issuer *DownloadIssuer
}

// SignDownload mints a download token for the artifact and returns the
// relative download URL plus the token's expiry.
func (s DownloadURLSigner) SignDownload(artifactID uuid.UUID) (string, time.Time, error) {
	tok, exp, err := s.Issuer.Issue(artifactID)
	if err != nil {
		return "", time.Time{}, err
	}
	return "/api/v1/artifacts/" + artifactID.String() + "/download?token=" + url.QueryEscape(tok), exp, nil
}

// DownloadVerifier validates download tokens for the download proxy route.
type DownloadVerifier struct {
	secret []byte
}

// NewDownloadVerifier builds a DownloadVerifier from the shared JWT secret.
func NewDownloadVerifier(secret string) *DownloadVerifier {
	return &DownloadVerifier{secret: []byte(secret)}
}

// Parse verifies the signature, the HS256 algorithm, the issuer, the download
// audience, and the (required) expiry, then requires `sub` to equal the
// artifact the caller is fetching. Any failure — including an access token
// (no aud) or a token minted for a different artifact — maps to the single
// ErrInvalidToken sentinel so the reason cannot leak.
func (v *DownloadVerifier) Parse(token string, artifactID uuid.UUID) error {
	var c jwt.RegisteredClaims
	_, err := jwt.ParseWithClaims(token, &c,
		func(*jwt.Token) (any, error) { return v.secret, nil },
		jwt.WithValidMethods([]string{signingMethod}),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithAudience(downloadAudience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30*time.Second),
	)
	if err != nil {
		return ErrInvalidToken
	}
	if c.Subject != artifactID.String() {
		return ErrInvalidToken
	}
	return nil
}
