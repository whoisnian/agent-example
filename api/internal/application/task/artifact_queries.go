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

// PresignArtifact returns a presigned download URL for an owned artifact.
func (s *ArtifactReadService) PresignArtifact(ctx context.Context, tenantID, userID, artifactID uuid.UUID) (domain.PresignResult, error) {
	return s.Domain.PresignArtifact(ctx, domain.Owner{TenantID: tenantID, UserID: userID}, artifactID)
}
