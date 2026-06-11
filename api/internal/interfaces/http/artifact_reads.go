package httpapi

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	apptask "github.com/whoisnian/agent-example/api/internal/application/task"
	"github.com/whoisnian/agent-example/api/internal/auth"
	taskdomain "github.com/whoisnian/agent-example/api/internal/domain/task"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// ArtifactHandlers groups dependencies for the artifact-read endpoints.
// Mirrors TaskReadHandlers / TaskCostHandlers — owner-scoped 404, unified
// envelope — and carries Metrics because the download proxy is an external
// (OSS) call. Tokens verifies the `?token=` download grants minted by the
// presign endpoint (the download route is public to the Bearer middleware:
// <img>/<iframe>/navigation cannot send an Authorization header).
type ArtifactHandlers struct {
	App     *apptask.ArtifactReadService
	Logger  *slog.Logger
	Metrics *observability.Metrics
	Tokens  *auth.DownloadVerifier
}

// Register mounts the artifact GET routes. The `:version_id` / `:artifact_id`
// wildcards stay consistent with the other read routes; the download route's
// template string must match its publicRoutes entry exactly.
func (h *ArtifactHandlers) Register(r *gin.RouterGroup) {
	r.GET("/versions/:version_id/artifacts", h.listVersionArtifacts)
	r.GET("/artifacts/:artifact_id/presign", h.presignArtifact)
	r.GET("/artifacts/:artifact_id/download", h.downloadArtifact)
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

// downloadArtifact handles GET /api/v1/artifacts/:artifact_id/download — the
// reverse proxy that streams artifact bytes from OSS through the API
// (add-artifact-download-proxy; the browser never reaches OSS_ENDPOINT). The
// route bypasses the Bearer middleware; its sole authentication is the
// `?token=` download grant, verified here. Every token failure collapses to a
// single 403 invalid_download_token (non-enumerable, mirroring S3's 403 on an
// expired presigned URL). Ownership is NOT re-checked: the token was minted
// owner-scoped and possession is the grant. The query string (token) must
// never be logged on this route — the access log records only the path.
func (h *ArtifactHandlers) downloadArtifact(c *gin.Context) {
	artifactID, ok := parseUUIDParam(c, "artifact_id")
	if !ok {
		return
	}
	if err := h.Tokens.Parse(c.Query("token"), artifactID); err != nil {
		h.Metrics.OSSDownloadTotal.WithLabelValues("token_invalid").Inc()
		Error(c, http.StatusForbidden, "invalid_download_token", "invalid or expired download token")
		return
	}

	obj, err := h.App.OpenArtifactObject(c.Request.Context(), artifactID)
	if err != nil {
		if errors.Is(err, taskdomain.ErrArtifactNotFound) {
			h.Metrics.OSSDownloadTotal.WithLabelValues("not_found").Inc()
		} else {
			h.Metrics.OSSDownloadTotal.WithLabelValues("oss_error").Inc()
		}
		h.handleError(c, err)
		return
	}
	defer func() { _ = obj.Body.Close() }()

	// DB metadata is authoritative for the content type; the OSS-reported one
	// is never trusted. CSP `sandbox` forces an opaque origin even on top-level
	// navigation so stored HTML cannot script against the API origin
	// (allow-scripts keeps the sandboxed-iframe rendered preview working);
	// Referrer-Policy forecloses token exfiltration via document-controlled
	// referrer overrides.
	mime := "application/octet-stream"
	if obj.Mime != nil && *obj.Mime != "" {
		mime = *obj.Mime
	}
	hdr := c.Writer.Header()
	hdr.Set("Content-Type", mime)
	if obj.ContentLength != nil { // nil = unknown; 0 is a legitimate empty object
		hdr.Set("Content-Length", strconv.FormatInt(*obj.ContentLength, 10))
	}
	hdr.Set("Content-Security-Policy", "sandbox allow-scripts")
	hdr.Set("Referrer-Policy", "no-referrer")
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Cache-Control", "private, no-store")
	c.Status(http.StatusOK)

	n, err := io.Copy(c.Writer, obj.Body)
	h.Metrics.OSSDownloadBytes.Add(float64(n))
	if err != nil {
		// Headers (and n bytes) are already out — no envelope is possible.
		// Cut the connection via http.ErrAbortHandler (re-panicked by the
		// recovery middleware) so the client never mistakes a truncated body
		// for a clean EOF.
		h.Metrics.OSSDownloadTotal.WithLabelValues("stream_aborted").Inc()
		h.Logger.LogAttrs(c.Request.Context(), slog.LevelError, "artifact_download_stream_aborted",
			slog.String("artifact_id", artifactID.String()),
			slog.Int64("bytes_sent", n),
			slog.String("err", err.Error()),
			slog.String("trace_id", observability.TraceIDFromContext(c.Request.Context())),
		)
		panic(http.ErrAbortHandler)
	}
	h.Metrics.OSSDownloadTotal.WithLabelValues("success").Inc()
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
