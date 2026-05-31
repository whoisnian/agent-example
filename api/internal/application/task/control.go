package task

import (
	"context"

	"github.com/google/uuid"

	domain "github.com/whoisnian/agent-example/api/internal/domain/task"
)

// ControlService wraps the domain ControlService. Bare (tenantID, userID)
// arrive from the HTTP layer and get folded into the single domain.Owner
// type here, mirroring the existing `application/task.ReadService` idiom.
type ControlService struct {
	Domain *domain.ControlService
}

// NewControlService constructs the application control service.
func NewControlService(d *domain.ControlService) *ControlService {
	return &ControlService{Domain: d}
}

// Apply executes the control request. The action / reason are passed through
// verbatim; the domain layer is the validation authority.
func (s *ControlService) Apply(ctx context.Context, tenantID, userID, taskID uuid.UUID, action domain.ControlAction, reason string) (*domain.ControlResult, error) {
	return s.Domain.Apply(ctx, domain.Owner{TenantID: tenantID, UserID: userID}, taskID, action, reason)
}
