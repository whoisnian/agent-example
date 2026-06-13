package task

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
)

// ArtifactPresigner signs a short-lived, API-relative download URL for one
// artifact, returning the URL and its expiry instant. Signing is local (an
// HS256 download token with sub = artifact id) — no OSS call. auth's
// DownloadURLSigner implements it; unit tests inject a fake that records the
// artifact id it was asked to sign (and can be told to fail).
type ArtifactPresigner interface {
	SignDownload(artifactID uuid.UUID) (url string, expiresAt time.Time, err error)
}

// VersionArchivePresigner signs a short-lived, API-relative archive download
// URL for one version (a version-archive token, sub = version id). Local
// signing, no OSS call. auth's ArchiveURLSigner implements it.
type VersionArchivePresigner interface {
	SignArchive(versionID uuid.UUID) (url string, expiresAt time.Time, err error)
}

// VersionPreviewPresigner signs a short-lived, API-relative preview BASE URL
// for one version (a version-preview token in a path segment, sub = version
// id). Local signing, no OSS call. auth's PreviewURLSigner implements it.
type VersionPreviewPresigner interface {
	SignPreview(versionID uuid.UUID) (baseURL string, expiresAt time.Time, err error)
}

// ArtifactObjectStore reads one object's bytes from the OSS bucket for the
// download proxy route. The infrastructure oss.Client implements it; the
// caller owns body and must Close it on every path. ContentLength is reported
// as the store knows it (nil when unknown; 0 is a legitimate empty object).
type ArtifactObjectStore interface {
	GetObject(ctx context.Context, key string) (body io.ReadCloser, contentLength *int64, err error)
}

// ArtifactReadService is the queries-only read side of the artifacts endpoints.
// Like the other read services it holds no pool / clock; it adds an
// ArtifactPresigner so the presign endpoint can mint a download URL without the
// HTTP layer touching the OSS client directly.
//
// Queries is the sqlc.Querier interface (not the concrete *sqlc.Queries) so the
// ownership + presign logic is unit-testable with a fake Querier — the SQL
// itself is still exercised end-to-end by the integration test. Production
// passes *sqlc.Queries, which satisfies the interface.
type ArtifactReadService struct {
	Queries   sqlc.Querier
	Presigner ArtifactPresigner
	Objects   ArtifactObjectStore
	// Version-scoped presigners (improve-artifact-conversation-ux). Optional on
	// the struct so the existing 3-arg constructor stays source-compatible;
	// production sets them (see cmd/api/main.go). The archive/preview endpoints
	// require them and will 500 (nil deref guarded by the wiring) if unset.
	ArchivePresigner VersionArchivePresigner
	PreviewPresigner VersionPreviewPresigner
}

// NewArtifactReadService constructs the artifact read service. The version
// archive/preview presigners are set as fields by the caller (main.go) to keep
// this constructor's signature stable for existing callers.
func NewArtifactReadService(q sqlc.Querier, p ArtifactPresigner, o ArtifactObjectStore) *ArtifactReadService {
	return &ArtifactReadService{Queries: q, Presigner: p, Objects: o}
}

// ListVersionArtifacts returns the artifact metadata for an owned version,
// ordered created_at ASC, id ASC. The version is ownership-probed first
// (reusing ownedVersion) so a missing/unowned version maps to
// ErrVersionNotFound before any artifact row is read. Never returns oss_key.
func (s *ArtifactReadService) ListVersionArtifacts(ctx context.Context, owner Owner, versionID uuid.UUID) (VersionArtifacts, error) {
	if _, err := ownedVersion(ctx, s.Queries, owner, versionID); err != nil {
		return VersionArtifacts{}, err
	}

	rows, err := s.Queries.ListArtifactsByVersion(ctx, toPgUUID(versionID))
	if err != nil {
		return VersionArtifacts{}, fmt.Errorf("list artifacts: %w", err)
	}

	out := VersionArtifacts{
		VersionID: versionID,
		Artifacts: make([]ArtifactMeta, 0, len(rows)),
	}
	for i := range rows {
		r := &rows[i]
		out.Artifacts = append(out.Artifacts, ArtifactMeta{
			ID:        uuid.UUID(r.ID.Bytes),
			Kind:      r.Kind,
			Path:      r.Path,
			Mime:      r.Mime,
			Bytes:     r.Bytes,
			Sha256:    r.Sha256,
			CreatedAt: r.CreatedAt.Time,
		})
	}
	return out, nil
}

