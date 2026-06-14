package task

import (
	"context"

	"github.com/google/uuid"

	domain "github.com/whoisnian/agent-example/api/internal/domain/task"
)

// DeleteService wraps the domain DeleteService, folding the bare
// (tenantID, userID) from the HTTP layer into domain.Owner — mirroring the
// existing application/task.ControlService idiom (add-task-deletion).
type DeleteService struct {
	Domain *domain.DeleteService
}

// NewDeleteService constructs the application delete service.
func NewDeleteService(d *domain.DeleteService) *DeleteService {
	return &DeleteService{Domain: d}
}

// SoftDelete soft-deletes a task the caller owns. Errors pass through verbatim
// (ErrTaskNotFound / *ErrActiveVersionExists) for the HTTP layer to map.
func (s *DeleteService) SoftDelete(ctx context.Context, tenantID, userID, taskID uuid.UUID) error {
	return s.Domain.SoftDelete(ctx, domain.Owner{TenantID: tenantID, UserID: userID}, taskID)
}
