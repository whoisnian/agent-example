package task

import (
	"context"

	"github.com/google/uuid"

	domain "github.com/whoisnian/agent-example/api/internal/domain/task"
)

// ErrTaskNotFound / ErrVersionNotFound are re-exported so consumers of this
// ownership port (the realtime gateway in interfaces/ws) can recognise the
// "not found ≡ not owned" outcome WITHOUT importing domain/task directly
// (AGENTS.md §4.1 layering — interfaces depend on application, not domain).
var (
	ErrTaskNotFound    = domain.ErrTaskNotFound
	ErrVersionNotFound = domain.ErrVersionNotFound
)

// OwnershipChecker is the application-layer port the realtime gateway uses to
// authorize a subscribe at subscribe time (design D5). It folds the bare
// tenant/user ids into a domain.Owner — the same boundary idiom as the read
// services — and returns the domain sentinels so "missing" and "owned by
// someone else" stay indistinguishable (no existence leak). Ownership is
// resolved ONCE per topic here; the fan-out hot path then trusts the
// subscription set with no per-event DB lookup.
type OwnershipChecker struct {
	Domain *domain.ReadService
}

// NewOwnershipChecker constructs the port over the domain read service (which
// holds the queries-only seam used by OwnsTask / OwnsVersion).
func NewOwnershipChecker(d *domain.ReadService) *OwnershipChecker {
	return &OwnershipChecker{Domain: d}
}

// OwnsTask returns nil when (tenantID,userID) owns taskID, ErrTaskNotFound when
// the task is missing or owned by another caller, or a DB error otherwise.
func (c *OwnershipChecker) OwnsTask(ctx context.Context, tenantID, userID, taskID uuid.UUID) error {
	return c.Domain.OwnsTask(ctx, domain.Owner{TenantID: tenantID, UserID: userID}, taskID)
}

// OwnsVersion returns nil when (tenantID,userID) owns versionID, ErrVersionNotFound
// when missing or owned by another caller, or a DB error otherwise.
func (c *OwnershipChecker) OwnsVersion(ctx context.Context, tenantID, userID, versionID uuid.UUID) error {
	return c.Domain.OwnsVersion(ctx, domain.Owner{TenantID: tenantID, UserID: userID}, versionID)
}
