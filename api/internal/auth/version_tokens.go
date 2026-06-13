package auth

// Version-scoped artifact tokens (improve-artifact-conversation-ux). Two more
// short-lived HS256 grants alongside the single-artifact download token, both
// scoped to a whole version via `sub = <version_id>`:
//
//   - artifact-archive : authorizes the zip-archive download of a version's
//     artifacts (rides in a `?token=` query param, like artifact-download).
//   - version-preview  : authorizes the directory-aware HTML preview of a
//     version's artifacts. The token rides in a PATH SEGMENT (not a query)
//     because a rendered document's relative asset references resolve against
//     the document path and never inherit a query string.
//
// Isolation stays explicit: each audience is required by exactly one verifier
// path; access tokens (no aud) and the other artifact-token kinds are rejected
// by audience mismatch, never by claim-shape accident.

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	// archiveAudience marks a version-archive download grant.
	archiveAudience = "artifact-archive"
	// previewAudience marks a version-preview grant (token in a path segment).
	previewAudience = "version-preview"
)

// issueForVersion mints an HS256 token for one version scoped to the given
// audience. The mint instant is truncated to whole seconds so the returned
// expiry and the token's second-granularity `exp` denote the same instant
// (mirrors DownloadIssuer.Issue).
func (i *DownloadIssuer) issueForVersion(audience string, versionID uuid.UUID) (token string, expiresAt time.Time, err error) {
	now := time.Now().UTC().Truncate(time.Second)
	exp := now.Add(i.ttl)
	c := jwt.RegisteredClaims{
		Subject:   versionID.String(),
		Issuer:    tokenIssuer,
		Audience:  jwt.ClaimStrings{audience},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(exp),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(i.secret)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, exp, nil
}

// IssueArchive mints a version-archive download token.
func (i *DownloadIssuer) IssueArchive(versionID uuid.UUID) (string, time.Time, error) {
	return i.issueForVersion(archiveAudience, versionID)
}

// IssuePreview mints a version-preview token.
func (i *DownloadIssuer) IssuePreview(versionID uuid.UUID) (string, time.Time, error) {
	return i.issueForVersion(previewAudience, versionID)
}

// parseForVersion verifies a version-scoped token: signature, HS256, issuer,
// the required audience, a required expiry, and `sub == versionID`. Every
// failure collapses to the single ErrInvalidToken sentinel so the reason never
// leaks (mirrors DownloadVerifier.Parse).
func (v *DownloadVerifier) parseForVersion(token, audience string, versionID uuid.UUID) error {
	var c jwt.RegisteredClaims
	_, err := jwt.ParseWithClaims(token, &c,
		func(*jwt.Token) (any, error) { return v.secret, nil },
		jwt.WithValidMethods([]string{signingMethod}),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithAudience(audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30*time.Second),
	)
	if err != nil {
		return ErrInvalidToken
	}
	if c.Subject != versionID.String() {
		return ErrInvalidToken
	}
	return nil
}

// ParseArchive validates a version-archive download token against versionID.
func (v *DownloadVerifier) ParseArchive(token string, versionID uuid.UUID) error {
	return v.parseForVersion(token, archiveAudience, versionID)
}

// ParsePreview validates a version-preview token against versionID.
func (v *DownloadVerifier) ParsePreview(token string, versionID uuid.UUID) error {
	return v.parseForVersion(token, previewAudience, versionID)
}

// ArchiveURLSigner renders the API-relative archive download URL for one
// version: `/api/v1/versions/{id}/artifacts/archive?token=<jwt>`. Satisfies the
// task domain's VersionArchivePresigner interface (structural typing).
type ArchiveURLSigner struct {
	Issuer *DownloadIssuer
}

// SignArchive mints an archive token and returns the relative download URL plus
// the token's expiry.
func (s ArchiveURLSigner) SignArchive(versionID uuid.UUID) (string, time.Time, error) {
	tok, exp, err := s.Issuer.IssueArchive(versionID)
	if err != nil {
		return "", time.Time{}, err
	}
	return "/api/v1/versions/" + versionID.String() + "/artifacts/archive?token=" + tok, exp, nil
}

// PreviewURLSigner renders the API-relative preview BASE url for one version:
// `/api/v1/versions/{id}/preview/<jwt>`. The token is a path segment (JWT chars
// are all path-safe), so a document loaded from `<base>/index.html` resolves
// its relative `./css/x.css` to `<base>/css/x.css` under the same token.
type PreviewURLSigner struct {
	Issuer *DownloadIssuer
}

// SignPreview mints a preview token and returns the relative base URL (no
// trailing slash) plus the token's expiry.
func (s PreviewURLSigner) SignPreview(versionID uuid.UUID) (string, time.Time, error) {
	tok, exp, err := s.Issuer.IssuePreview(versionID)
	if err != nil {
		return "", time.Time{}, err
	}
	return "/api/v1/versions/" + versionID.String() + "/preview/" + tok, exp, nil
}
