package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	apptask "github.com/whoisnian/agent-example/api/internal/application/task"
	taskdomain "github.com/whoisnian/agent-example/api/internal/domain/task"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// TaskCostHandlers groups dependencies for the four cost-read endpoints.
// Mirrors TaskReadHandlers structurally — owner-scoped 404, decimal-string
// money, unified envelope.
type TaskCostHandlers struct {
	App    *apptask.CostReadService
	Logger *slog.Logger
	// NowFn lets tests inject a fixed clock for the /me/cost default window
	// (S7). Production wires time.Now.
	NowFn func() time.Time
}

// Register mounts the four owner-scoped GET routes.
func (h *TaskCostHandlers) Register(r *gin.RouterGroup) {
	r.GET("/tasks/:task_id/cost", h.getTaskCost)
	r.GET("/versions/:version_id/cost", h.getVersionCost)
	r.GET("/me/cost", h.getOwnerCost)
	r.GET("/pricing", h.listPricing)
}

// getTaskCost handles GET /api/v1/tasks/:task_id/cost.
func (h *TaskCostHandlers) getTaskCost(c *gin.Context) {
	taskID, ok := parseUUIDParam(c, "task_id")
	if !ok {
		return
	}
	p, ok := principalOrAbort(c)
	if !ok {
		return
	}
	res, err := h.App.GetTaskCost(c.Request.Context(), p.TenantID, p.UserID, taskID)
	if err != nil {
		h.handleError(c, err)
		return
	}
	OK(c, res)
}

// getVersionCost handles GET /api/v1/versions/:version_id/cost.
func (h *TaskCostHandlers) getVersionCost(c *gin.Context) {
	versionID, ok := parseUUIDParam(c, "version_id")
	if !ok {
		return
	}
	p, ok := principalOrAbort(c)
	if !ok {
		return
	}
	res, err := h.App.GetVersionCost(c.Request.Context(), p.TenantID, p.UserID, versionID)
	if err != nil {
		h.handleError(c, err)
		return
	}
	OK(c, res)
}

// getOwnerCost handles GET /api/v1/me/cost. Validates group_by, parses + caps
// the optional time window, applies the defaulting rules, dispatches to the
// totals-or-grouped service method based on whether group_by was provided.
func (h *TaskCostHandlers) getOwnerCost(c *gin.Context) {
	rawGroup := c.Query("group_by")
	groupBy, err := taskdomain.ParseGroupBy(rawGroup)
	if err != nil {
		writeInvalidInputField(c, "group_by", "must be one of day/task_type/model")
		return
	}

	from, ok := parseRFC3339Query(c, "from")
	if !ok {
		return
	}
	to, ok := parseRFC3339Query(c, "to")
	if !ok {
		return
	}

	if err := taskdomain.ValidateWindow(groupBy, from, to); err != nil {
		// `to` carries the misuse-of-window blame in both cases (from>=to
		// and window-cap-exceeded) so the front-end gets a single
		// actionable field reference. Matches spec scenarios.
		writeInvalidInputField(c, "to", err.Error())
		return
	}

	from, to = taskdomain.ApplyWindowDefaults(groupBy, from, to, h.now)

	p, ok := principalOrAbort(c)
	if !ok {
		return
	}

	ctx := c.Request.Context()
	if groupBy == "" {
		res, err := h.App.GetOwnerCostTotal(ctx, p.TenantID, p.UserID, from, to)
		if err != nil {
			h.handleError(c, err)
			return
		}
		OK(c, res)
		return
	}

	res, err := h.App.GetOwnerCostGrouped(ctx, p.TenantID, p.UserID, groupBy, from, to)
	if err != nil {
		h.handleError(c, err)
		return
	}
	OK(c, res)
}

// listPricing handles GET /api/v1/pricing. Owner-agnostic — does not consult
// the caller principal. Every authenticated caller receives the same body.
func (h *TaskCostHandlers) listPricing(c *gin.Context) {
	res, err := h.App.ListPricing(c.Request.Context())
	if err != nil {
		h.handleError(c, err)
		return
	}
	OK(c, res)
}

// handleError mirrors TaskReadHandlers.handleError. Kept as a method (not a
// shared free function) so the structured-log tag stays handler-specific.
func (h *TaskCostHandlers) handleError(c *gin.Context, err error) {
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
	h.Logger.LogAttrs(c.Request.Context(), level, "task_cost_read_error", attrs...)
	Error(c, status, code, message)
}

// now resolves the handler's clock — production passes time.Now via NowFn,
// tests inject a fixed clock so default-window assertions are deterministic.
func (h *TaskCostHandlers) now() time.Time {
	if h.NowFn != nil {
		return h.NowFn()
	}
	return time.Now()
}

// parseRFC3339Query parses an optional RFC3339 query value. Empty → (nil,
// true). Malformed → writes 400 invalid_input naming the field and returns
// (nil, false) so the caller bails.
func parseRFC3339Query(c *gin.Context, field string) (*time.Time, bool) {
	raw := c.Query(field)
	if raw == "" {
		return nil, true
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		writeInvalidInputField(c, field, "must be RFC3339 timestamp")
		return nil, false
	}
	return &t, true
}
