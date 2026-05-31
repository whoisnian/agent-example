package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	apptask "github.com/whoisnian/agent-example/api/internal/application/task"
	taskdomain "github.com/whoisnian/agent-example/api/internal/domain/task"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// TaskControlHandlers serves POST /api/v1/tasks/{task_id}/control.
type TaskControlHandlers struct {
	App         *apptask.ControlService
	Logger      *slog.Logger
	Metrics     *observability.Metrics
	DevTenantID uuid.UUID
	DevUserID   uuid.UUID
}

// controlRequest is the JSON body shape.
type controlRequest struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
}

// controlResponse is the 202 body. Reviewer S7: outbox_id is deliberately
// NOT exposed (internal detail; access-log carries it for correlation).
type controlResponse struct {
	Accepted  bool      `json:"accepted"`
	Action    string    `json:"action"`
	TaskID    uuid.UUID `json:"task_id"`
	Effective string    `json:"effective"` // "queued" | "best_effort" (S9)
}

const (
	effectiveQueued     = "queued"
	effectiveBestEffort = "best_effort"

	// Metric label values for unparseable actions (reviewer S15).
	actionUnknown = "unknown"
)

// Register mounts the control route.
func (h *TaskControlHandlers) Register(r *gin.RouterGroup) {
	r.POST("/tasks/:task_id/control", h.postControl)
}

// postControl handles POST /api/v1/tasks/{task_id}/control.
func (h *TaskControlHandlers) postControl(c *gin.Context) {
	// We track the action label across all bump-paths so the deferred
	// observation can pick it up regardless of which exit we take.
	action := actionUnknown
	outcome := "invalid"
	defer func() {
		h.Metrics.TaskControlRequestsTotal.WithLabelValues(action, outcome).Inc()
	}()

	taskID, ok := parseUUIDParam(c, "task_id")
	if !ok {
		// parseUUIDParam already wrote the 400 envelope; action stays "unknown"
		return
	}

	// Read + decode body. Empty body is "missing action" -> 400.
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		writeInvalidInputField(c, "body", "failed to read request body")
		return
	}
	var req controlRequest
	if len(body) == 0 {
		writeInvalidInputField(c, "action", "must be one of pause/resume/cancel")
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeInvalidInputField(c, "body", "invalid JSON")
		return
	}

	if !taskdomain.IsValidControlAction(req.Action) {
		writeInvalidInputField(c, "action", "must be one of pause/resume/cancel")
		return
	}
	action = req.Action

	// Reason validation: trim trailing whitespace, cap to MaxControlReasonLen
	// (matches task.title cap; reviewer S13).
	reason := strings.TrimRight(req.Reason, " \t\n\r")
	if len(reason) > taskdomain.MaxControlReasonLen {
		writeInvalidInputField(c, "reason", "exceeds 200 characters")
		return
	}

	res, err := h.App.Apply(c.Request.Context(), h.DevTenantID, h.DevUserID, taskID, taskdomain.ControlAction(req.Action), reason)
	if err != nil {
		switch {
		case errors.Is(err, taskdomain.ErrTaskNotFound):
			outcome = "not_found"
			h.logAt(c, slog.LevelInfo, "task_control_not_found", taskID, action, err)
			Error(c, http.StatusNotFound, "task_not_found", "task not found")
		case errors.Is(err, taskdomain.ErrInvalidState):
			outcome = "conflict"
			h.logAt(c, slog.LevelInfo, "task_control_invalid_state", taskID, action, err)
			Error(c, http.StatusConflict, "invalid_state", err.Error())
		default:
			outcome = "invalid" // 500 path also flagged invalid in metrics; the log call captures the err
			h.logAt(c, slog.LevelError, "task_control_failed", taskID, action, err)
			Error(c, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	outcome = "accepted"
	effective := effectiveBestEffort
	if res.HasActiveRun {
		effective = effectiveQueued
	}
	h.Logger.LogAttrs(c.Request.Context(), slog.LevelInfo, "task_control_accepted",
		slog.String("task_id", taskID.String()),
		slog.String("action", action),
		slog.String("effective", effective),
		slog.Int64("outbox_id", res.OutboxID),
		slog.String("trace_id", observability.TraceIDFromContext(c.Request.Context())),
	)
	JSON(c, http.StatusAccepted, controlResponse{
		Accepted:  true,
		Action:    action,
		TaskID:    taskID,
		Effective: effective,
	})
}

func (h *TaskControlHandlers) logAt(c *gin.Context, level slog.Level, msg string, taskID uuid.UUID, action string, err error) {
	h.Logger.LogAttrs(c.Request.Context(), level, msg,
		slog.String("task_id", taskID.String()),
		slog.String("action", action),
		slog.String("trace_id", observability.TraceIDFromContext(c.Request.Context())),
		slog.String("err", err.Error()),
	)
}
