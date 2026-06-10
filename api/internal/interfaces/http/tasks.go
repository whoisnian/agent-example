package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	apptask "github.com/whoisnian/agent-example/api/internal/application/task"
	taskdomain "github.com/whoisnian/agent-example/api/internal/domain/task"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// CreateTaskRequest is the JSON body for POST /api/v1/tasks. We deliberately
// avoid `binding` validators here so the domain layer remains the single
// source of truth for field-level rules; the handler converts only the
// shape-level errors itself.
type CreateTaskRequest struct {
	// Title is optional; absent / blank → the domain derives it from Prompt.
	Title    string          `json:"title"`
	TaskType string          `json:"task_type"`
	Prompt   string          `json:"prompt"`
	Params   json.RawMessage `json:"params"`
	Lane     *string         `json:"lane"`
}

// CreateTaskResponse is the success envelope's `data` block.
type CreateTaskResponse struct {
	TaskID    uuid.UUID `json:"task_id"`
	VersionID uuid.UUID `json:"version_id"`
	VersionNo int32     `json:"version_no"`
	Status    string    `json:"status"`
}

// IterateTaskRequest is the JSON body for POST /api/v1/tasks/{task_id}/iterate.
type IterateTaskRequest struct {
	BaseVersionID *string         `json:"base_version_id"`
	Prompt        string          `json:"prompt"`
	Params        json.RawMessage `json:"params"`
	Lane          *string         `json:"lane"`
}

// IterateTaskResponse is the success envelope's `data` block.
type IterateTaskResponse struct {
	VersionID uuid.UUID `json:"version_id"`
	VersionNo int32     `json:"version_no"`
	Status    string    `json:"status"`
}

// RollbackTaskRequest is the JSON body for POST /api/v1/tasks/{task_id}/rollback.
type RollbackTaskRequest struct {
	TargetVersionID string          `json:"target_version_id"`
	Mode            string          `json:"mode"`
	Prompt          string          `json:"prompt"`
	Params          json.RawMessage `json:"params"`
	Lane            *string         `json:"lane"`
}

// RollbackBranchResponse is the 201 `data` block for mode=branch (a new version).
type RollbackBranchResponse struct {
	VersionID uuid.UUID `json:"version_id"`
	VersionNo int32     `json:"version_no"`
	Status    string    `json:"status"`
}

// RollbackSwitchResponse is the 200 `data` block for mode=switch (pointer move).
type RollbackSwitchResponse struct {
	CurrentVersionID uuid.UUID `json:"current_version_id"`
	VersionNo        int32     `json:"version_no"`
	Status           string    `json:"status"`
}

// activeVersionConflictData populates the `data` block on 409 responses.
type activeVersionConflictData struct {
	ActiveVersionID     uuid.UUID `json:"active_version_id"`
	ActiveVersionStatus string    `json:"active_version_status"`
}

// TaskHandlers groups dependencies for the two write endpoints.
type TaskHandlers struct {
	App     *apptask.Service
	Logger  *slog.Logger
	Metrics *observability.Metrics
}

// RegisterTaskRoutes mounts POST /api/v1/tasks, /iterate, and /rollback.
func (h *TaskHandlers) Register(r *gin.RouterGroup) {
	r.POST("/tasks", h.createTask)
	r.POST("/tasks/:task_id/iterate", h.iterateTask)
	r.POST("/tasks/:task_id/rollback", h.rollbackTask)
}

// rollbackModeUnknown labels metric increments taken before mode is parsed.
const rollbackModeUnknown = "unknown"

