package task

import (
	"time"

	"github.com/google/uuid"
)

// --- /versions/{id}/artifacts ----------------------------------------------

// VersionArtifacts is the payload of GET /api/v1/versions/{version_id}/artifacts.
// Artifacts is ordered created_at ASC, id ASC and is always a non-nil slice
// (empty `[]` when the version has produced nothing yet).
type VersionArtifacts struct {
	VersionID uuid.UUID      `json:"version_id"`
	Artifacts []ArtifactMeta `json:"artifacts"`
}

// ArtifactMeta is one artifact's public metadata. It deliberately omits
// oss_key — the internal storage layout is never part of the contract (design
// D6); clients reach bytes only through the presign endpoint. Path / Mime /
// Bytes / Sha256 are nullable in the table and render as JSON null (present,
// never omitted) when absent. Path is the artifact's version-relative file
// path (e.g. index.html, css/style.css) — the preferred display label.
type ArtifactMeta struct {
	ID        uuid.UUID `json:"id"`
	Kind      string    `json:"kind"`
	Path      *string   `json:"path"`
	Mime      *string   `json:"mime"`
	Bytes     *int64    `json:"bytes"`
	Sha256    *string   `json:"sha256"`
	CreatedAt time.Time `json:"created_at"`
}

// --- /artifacts/{id}/presign -----------------------------------------------

// PresignResult is the payload of GET /api/v1/artifacts/{artifact_id}/presign.
// URL is a short-lived presigned GET URL for one object; ExpiresAt is the
// advisory expiry (mint-time + configured TTL, UTC — OSS is the authority on
// actual expiry). Bytes / Mime / Sha256 echo the row so the client can label
// the download, following the same nullable rule as ArtifactMeta.
type PresignResult struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
	Bytes     *int64    `json:"bytes"`
	Mime      *string   `json:"mime"`
	Sha256    *string   `json:"sha256"`
}

// --- /versions/{id}/artifacts/archive/presign ------------------------------

// ArchivePresignResult is the payload of the version-archive presign endpoint:
// a short-lived API-relative URL that streams a zip of the version's artifacts,
// plus its advisory expiry (mint-time + configured TTL, UTC).
type ArchivePresignResult struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// --- /versions/{id}/preview ------------------------------------------------

// PreviewMintResult is the payload of the version-preview mint endpoint:
// an opaque API-relative base URL (`/api/v1/versions/{id}/preview/<token>`)
// under which a rendered HTML artifact's relative asset references resolve to
// sibling artifacts of the version, plus its advisory expiry.
type PreviewMintResult struct {
	BaseURL   string    `json:"base_url"`
	ExpiresAt time.Time `json:"expires_at"`
}