// PresignArtifact resolves an artifact's existence and ownership in one query,
// then signs an API-relative download URL scoped to exactly that artifact. A
// missing artifact OR one owned by a different caller both map to
// ErrArtifactNotFound (identical 404 regardless of cause — no existence leak).
// Signing is local; a failure propagates as a wrapped error → 500 at the HTTP
// layer. Ownership is enforced HERE, at mint time: the download route trusts
// token possession (S3-presign semantics).
func (s *ArtifactReadService) PresignArtifact(ctx context.Context, owner Owner, artifactID uuid.UUID) (PresignResult, error) {
	row, err := s.Queries.GetArtifactWithOwner(ctx, toPgUUID(artifactID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PresignResult{}, ErrArtifactNotFound
		}
		return PresignResult{}, fmt.Errorf("get artifact: %w", err)
	}
	if !owner.owns(row.TenantID, row.UserID) {
		return PresignResult{}, ErrArtifactNotFound
	}

	url, expiresAt, err := s.Presigner.SignDownload(artifactID)
	if err != nil {
		return PresignResult{}, fmt.Errorf("sign download url: %w", err)
	}
	return PresignResult{
		URL:       url,
		ExpiresAt: expiresAt,
		Bytes:     row.Bytes,
		Mime:      row.Mime,
		Sha256:    row.Sha256,
	}, nil
}

// ArtifactObject is an opened artifact stream for the download proxy: the
// object body plus the metadata the HTTP layer turns into response headers.
// The caller must Close Body on every path.
type ArtifactObject struct {
	Body          io.ReadCloser
	ContentLength *int64
	Mime          *string
}

// OpenArtifactObject resolves an artifact row by id and opens its OSS object
// for streaming. No owner check: authorization happened at mint time
// (PresignArtifact is owner-scoped) and the verified download token IS the
// grant. A missing row maps to ErrArtifactNotFound; any object-store failure
// maps to ErrOSSUnavailable with the cause wrapped for logs only — the HTTP
// layer never leaks the oss_key or the store's error detail.
func (s *ArtifactReadService) OpenArtifactObject(ctx context.Context, artifactID uuid.UUID) (ArtifactObject, error) {
	row, err := s.Queries.GetArtifactWithOwner(ctx, toPgUUID(artifactID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ArtifactObject{}, ErrArtifactNotFound
		}
		return ArtifactObject{}, fmt.Errorf("get artifact: %w", err)
	}
	body, n, err := s.Objects.GetObject(ctx, row.OssKey)
	if err != nil {
		return ArtifactObject{}, fmt.Errorf("%w: %w", ErrOSSUnavailable, err)
	}
	return ArtifactObject{Body: body, ContentLength: n, Mime: row.Mime}, nil
}

// PresignArchive owner-probes the version, then signs a version-archive
// download URL. A missing/unowned version maps to ErrVersionNotFound (no
// existence leak). Signing is local; a failure → 500. Ownership is enforced
// HERE at mint time — the archive download route trusts token possession.
func (s *ArtifactReadService) PresignArchive(ctx context.Context, owner Owner, versionID uuid.UUID) (ArchivePresignResult, error) {
	if _, err := ownedVersion(ctx, s.Queries, owner, versionID); err != nil {
		return ArchivePresignResult{}, err
	}
	url, expiresAt, err := s.ArchivePresigner.SignArchive(versionID)
	if err != nil {
		return ArchivePresignResult{}, fmt.Errorf("sign archive url: %w", err)
	}
	return ArchivePresignResult{URL: url, ExpiresAt: expiresAt}, nil
}

