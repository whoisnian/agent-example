package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
)

// ArtifactPresigner mints a short-lived presigned GET URL for one OSS object
// key, returning the URL and an advisory expiry instant. The infrastructure
// oss.Client implements it; unit tests inject a fake that records the key it
// was asked to sign (and can be told to fail).
type ArtifactPresigner interface {
	PresignGet(ctx context.Context, key string) (url string, expiresAt time.Time, err error)
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
}

// NewArtifactReadService constructs the artifact read service.
func NewArtifactReadService(q sqlc.Querier, p ArtifactPresigner) *ArtifactReadService {
	return &ArtifactReadService{Queries: q, Presigner: p}
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
			Mime:      r.Mime,
			Bytes:     r.Bytes,
			Sha256:    r.Sha256,
			CreatedAt: r.CreatedAt.Time,
		})
	}
	return out, nil
}

// PresignArtifact resolves an owned artifact's oss_key and ownership in one
// query, then mints a presigned GET URL for exactly that object. A missing
// artifact OR one owned by a different caller both map to ErrArtifactNotFound
// (identical 404 regardless of cause — no existence leak). A presign failure
// propagates as a wrapped error → 500 internal_error at the HTTP layer.
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

	url, expiresAt, err := s.Presigner.PresignGet(ctx, row.OssKey)
	if err != nil {
		return PresignResult{}, fmt.Errorf("presign artifact: %w", err)
	}
	return PresignResult{
		URL:       url,
		ExpiresAt: expiresAt,
		Bytes:     row.Bytes,
		Mime:      row.Mime,
		Sha256:    row.Sha256,
	}, nil
}
