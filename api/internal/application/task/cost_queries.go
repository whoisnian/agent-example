package task

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "github.com/whoisnian/agent-example/api/internal/domain/task"
)

// CostReadService is the application-layer wrapper around the domain
// CostReadService. Same idiom as ReadService: identity comes in as bare
// tenant/user ids and gets folded into the single domain.Owner type at the
// boundary, so the HTTP layer doesn't need to know about pgtype shapes.
type CostReadService struct {
	Domain *domain.CostReadService
}

// NewCostReadService constructs the application cost read service.
func NewCostReadService(d *domain.CostReadService) *CostReadService {
	return &CostReadService{Domain: d}
}

// GetTaskCost returns the task-level cost detail (total + by_version).
func (s *CostReadService) GetTaskCost(ctx context.Context, tenantID, userID, taskID uuid.UUID) (domain.TaskCostDetail, error) {
	return s.Domain.GetTaskCost(ctx, domain.Owner{TenantID: tenantID, UserID: userID}, taskID)
}

// GetVersionCost returns the single-version cost detail.
func (s *CostReadService) GetVersionCost(ctx context.Context, tenantID, userID, versionID uuid.UUID) (domain.VersionCostDetail, error) {
	return s.Domain.GetVersionCost(ctx, domain.Owner{TenantID: tenantID, UserID: userID}, versionID)
}

// GetOwnerCostTotal returns the no-group_by /me/cost rollup.
func (s *CostReadService) GetOwnerCostTotal(ctx context.Context, tenantID, userID uuid.UUID, from, to *time.Time) (domain.OwnerCostTotal, error) {
	return s.Domain.GetOwnerCostTotal(ctx, domain.Owner{TenantID: tenantID, UserID: userID}, from, to)
}

// GetOwnerCostGrouped returns the grouped /me/cost rollup. groupBy MUST be
// one of GroupByDay / GroupByTaskType / GroupByModel — the HTTP layer
// validates inputs.
func (s *CostReadService) GetOwnerCostGrouped(ctx context.Context, tenantID, userID uuid.UUID, groupBy string, from, to *time.Time) (domain.OwnerCostGrouped, error) {
	return s.Domain.GetOwnerCostGrouped(ctx, domain.Owner{TenantID: tenantID, UserID: userID}, groupBy, from, to)
}

// ListPricing returns the currently-effective pricing rows. Owner-agnostic
// — caller identity is not consulted.
func (s *CostReadService) ListPricing(ctx context.Context) (domain.PricingList, error) {
	return s.Domain.ListPricing(ctx)
}
