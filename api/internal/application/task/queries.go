package task

import (
	"context"

	"github.com/google/uuid"

	domain "github.com/whoisnian/agent-example/api/internal/domain/task"
)

// ReadService is the application-layer wrapper around the domain ReadService.
// It is a sibling of the write Service so the read path stays free of the
// pool / clock / id-generator dependencies. Identity arrives as bare
// tenant/user ids (mirroring the write commands) and is folded into the single
// domain.Owner value type here — no second identity type is introduced.
type ReadService struct {
	Domain *domain.ReadService
}

// NewReadService constructs the application read service.
func NewReadService(d *domain.ReadService) *ReadService { return &ReadService{Domain: d} }

// ListTasks returns the caller's paginated, optionally status-filtered tasks.
func (s *ReadService) ListTasks(ctx context.Context, tenantID, userID uuid.UUID, page, pageSize int, status *string) (domain.TaskListPage, error) {
	return s.Domain.ListTasks(ctx, domain.Owner{TenantID: tenantID, UserID: userID}, page, pageSize, status)
}

// GetTask returns one task the caller owns, with its current-version summary.
func (s *ReadService) GetTask(ctx context.Context, tenantID, userID, taskID uuid.UUID) (domain.TaskDetail, error) {
	return s.Domain.GetTask(ctx, domain.Owner{TenantID: tenantID, UserID: userID}, taskID)
}

// ListVersions returns the version tree for a task the caller owns.
func (s *ReadService) ListVersions(ctx context.Context, tenantID, userID, taskID uuid.UUID) (domain.VersionTree, error) {
	return s.Domain.ListVersions(ctx, domain.Owner{TenantID: tenantID, UserID: userID}, taskID)
}

// GetVersion returns a version (with runs + cost) reachable by the caller.
func (s *ReadService) GetVersion(ctx context.Context, tenantID, userID, versionID uuid.UUID) (domain.VersionDetail, error) {
	return s.Domain.GetVersion(ctx, domain.Owner{TenantID: tenantID, UserID: userID}, versionID)
}

// ListVersionEvents returns the event backfill page for a version the caller owns.
func (s *ReadService) ListVersionEvents(ctx context.Context, tenantID, userID, versionID uuid.UUID, afterID int64, limit int) (domain.EventPage, error) {
	return s.Domain.ListVersionEvents(ctx, domain.Owner{TenantID: tenantID, UserID: userID}, versionID, afterID, limit)
}
