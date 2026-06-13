package httpapi

import (
	"archive/zip"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"

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
	// Version-scoped zip archive (improve-artifact-conversation-ux): a Bearer
	// presign mint + a public token-authenticated streaming download.
	r.GET("/versions/:version_id/artifacts/archive/presign", h.presignArchive)
	r.GET("/versions/:version_id/artifacts/archive", h.downloadArchive)
	// Directory-aware HTML preview: a Bearer mint returning a tokenized base
	// URL + a public token-in-path serve route whose `*filepath` resolves to a
	// sibling artifact of the version (so relative css/js load correctly).
	r.GET("/versions/:version_id/preview", h.presignPreview)
	r.GET("/versions/:version_id/preview/:token/*filepath", h.previewFile)
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

// presignArchive handles GET /versions/:version_id/artifacts/archive/presign:
// owner-scoped mint of a version-archive download URL. Bearer-authenticated.
func (h *ArtifactHandlers) presignArchive(c *gin.Context) {
	versionID, ok := parseUUIDParam(c, "version_id")
	if !ok {
		return
	}
	p, ok := principalOrAbort(c)
	if !ok {
		return
	}
	res, err := h.App.PresignArchive(c.Request.Context(), p.TenantID, p.UserID, versionID)
	if err != nil {
		h.handleError(c, err)
		return
	}
	OK(c, res)
}

// presignPreview handles GET /versions/:version_id/preview: owner-scoped mint
// of a tokenized preview base URL. Bearer-authenticated.
func (h *ArtifactHandlers) presignPreview(c *gin.Context) {
	versionID, ok := parseUUIDParam(c, "version_id")
	if !ok {
		return
	}
	p, ok := principalOrAbort(c)
	if !ok {
		return
	}
	res, err := h.App.PresignPreview(c.Request.Context(), p.TenantID, p.UserID, versionID)
	if err != nil {
		h.handleError(c, err)
		return
	}
	OK(c, res)
}

// downloadArchive handles GET /versions/:version_id/artifacts/archive?token=:
// the public route that streams a zip of the version's artifacts. Its sole
// authentication is the `?token=` version-archive grant. An OSS failure BEFORE
// the first zip byte returns 502; a failure mid-stream aborts the connection
// (a zip cannot carry an error envelope once bytes are out). The token rides in
// the query string, so the standard middleware (which logs only the path)
// keeps it out of the access log.
func (h *ArtifactHandlers) downloadArchive(c *gin.Context) {
	versionID, ok := parseUUIDParam(c, "version_id")
	if !ok {
		return
	}
	if err := h.Tokens.ParseArchive(c.Query("token"), versionID); err != nil {
		h.Metrics.OSSArchiveTotal.WithLabelValues("token_invalid").Inc()
		Error(c, http.StatusForbidden, "invalid_download_token", "invalid or expired download token")
		return
	}

	ctx := c.Request.Context()
	entries, err := h.App.ListVersionArchiveEntries(ctx, versionID)
	if err != nil {
		h.Metrics.OSSArchiveTotal.WithLabelValues("oss_error").Inc()
		h.handleError(c, err)
		return
	}

	headerWritten := false
	writeHeaders := func() {
		hdr := c.Writer.Header()
		hdr.Set("Content-Type", "application/zip")
		hdr.Set("Content-Disposition", `attachment; filename="artifacts-`+versionID.String()+`.zip"`)
		hdr.Set("X-Content-Type-Options", "nosniff")
		hdr.Set("Cache-Control", "private, no-store")
		c.Status(http.StatusOK)
		headerWritten = true
	}

	counter := &countingWriter{w: c.Writer}
	zw := zip.NewWriter(counter)
	for i := range entries {
		e := entries[i]
		body, oerr := e.Open(ctx)
		if oerr != nil {
			if !headerWritten {
				// Nothing on the wire yet → a clean 502 envelope is still possible.
				h.Metrics.OSSArchiveTotal.WithLabelValues("oss_error").Inc()
				h.handleError(c, oerr)
				return
			}
			h.archiveAbort(c, versionID, oerr)
			return
		}
		if !headerWritten {
			writeHeaders()
		}
		w, werr := zw.Create(e.Name)
		if werr == nil {
			_, werr = io.Copy(w, body)
		}
		_ = body.Close()
		if werr != nil {
			h.archiveAbort(c, versionID, werr)
			return
		}
	}
	if !headerWritten { // zero-artifact version → a valid empty zip
		writeHeaders()
	}
	if err := zw.Close(); err != nil {
		h.archiveAbort(c, versionID, err)
		return
	}
	h.Metrics.OSSArchiveBytes.Add(float64(counter.n))
	h.Metrics.OSSArchiveTotal.WithLabelValues("success").Inc()
}

// archiveAbort records a mid-stream archive failure and cuts the connection —
// no error envelope is possible once zip bytes are out.
func (h *ArtifactHandlers) archiveAbort(c *gin.Context, versionID interface{ String() string }, err error) {
	h.Metrics.OSSArchiveTotal.WithLabelValues("stream_aborted").Inc()
	h.Logger.LogAttrs(c.Request.Context(), slog.LevelError, "artifact_archive_stream_aborted",
		slog.String("version_id", versionID.String()),
		slog.String("err", err.Error()),
		slog.String("trace_id", observability.TraceIDFromContext(c.Request.Context())),
	)
	panic(http.ErrAbortHandler)
}

// previewFile handles GET /versions/:version_id/preview/:token/*filepath: the
// public directory-aware preview route. The token rides in the `:token` PATH
// segment; `*filepath` resolves to a sibling artifact by exact (version_id,
// path) match so a rendered document's relative css/js load under the same
// token prefix.
func (h *ArtifactHandlers) previewFile(c *gin.Context) {
	versionID, ok := parseUUIDParam(c, "version_id")
	if !ok {
		return
	}
	if err := h.Tokens.ParsePreview(c.Param("token"), versionID); err != nil {
		h.Metrics.OSSPreviewTotal.WithLabelValues("token_invalid").Inc()
		Error(c, http.StatusForbidden, "invalid_download_token", "invalid or expired preview token")
		return
	}
	rel, valid := sanitizePreviewPath(c.Param("filepath"))
	if !valid {
		h.Metrics.OSSPreviewTotal.WithLabelValues("not_found").Inc()
		Error(c, http.StatusNotFound, "artifact_not_found", "artifact not found")
		return
	}

	obj, err := h.App.OpenVersionFile(c.Request.Context(), versionID, rel)
	if err != nil {
		if errors.Is(err, taskdomain.ErrArtifactNotFound) {
			h.Metrics.OSSPreviewTotal.WithLabelValues("not_found").Inc()
		} else {
			h.Metrics.OSSPreviewTotal.WithLabelValues("oss_error").Inc()
		}
		h.handleError(c, err)
		return
	}
	defer func() { _ = obj.Body.Close() }()

	mime := "application/octet-stream"
	if obj.Mime != nil && *obj.Mime != "" {
		mime = *obj.Mime
	}
	hdr := c.Writer.Header()
	hdr.Set("Content-Type", mime)
	if obj.ContentLength != nil {
		hdr.Set("Content-Length", strconv.FormatInt(*obj.ContentLength, 10))
	}
	hdr.Set("Content-Security-Policy", "sandbox allow-scripts")
	hdr.Set("Referrer-Policy", "no-referrer")
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Cache-Control", "private, no-store")
	c.Status(http.StatusOK)

	n, err := io.Copy(c.Writer, obj.Body)
	h.Metrics.OSSPreviewBytes.Add(float64(n))
	if err != nil {
		h.Metrics.OSSPreviewTotal.WithLabelValues("stream_aborted").Inc()
		h.Logger.LogAttrs(c.Request.Context(), slog.LevelError, "artifact_preview_stream_aborted",
			slog.String("version_id", versionID.String()),
			slog.Int64("bytes_sent", n),
			slog.String("err", err.Error()),
			slog.String("trace_id", observability.TraceIDFromContext(c.Request.Context())),
		)
		panic(http.ErrAbortHandler)
	}
	h.Metrics.OSSPreviewTotal.WithLabelValues("success").Inc()
}

// sanitizePreviewPath cleans the catch-all `*filepath` (gin returns it with a
// leading slash, already percent-decoded) into a version-relative artifact
// path. It rejects (returns ok=false) an empty / dot path, a backslash, or any
// path that escapes the version namespace (`..` segments, absolute) — those map
// to 404 so no traversal can reach a foreign object.
func sanitizePreviewPath(raw string) (string, bool) {
	rel := strings.TrimPrefix(raw, "/")
	if rel == "" || strings.Contains(rel, "\\") {
		return "", false
	}
	cleaned := path.Clean(rel)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", false
	}
	return cleaned, true
}

// countingWriter tallies bytes written through it (the compressed zip stream)
// without buffering, for the archive bytes-streamed metric.
type countingWriter struct {
	w io.Writer
	n int64
}

func (cw *countingWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	cw.n += int64(n)
	return n, err
}
