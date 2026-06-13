package task

import (
	"context"

	"github.com/google/uuid"

	domain "github.com/whoisnian/agent-example/api/internal/domain/task"
)

// ArtifactReadService is the application-layer wrapper around the domain
// ArtifactReadService. Same idiom as the cost read service: identity arrives as
// bare tenant/user ids and is folded into domain.Owner at the boundary so the
// HTTP layer stays free of pgtype shapes.
type ArtifactReadService struct {
	Domain *domain.ArtifactReadService
}

// NewArtifactReadService constructs the application artifact read service.
func NewArtifactReadService(d *domain.ArtifactReadService) *ArtifactReadService {
	return &ArtifactReadService{Domain: d}
}

// ListVersionArtifacts returns the artifact metadata list for an owned version.
func (s *ArtifactReadService) ListVersionArtifacts(ctx context.Context, tenantID, userID, versionID uuid.UUID) (domain.VersionArtifacts, error) {
	return s.Domain.ListVersionArtifacts(ctx, domain.Owner{TenantID: tenantID, UserID: userID}, versionID)
}

// PresignArtifact returns an API-signed download URL for an owned artifact.
func (s *ArtifactReadService) PresignArtifact(ctx context.Context, tenantID, userID, artifactID uuid.UUID) (domain.PresignResult, error) {
	return s.Domain.PresignArtifact(ctx, domain.Owner{TenantID: tenantID, UserID: userID}, artifactID)
}

// OpenArtifactObject opens an artifact's object stream for the download proxy.
// Deliberately no tenant/user identity: the verified download token is the
// authorization (ownership was enforced when the URL was minted).
func (s *ArtifactReadService) OpenArtifactObject(ctx context.Context, artifactID uuid.UUID) (domain.ArtifactObject, error) {
	return s.Domain.OpenArtifactObject(ctx, artifactID)
}

// PresignArchive returns an API-signed zip-archive download URL for an owned
// version (improve-artifact-conversation-ux).
func (s *ArtifactReadService) PresignArchive(ctx context.Context, tenantID, userID, versionID uuid.UUID) (domain.ArchivePresignResult, error) {
	return s.Domain.PresignArchive(ctx, domain.Owner{TenantID: tenantID, UserID: userID}, versionID)
}

// PresignPreview returns an API-signed directory-preview base URL for an owned
// version.
func (s *ArtifactReadService) PresignPreview(ctx context.Context, tenantID, userID, versionID uuid.UUID) (domain.PreviewMintResult, error) {
	return s.Domain.PresignPreview(ctx, domain.Owner{TenantID: tenantID, UserID: userID}, versionID)
}

// ListVersionArchiveEntries returns the lazy zip entries for a version's
// artifacts. No identity: the verified version-archive token is the grant.
func (s *ArtifactReadService) ListVersionArchiveEntries(ctx context.Context, versionID uuid.UUID) ([]domain.ArchiveEntry, error) {
	return s.Domain.ListVersionArchiveEntries(ctx, versionID)
}

// OpenVersionFile opens one artifact by (version_id, path) for the preview
// route. No identity: the verified version-preview token is the grant.
func (s *ArtifactReadService) OpenVersionFile(ctx context.Context, versionID uuid.UUID, path string) (domain.ArtifactObject, error) {
	return s.Domain.OpenVersionFile(ctx, versionID, path)
}