// PresignPreview owner-probes the version, then signs a version-preview base
// URL. Same ownership/error posture as PresignArchive.
func (s *ArtifactReadService) PresignPreview(ctx context.Context, owner Owner, versionID uuid.UUID) (PreviewMintResult, error) {
	if _, err := ownedVersion(ctx, s.Queries, owner, versionID); err != nil {
		return PreviewMintResult{}, err
	}
	baseURL, expiresAt, err := s.PreviewPresigner.SignPreview(versionID)
	if err != nil {
		return PreviewMintResult{}, fmt.Errorf("sign preview url: %w", err)
	}
	return PreviewMintResult{BaseURL: baseURL, ExpiresAt: expiresAt}, nil
}

// ArchiveEntry is one file in a version's zip archive: the in-zip entry name
// and a lazy opener for its OSS bytes. The opener captures the object key so
// the oss_key never leaves the domain (the handler streams bytes only).
type ArchiveEntry struct {
	Name string
	open func(ctx context.Context) (io.ReadCloser, error)
}

// Open reads the entry's object bytes; the caller must Close the result.
func (e ArchiveEntry) Open(ctx context.Context) (io.ReadCloser, error) { return e.open(ctx) }

// ListVersionArchiveEntries returns one ArchiveEntry per artifact of the
// version, ordered created_at ASC / id ASC. No owner check: the verified
// version-archive token is the grant (ownership enforced at PresignArchive
// mint time). The zip entry name is the artifact's path, falling back to
// `artifact-<id>` when path is null. A version with no artifacts yields an
// empty slice (the handler writes a valid empty zip).
func (s *ArtifactReadService) ListVersionArchiveEntries(ctx context.Context, versionID uuid.UUID) ([]ArchiveEntry, error) {
	rows, err := s.Queries.ListArtifactObjectsByVersion(ctx, toPgUUID(versionID))
	if err != nil {
		return nil, fmt.Errorf("list artifact objects: %w", err)
	}
	entries := make([]ArchiveEntry, 0, len(rows))
	for i := range rows {
		r := rows[i]
		key := r.OssKey
		name := "artifact-" + uuid.UUID(r.ID.Bytes).String()
		if r.Path != nil && *r.Path != "" {
			name = *r.Path
		}
		entries = append(entries, ArchiveEntry{
			Name: name,
			open: func(ctx context.Context) (io.ReadCloser, error) {
				body, _, oerr := s.Objects.GetObject(ctx, key)
				if oerr != nil {
					return nil, fmt.Errorf("%w: %w", ErrOSSUnavailable, oerr)
				}
				return body, nil
			},
		})
	}
	return entries, nil
}

// OpenVersionFile resolves one artifact by (version_id, path) and opens its OSS
// object for the directory-aware preview route. No owner check: the verified
// version-preview token is the grant. A missing row maps to ErrArtifactNotFound;
// an object-store failure maps to ErrOSSUnavailable (cause wrapped for logs).
func (s *ArtifactReadService) OpenVersionFile(ctx context.Context, versionID uuid.UUID, path string) (ArtifactObject, error) {
	row, err := s.Queries.GetArtifactObjectByVersionPath(ctx, sqlc.GetArtifactObjectByVersionPathParams{
		VersionID: toPgUUID(versionID),
		Path:      &path,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ArtifactObject{}, ErrArtifactNotFound
		}
		return ArtifactObject{}, fmt.Errorf("get artifact by path: %w", err)
	}
	body, n, err := s.Objects.GetObject(ctx, row.OssKey)
	if err != nil {
		return ArtifactObject{}, fmt.Errorf("%w: %w", ErrOSSUnavailable, err)
	}
	return ArtifactObject{Body: body, ContentLength: n, Mime: row.Mime}, nil
}
