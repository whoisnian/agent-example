package httpapi

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	apptask "github.com/whoisnian/agent-example/api/internal/application/task"
	taskdomain "github.com/whoisnian/agent-example/api/internal/domain/task"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// Defaults for absent pagination / cursor query params. Upper bounds and
// lower-clamping live in the domain ReadService (design D3 / D7).
const (
	defaultListPage = 1
	defaultPageSize = 20
	defaultEventLim = 200
	defaultAfterID  = int64(0)
)

// TaskReadHandlers groups dependencies for the five read endpoints. It mirrors
// the write-side TaskHandlers but depends on the application ReadService and
// carries no Metrics (reads are not state transitions; see design D10).
type TaskReadHandlers struct {
	App         *apptask.ReadService
	Logger      *slog.Logger
	DevTenantID uuid.UUID
	DevUserID   uuid.UUID
}

// Register mounts the five owner-scoped GET routes. Paths share the `:task_id`
// / `:version_id` wildcards with the write routes (consistent param names).
func (h *TaskReadHandlers) Register(r *gin.RouterGroup) {
	r.GET("/tasks", h.listTasks)
	r.GET("/tasks/:task_id", h.getTask)
	r.GET("/tasks/:task_id/versions", h.listVersions)
	r.GET("/versions/:version_id", h.getVersion)
	r.GET("/versions/:version_id/events", h.listVersionEvents)
}

// listTasks handles GET /api/v1/tasks.
func (h *TaskReadHandlers) listTasks(c *gin.Context) {
	page, ok := parseIntQuery(c, "page", defaultListPage)
	if !ok {
		writeInvalidInputField(c, "page", "must be an integer")
		return
	}
	pageSize, ok := parseIntQuery(c, "page_size", defaultPageSize)
	if !ok {
		writeInvalidInputField(c, "page_size", "must be an integer")
		return
	}
	var status *string
	if raw := c.Query("status"); raw != "" {
		if !taskdomain.IsValidTaskStatus(raw) {
			writeInvalidInputField(c, "status", "must be one of pending/running/paused/cancelled/succeeded/failed")
			return
		}
		status = &raw
	}

	res, err := h.App.ListTasks(c.Request.Context(), h.DevTenantID, h.DevUserID, page, pageSize, status)
	if err != nil {
		h.handleError(c, err)
		return
	}
	OK(c, res)
}

// getTask handles GET /api/v1/tasks/:task_id.
func (h *TaskReadHandlers) getTask(c *gin.Context) {
	taskID, ok := parseUUIDParam(c, "task_id")
	if !ok {
		return
	}
	res, err := h.App.GetTask(c.Request.Context(), h.DevTenantID, h.DevUserID, taskID)
	if err != nil {
		h.handleError(c, err)
		return
	}
	OK(c, res)
}

// listVersions handles GET /api/v1/tasks/:task_id/versions.
func (h *TaskReadHandlers) listVersions(c *gin.Context) {
	taskID, ok := parseUUIDParam(c, "task_id")
	if !ok {
		return
	}
	res, err := h.App.ListVersions(c.Request.Context(), h.DevTenantID, h.DevUserID, taskID)
	if err != nil {
		h.handleError(c, err)
		return
	}
	OK(c, res)
}

// getVersion handles GET /api/v1/versions/:version_id.
func (h *TaskReadHandlers) getVersion(c *gin.Context) {
	versionID, ok := parseUUIDParam(c, "version_id")
	if !ok {
		return
	}
	res, err := h.App.GetVersion(c.Request.Context(), h.DevTenantID, h.DevUserID, versionID)
	if err != nil {
		h.handleError(c, err)
		return
	}
	OK(c, res)
}

// listVersionEvents handles GET /api/v1/versions/:version_id/events.
func (h *TaskReadHandlers) listVersionEvents(c *gin.Context) {
	versionID, ok := parseUUIDParam(c, "version_id")
	if !ok {
		return
	}
	afterID, ok := parseInt64Query(c, "after_id", defaultAfterID)
	if !ok {
		writeInvalidInputField(c, "after_id", "must be an integer")
		return
	}
	limit, ok := parseIntQuery(c, "limit", defaultEventLim)
	if !ok {
		writeInvalidInputField(c, "limit", "must be an integer")
		return
	}

	res, err := h.App.ListVersionEvents(c.Request.Context(), h.DevTenantID, h.DevUserID, versionID, afterID, limit)
	if err != nil {
		h.handleError(c, err)
		return
	}
	OK(c, res)
}

// handleError renders a read error to the unified envelope and emits a
// structured log line carrying trace_id / task_id / version_id. 5xx logs at
// error level; expected 4xx (not-found / invalid) at info.
func (h *TaskReadHandlers) handleError(c *gin.Context, err error) {
	status, code, message := MapError(err)
	attrs := []slog.Attr{
		slog.String("code", code),
		slog.Int("status", status),
		slog.String("trace_id", observability.TraceIDFromContext(c.Request.Context())),
	}
	if v := c.Param("task_id"); v != "" {
		attrs = append(attrs, slog.String("task_id", v))
	}
	if v := c.Param("version_id"); v != "" {
		attrs = append(attrs, slog.String("version_id", v))
	}
	level := slog.LevelInfo
	if status >= http.StatusInternalServerError {
		level = slog.LevelError
		attrs = append(attrs, slog.String("err", err.Error()))
	}
	h.Logger.LogAttrs(c.Request.Context(), level, "task_read_error", attrs...)
	Error(c, status, code, message)
}

// writeInvalidInputField sends a 400 invalid_input naming the offending field,
// matching the write-side envelope shape.
func writeInvalidInputField(c *gin.Context, field, reason string) {
	c.AbortWithStatusJSON(http.StatusBadRequest, Envelope{
		Code:    "invalid_input",
		Message: "invalid_input: " + field + ": " + reason,
		Data:    map[string]string{"field": field, "reason": reason},
		TraceID: observability.TraceIDFromContext(c.Request.Context()),
	})
}

// parseUUIDParam parses a path param as a UUID, writing a 400 and returning
// ok=false on failure.
func parseUUIDParam(c *gin.Context, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		writeInvalidInputField(c, name, "must be a valid UUID")
		return uuid.Nil, false
	}
	return id, true
}

// parseIntQuery reads an int query param, returning the default when absent and
// ok=false when present but non-integer (caller renders the 400).
func parseIntQuery(c *gin.Context, name string, def int) (int, bool) {
	raw := c.Query(name)
	if raw == "" {
		return def, true
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return v, true
}

// parseInt64Query is the int64 counterpart for the after_id cursor.
func parseInt64Query(c *gin.Context, name string, def int64) (int64, bool) {
	raw := c.Query(name)
	if raw == "" {
		return def, true
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