// createTask handles POST /api/v1/tasks.
func (h *TaskHandlers) createTask(c *gin.Context) {
	var req CreateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.writeInvalidInput(c, "body", "must be valid JSON")
		return
	}

	p, ok := principalOrAbort(c)
	if !ok {
		return
	}

	cmd := apptask.CreateTaskCommand{
		TenantID: p.TenantID,
		UserID:   p.UserID,
		Title:    req.Title,
		TaskType: req.TaskType,
		Prompt:   req.Prompt,
		Params:   req.Params,
		Lane:     req.Lane,
	}

	res, err := h.App.CreateTask(c.Request.Context(), cmd)
	if err != nil {
		h.handleError(c, err)
		return
	}

	h.Logger.LogAttrs(c.Request.Context(), slog.LevelInfo, "task_create_ok",
		slog.String("task_id", res.TaskID.String()),
		slog.String("version_id", res.VersionID.String()),
		slog.String("task_type", req.TaskType),
	)
	h.Metrics.TasksCreatedTotal.WithLabelValues(req.TaskType).Inc()

	c.JSON(http.StatusCreated, Envelope{
		Code:    0,
		Message: "ok",
		Data: CreateTaskResponse{
			TaskID:    res.TaskID,
			VersionID: res.VersionID,
			VersionNo: res.VersionNo,
			Status:    res.Status,
		},
		TraceID: observability.TraceIDFromContext(c.Request.Context()),
	})
}

// iterateTask handles POST /api/v1/tasks/:task_id/iterate.
func (h *TaskHandlers) iterateTask(c *gin.Context) {
	taskIDStr := c.Param("task_id")
	taskID, err := uuid.Parse(taskIDStr)
	if err != nil {
		h.writeInvalidInput(c, "task_id", "must be a valid UUID")
		h.Metrics.TasksIteratedTotal.WithLabelValues("invalid").Inc()
		return
	}

	var req IterateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.writeInvalidInput(c, "body", "must be valid JSON")
		h.Metrics.TasksIteratedTotal.WithLabelValues("invalid").Inc()
		return
	}

	var baseVersionID *uuid.UUID
	if req.BaseVersionID != nil && *req.BaseVersionID != "" {
		parsed, parseErr := uuid.Parse(*req.BaseVersionID)
		if parseErr != nil {
			h.writeInvalidInput(c, "base_version_id", "must be a valid UUID")
			h.Metrics.TasksIteratedTotal.WithLabelValues("invalid").Inc()
			return
		}
		baseVersionID = &parsed
	}

	cmd := apptask.IterateTaskCommand{
		TaskID:        taskID,
		BaseVersionID: baseVersionID,
		Prompt:        req.Prompt,
		Params:        req.Params,
		Lane:          req.Lane,
	}

	res, err := h.App.IterateTask(c.Request.Context(), cmd)
	if err != nil {
		h.handleError(c, err)
		h.Metrics.TasksIteratedTotal.WithLabelValues(outcomeLabel(err)).Inc()
		var ave *taskdomain.ErrActiveVersionExists
		if errors.As(err, &ave) {
			h.Logger.LogAttrs(c.Request.Context(), slog.LevelInfo, "task_iterate_conflict",
				slog.String("task_id", taskID.String()),
				slog.String("active_version_id", ave.ActiveVersionID.String()),
				slog.String("active_version_status", ave.ActiveVersionStatus),
			)
		}
		return
	}

	h.Logger.LogAttrs(c.Request.Context(), slog.LevelInfo, "task_iterate_ok",
		slog.String("task_id", taskID.String()),
		slog.String("version_id", res.VersionID.String()),
		slog.Int("version_no", int(res.VersionNo)),
	)
	h.Metrics.TasksIteratedTotal.WithLabelValues("success").Inc()

	c.JSON(http.StatusCreated, Envelope{
		Code:    0,
		Message: "ok",
		Data: IterateTaskResponse{
			VersionID: res.VersionID,
			VersionNo: res.VersionNo,
			Status:    res.Status,
		},
		TraceID: observability.TraceIDFromContext(c.Request.Context()),
	})
}

