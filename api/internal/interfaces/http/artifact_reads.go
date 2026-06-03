package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	apptask "github.com/whoisnian/agent-example/api/internal/application/task"
	taskdomain "github.com/whoisnian/agent-example/api/internal/domain/task"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// ArtifactHandlers groups dependencies for the two artifact-read endpoints.
// Mirrors TaskReadHandlers / TaskCostHandlers — owner-scoped 404, unified
// envelope — and carries Metrics because presign is an external (OSS) call.
type ArtifactHandlers struct {
	App     *apptask.ArtifactReadService
	Logger  *slog.Logger
	Metrics *observability.Metrics
}

// Register mounts the two owner-scoped GET routes. The `:version_id` /
// `:artifact_id` wildcards stay consistent with the other read routes.
func (h *ArtifactHandlers) Register(r *gin.RouterGroup) {
	r.GET("/versions/:version_id/artifacts", h.listVersionArtifacts)
	r.GET("/artifacts/:artifact_id/presign", h.presignArtifact)
}

// listVersionArtifacts handles GET /api/v1/versions/:version_id/artifacts.
func (h *ArtifactHandlers) listVersionArtifacts(c *gin.Context) {
	versionID, ok := parseUUIDParam(c, "version_id")
	if !ok {
		return
	}
	p, ok := principalOrAbort(c)
	if !ok {
		return
	}
	res, err := h.App.ListVersionArtifacts(c.Request.Context(), p.TenantID, p.UserID, versionID)
	if err != nil {
		h.handleError(c, err)
		return
	}
	OK(c, res)
}

// presignArtifact handles GET /api/v1/artifacts/:artifact_id/presign. It bumps
// OSSPresignTotal for every delivery that actually reached the OSS presigner:
// `success` on a minted URL, `error` on a presign failure (→ 500). A 404
// (artifact missing / unowned) never reaches the presigner, so it is not
// counted as an OSS interaction.
func (h *ArtifactHandlers) presignArtifact(c *gin.Context) {
	artifactID, ok := parseUUIDParam(c, "artifact_id")
	if !ok {
		return
	}
	p, ok := principalOrAbort(c)
	if !ok {
		return
	}
	res, err := h.App.PresignArtifact(c.Request.Context(), p.TenantID, p.UserID, artifactID)
	if err != nil {
		if !errors.Is(err, taskdomain.ErrArtifactNotFound) {
			h.Metrics.OSSPresignTotal.WithLabelValues("error").Inc()
		}
		h.handleError(c, err)
		return
	}
	h.Metrics.OSSPresignTotal.WithLabelValues("success").Inc()
	OK(c, res)
}

// handleError mirrors the other read handlers. Logs the offending id (never
// oss_key, never credentials) and the trace id; only 5xx attach the raw err.
func (h *ArtifactHandlers) handleError(c *gin.Context, err error) {
	status, code, message := MapError(err)
	attrs := []slog.Attr{
		slog.String("code", code),
		slog.Int("status", status),
		slog.String("trace_id", observability.TraceIDFromContext(c.Request.Context())),
	}
	if v := c.Param("version_id"); v != "" {
		attrs = append(attrs, slog.String("version_id", v))
	}
	if v := c.Param("artifact_id"); v != "" {
		attrs = append(attrs, slog.String("artifact_id", v))
	}
	level := slog.LevelInfo
	if status >= http.StatusInternalServerError {
		level = slog.LevelError
		attrs = append(attrs, slog.String("err", err.Error()))
	}
	h.Logger.LogAttrs(c.Request.Context(), level, "artifact_read_error", attrs...)
	Error(c, status, code, message)
}
