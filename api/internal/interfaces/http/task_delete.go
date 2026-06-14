package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	apptask "github.com/whoisnian/agent-example/api/internal/application/task"
	taskdomain "github.com/whoisnian/agent-example/api/internal/domain/task"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// TaskDeleteHandlers serves DELETE /api/v1/tasks/{task_id} (add-task-deletion).
type TaskDeleteHandlers struct {
	App     *apptask.DeleteService
	Logger  *slog.Logger
	Metrics *observability.Metrics
}

// deleteResponse is the 200 body for a successful soft delete.
type deleteResponse struct {
	Deleted bool      `json:"deleted"`
	TaskID  uuid.UUID `json:"task_id"`
}

// Register mounts the delete route.
func (h *TaskDeleteHandlers) Register(r *gin.RouterGroup) {
	r.DELETE("/tasks/:task_id", h.deleteTask)
}

// deleteTask handles DELETE /api/v1/tasks/{task_id}.
func (h *TaskDeleteHandlers) deleteTask(c *gin.Context) {
	outcome := "invalid"
	defer func() { h.Metrics.TaskDeletedTotal.WithLabelValues(outcome).Inc() }()

	taskID, ok := parseUUIDParam(c, "task_id")
	if !ok {
		return // parseUUIDParam wrote the 400
	}

	p, ok := principalOrAbort(c)
	if !ok {
		return
	}

	err := h.App.SoftDelete(c.Request.Context(), p.TenantID, p.UserID, taskID)
	if err != nil {
		var ave *taskdomain.ErrActiveVersionExists
		switch {
		case errors.As(err, &ave):
			outcome = "conflict"
			h.logAt(c, slog.LevelInfo, "task_delete_active", taskID, err)
			c.AbortWithStatusJSON(http.StatusConflict, Envelope{
				Code:    "active_version_exists",
				Message: ave.Error(),
				Data: activeVersionConflictData{
					ActiveVersionID:     ave.ActiveVersionID,
					ActiveVersionStatus: ave.ActiveVersionStatus,
				},
				TraceID: observability.TraceIDFromContext(c.Request.Context()),
			})
		case errors.Is(err, taskdomain.ErrTaskNotFound):
			outcome = "not_found"
			h.logAt(c, slog.LevelInfo, "task_delete_not_found", taskID, err)
			Error(c, http.StatusNotFound, "task_not_found", "task not found")
		default:
			outcome = "invalid"
			h.logAt(c, slog.LevelError, "task_delete_failed", taskID, err)
			Error(c, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	outcome = "deleted"
	h.Logger.LogAttrs(c.Request.Context(), slog.LevelInfo, "task_delete_ok",
		slog.String("task_id", taskID.String()),
		slog.String("trace_id", observability.TraceIDFromContext(c.Request.Context())),
	)
	JSON(c, http.StatusOK, deleteResponse{Deleted: true, TaskID: taskID})
}

func (h *TaskDeleteHandlers) logAt(c *gin.Context, level slog.Level, msg string, taskID uuid.UUID, err error) {
	h.Logger.LogAttrs(c.Request.Context(), level, msg,
		slog.String("task_id", taskID.String()),
		slog.String("trace_id", observability.TraceIDFromContext(c.Request.Context())),
		slog.String("err", err.Error()),
	)
}