// rollbackTask handles POST /api/v1/tasks/:task_id/rollback. Both modes share
// the owner-scoped, non-active precondition; branch returns 201 (new version),
// switch returns 200 (pointer move). The metric is bumped on every exit path
// with both labels — pre-mode-parse 400s use mode="unknown".
func (h *TaskHandlers) rollbackTask(c *gin.Context) {
	mode := rollbackModeUnknown
	outcome := "invalid"
	defer func() {
		h.Metrics.TasksRolledBackTotal.WithLabelValues(mode, outcome).Inc()
	}()

	taskID, ok := parseUUIDParam(c, "task_id")
	if !ok {
		return // parseUUIDParam wrote the 400; mode stays "unknown"
	}

	var req RollbackTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.writeInvalidInput(c, "body", "must be valid JSON")
		return
	}

	targetVersionID, err := uuid.Parse(req.TargetVersionID)
	if err != nil {
		h.writeInvalidInput(c, "target_version_id", "must be a valid UUID")
		return
	}

	if !taskdomain.IsValidRollbackMode(req.Mode) {
		h.writeInvalidInput(c, "mode", "must be one of branch/switch")
		return
	}
	mode = req.Mode

	p, ok := principalOrAbort(c)
	if !ok {
		return
	}

	res, err := h.App.RollbackTask(c.Request.Context(), apptask.RollbackTaskCommand{
		TenantID:        p.TenantID,
		UserID:          p.UserID,
		TaskID:          taskID,
		TargetVersionID: targetVersionID,
		Mode:            req.Mode,
		Prompt:          req.Prompt,
		Params:          req.Params,
		Lane:            req.Lane,
	})
	if err != nil {
		h.handleError(c, err)
		outcome = outcomeLabel(err)
		var ave *taskdomain.ErrActiveVersionExists
		if errors.As(err, &ave) {
			h.Logger.LogAttrs(c.Request.Context(), slog.LevelInfo, "task_rollback_conflict",
				slog.String("task_id", taskID.String()),
				slog.String("mode", mode),
				slog.String("active_version_id", ave.ActiveVersionID.String()),
				slog.String("active_version_status", ave.ActiveVersionStatus),
			)
		}
		return
	}

	outcome = "success"
	h.Logger.LogAttrs(c.Request.Context(), slog.LevelInfo, "task_rollback_ok",
		slog.String("task_id", taskID.String()),
		slog.String("mode", mode),
		slog.String("version_id", res.VersionID.String()),
		slog.Int("version_no", int(res.VersionNo)),
	)

	if res.Mode == string(taskdomain.RollbackBranch) {
		JSON(c, http.StatusCreated, RollbackBranchResponse{
			VersionID: res.VersionID,
			VersionNo: res.VersionNo,
			Status:    res.Status,
		})
		return
	}
	JSON(c, http.StatusOK, RollbackSwitchResponse{
		CurrentVersionID: res.VersionID,
		VersionNo:        res.VersionNo,
		Status:           res.Status,
	})
}

// handleError renders a domain error to the unified envelope. The
// `active_version_exists` case carries a populated `data` block so the client
// can show "v2 is currently running" without a follow-up request.
func (h *TaskHandlers) handleError(c *gin.Context, err error) {
	status, code, message := MapError(err)
	var ave *taskdomain.ErrActiveVersionExists
	if errors.As(err, &ave) {
		c.AbortWithStatusJSON(status, Envelope{
			Code:    code,
			Message: message,
			Data: activeVersionConflictData{
				ActiveVersionID:     ave.ActiveVersionID,
				ActiveVersionStatus: ave.ActiveVersionStatus,
			},
			TraceID: observability.TraceIDFromContext(c.Request.Context()),
		})
		return
	}
	Error(c, status, code, message)
}

// writeInvalidInput sends a 400 with the offending field surfaced in `data`.
func (h *TaskHandlers) writeInvalidInput(c *gin.Context, field, reason string) {
	c.AbortWithStatusJSON(http.StatusBadRequest, Envelope{
		Code:    "invalid_input",
		Message: "invalid_input: " + field + ": " + reason,
		Data:    map[string]string{"field": field, "reason": reason},
		TraceID: observability.TraceIDFromContext(c.Request.Context()),
	})
}

// outcomeLabel buckets iterate-task errors for the metric.
func outcomeLabel(err error) string {
	switch {
	case errors.Is(err, taskdomain.ErrTaskNotFound):
		return "not_found"
	case errors.Is(err, taskdomain.ErrVersionNotFound):
		return "not_found"
	case errors.As(err, new(*taskdomain.ErrActiveVersionExists)):
		return "conflict"
	case errors.Is(err, taskdomain.ErrInvalidState):
		return "conflict"
	case errors.As(err, new(*taskdomain.ErrInvalidInput)):
		return "invalid"
	default:
		return "error"
	}
}
