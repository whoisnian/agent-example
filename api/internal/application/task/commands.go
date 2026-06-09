// Package task is the application layer for task-write-api. It exists to
// translate HTTP-shaped commands into domain inputs and dispatch them to the
// Domain Service. No business logic lives here.
package task

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	domain "github.com/whoisnian/agent-example/api/internal/domain/task"
)

// CreateTaskCommand mirrors the public POST /api/v1/tasks request body plus
// the principal fields the auth middleware will eventually inject.
type CreateTaskCommand struct {
	TenantID uuid.UUID
	UserID   uuid.UUID
	Title    string
	TaskType string
	Prompt   string
	Params   json.RawMessage
	Lane     *string
}

// IterateTaskCommand mirrors POST /api/v1/tasks/{task_id}/iterate.
type IterateTaskCommand struct {
	TaskID        uuid.UUID
	BaseVersionID *uuid.UUID
	Prompt        string
	Params        json.RawMessage
	Lane          *string
}

// CreateTaskResult is the application-level response shape the handler then
// renders into the envelope `data` block.
type CreateTaskResult struct {
	TaskID    uuid.UUID
	VersionID uuid.UUID
	VersionNo int32
	Status    string
}

// IterateTaskResult mirrors the iterate response.
type IterateTaskResult struct {
	VersionID uuid.UUID
	VersionNo int32
	Status    string
}

// RollbackTaskCommand mirrors POST /api/v1/tasks/{task_id}/rollback plus the
// principal fields the auth middleware injects. Mode is "branch" | "switch".
type RollbackTaskCommand struct {
	TenantID        uuid.UUID
	UserID          uuid.UUID
	TaskID          uuid.UUID
	TargetVersionID uuid.UUID
	Mode            string
	Prompt          string
	Params          json.RawMessage
	Lane            *string
}

// RollbackTaskResult mirrors the rollback response. For branch, VersionID is
// the new version; for switch it is the now-current target version.
type RollbackTaskResult struct {
	VersionID uuid.UUID
	VersionNo int32
	Status    string
	Mode      string
}

// Service is the application-layer wrapper around the domain Service. The
// struct stays open for adding cross-cutting concerns (metrics, audit) later
// without forcing handlers to touch the domain package directly.
type Service struct {
	Domain *domain.Service
}

// NewService constructs the application service.
func NewService(d *domain.Service) *Service { return &Service{Domain: d} }

// CreateTask forwards to domain.Service.CreateTask.
//
//nolint:gocritic // hugeParam: value semantics intentional for an input command; the struct is read-only.
func (s *Service) CreateTask(ctx context.Context, cmd CreateTaskCommand) (CreateTaskResult, error) {
	out, err := s.Domain.CreateTask(ctx, domain.CreateInput{
		TenantID: cmd.TenantID,
		UserID:   cmd.UserID,
		Title:    cmd.Title,
		TaskType: cmd.TaskType,
		Prompt:   cmd.Prompt,
		Params:   cmd.Params,
		Lane:     cmd.Lane,
	})
	if err != nil {
		return CreateTaskResult{}, err
	}
	return CreateTaskResult{
		TaskID:    out.TaskID,
		VersionID: out.VersionID,
		VersionNo: out.VersionNo,
		Status:    string(out.Status),
	}, nil
}

// IterateTask forwards to domain.Service.IterateTask.
func (s *Service) IterateTask(ctx context.Context, cmd IterateTaskCommand) (IterateTaskResult, error) {
	out, err := s.Domain.IterateTask(ctx, domain.IterateInput{
		TaskID:        cmd.TaskID,
		BaseVersionID: cmd.BaseVersionID,
		Prompt:        cmd.Prompt,
		Params:        cmd.Params,
		Lane:          cmd.Lane,
	})
	if err != nil {
		return IterateTaskResult{}, err
	}
	return IterateTaskResult{
		VersionID: out.VersionID,
		VersionNo: out.VersionNo,
		Status:    string(out.Status),
	}, nil
}

// RollbackTask folds the principal into a domain.Owner and forwards to
// domain.Service.RollbackTask.
//
//nolint:gocritic // hugeParam: value semantics intentional for an input command; the struct is read-only.
func (s *Service) RollbackTask(ctx context.Context, cmd RollbackTaskCommand) (RollbackTaskResult, error) {
	out, err := s.Domain.RollbackTask(ctx, domain.Owner{TenantID: cmd.TenantID, UserID: cmd.UserID}, domain.RollbackInput{
		TaskID:          cmd.TaskID,
		TargetVersionID: cmd.TargetVersionID,
		Mode:            domain.RollbackMode(cmd.Mode),
		Prompt:          cmd.Prompt,
		Params:          cmd.Params,
		Lane:            cmd.Lane,
	})
	if err != nil {
		return RollbackTaskResult{}, err
	}
	return RollbackTaskResult{
		VersionID: out.VersionID,
		VersionNo: out.VersionNo,
		Status:    string(out.Status),
		Mode:      string(out.Mode),
	}, nil
}
