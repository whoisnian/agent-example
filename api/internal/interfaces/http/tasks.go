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

// activeVersionConflictData populates the `data` block on 409 responses.
type activeVersionConflictData struct {
	ActiveVersionID     uuid.UUID `json:"active_version_id"`
	ActiveVersionStatus string    `json:"active_version_status"`
}

// TaskHandlers groups dependencies for the two write endpoints.
type TaskHandlers struct {
	App           *apptask.Service
	Logger        *slog.Logger
	Metrics       *observability.Metrics
	DevTenantID   uuid.UUID
	DevUserID     uuid.UUID
}

// RegisterTaskRoutes mounts POST /api/v1/tasks and POST /api/v1/tasks/{task_id}/iterate.
func (h *TaskHandlers) Register(r *gin.RouterGroup) {
	r.POST("/tasks", h.createTask)
	r.POST("/tasks/:task_id/iterate", h.iterateTask)
}

// createTask handles POST /api/v1/tasks.
func (h *TaskHandlers) createTask(c *gin.Context) {
	var req CreateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.writeInvalidInput(c, "body", "must be valid JSON")
		return
	}

	cmd := apptask.CreateTaskCommand{
		TenantID: h.DevTenantID,
		UserID:   h.DevUserID,
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
	case errors.As(err, new(*taskdomain.ErrInvalidInput)):
		return "invalid"
	default:
		return "error"
	}
}
