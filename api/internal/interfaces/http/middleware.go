package httpapi

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/whoisnian/agent-example/api/internal/auth"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

const (
	headerRequestID   = "X-Request-ID"
	headerTraceParent = "traceparent"
	tracerName        = "github.com/whoisnian/agent-example/api/http"
)

// requestIDMiddleware extracts or generates a request id, stores it in
// the request context, and echoes it in the response header.
func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader(headerRequestID)
		if rid == "" {
			rid = uuid.NewString()
		}
		ctx := observability.WithRequestID(c.Request.Context(), rid)
		c.Request = c.Request.WithContext(ctx)
		c.Writer.Header().Set(headerRequestID, rid)
		c.Next()
	}
}

// tracingMiddleware starts a span per request, propagates W3C headers, and
// stuffs the trace id into the request context so log records and the response
// envelope can reference it.
func tracingMiddleware() gin.HandlerFunc {
	tracer := otel.Tracer(tracerName)
	prop := otel.GetTextMapPropagator()
	return func(c *gin.Context) {
		ctx := prop.Extract(c.Request.Context(), propagation.HeaderCarrier(c.Request.Header))
		route := c.FullPath()
		if route == "" {
			route = c.Request.URL.Path
		}
		spanName := "HTTP " + c.Request.Method + " " + route

		ctx, span := tracer.Start(ctx, spanName,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				semconv.HTTPRequestMethodKey.String(c.Request.Method),
				semconv.HTTPRouteKey.String(route),
			),
		)
		defer span.End()

		if sc := span.SpanContext(); sc.HasTraceID() {
			ctx = observability.WithTraceID(ctx, sc.TraceID().String())
		}
		c.Request = c.Request.WithContext(ctx)

		c.Next()

		span.SetAttributes(semconv.HTTPResponseStatusCodeKey.Int(c.Writer.Status()))
	}
}

// metricsMiddleware records request count and duration into the observability
// registry. Routes without a matching template fall back to "unmatched" so
// label cardinality stays bounded.
func metricsMiddleware(m *observability.Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		status := strconv.Itoa(c.Writer.Status())
		m.HTTPRequestsTotal.WithLabelValues(route, c.Request.Method, status).Inc()
		m.HTTPRequestDuration.WithLabelValues(route, c.Request.Method).Observe(time.Since(start).Seconds())
	}
}

// accessLogMiddleware emits one structured log entry per completed request.
// Correlation IDs are pulled from context by the slog handler automatically.
func accessLogMiddleware(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.LogAttrs(c.Request.Context(), slog.LevelInfo, "http_access",
			slog.String("method", c.Request.Method),
			slog.String("path", accessLogPath(c)),
			slog.Int("status", c.Writer.Status()),
			slog.Duration("duration", time.Since(start)),
			slog.String("remote", c.ClientIP()),
		)
	}
}

// previewServeRoute is the matched template of the directory-aware preview
// serve route, whose `:token` segment carries the grant and MUST NOT be logged.
const previewServeRoute = "/api/v1/versions/:version_id/preview/:token/*filepath"

// accessLogPath returns a loggable request path with any in-path secret
// redacted. The preview serve route carries its token in a path segment, so
// the raw URL.Path would leak it; reconstruct a redacted path from the safe
// params (version_id + filepath) instead. All other routes log URL.Path as-is
// (query strings — where the download/archive tokens ride — are never logged).
func accessLogPath(c *gin.Context) string {
	if c.FullPath() == previewServeRoute {
		return "/api/v1/versions/" + c.Param("version_id") + "/preview/[redacted]" + c.Param("filepath")
	}
	return c.Request.URL.Path
}

// publicRoute is a (method, route-template) pair the auth middleware lets
// through without a token.
type publicRoute struct{ method, path string }

// publicRoutes is the fixed allowlist: the health/metrics probes, the login
// endpoint (a caller has no token yet), the WS upgrade, and the artifact
// download proxy. The WS and download routes are NOT unauthenticated —
// browsers can't set an Authorization header on a WebSocket handshake or an
// <img>/<iframe>/navigation load, so each route authenticates its own
// `?token=...` query param (the gateway closes 4001; the download handler
// answers 403 invalid_download_token). They must bypass this header-based
// middleware so those query-token paths can run. Keyed on the route TEMPLATE
// (c.FullPath()) + method so only the intended verb is public and trailing-
// slash / query tricks can't widen it.
var publicRoutes = map[publicRoute]bool{
	{http.MethodGet, "/healthz"}:                                true,
	{http.MethodGet, "/readyz"}:                                 true,
	{http.MethodGet, "/metrics"}:                                true,
	{http.MethodPost, "/api/v1/auth/login"}:                     true,
	{http.MethodGet, "/api/v1/ws"}:                              true,
	{http.MethodGet, "/api/v1/artifacts/:artifact_id/download"}: true,
	// Version-scoped artifact streams (improve-artifact-conversation-ux): the
	// zip archive carries its grant in `?token=`, the preview carries it in a
	// path segment — neither can send an Authorization header (navigation /
	// <iframe> / <link>). They authenticate via their own token verifiers.
	{http.MethodGet, "/api/v1/versions/:version_id/artifacts/archive"}:        true,
	{http.MethodGet, "/api/v1/versions/:version_id/preview/:token/*filepath"}: true,
}

// authMiddleware authenticates every non-public request via a Bearer JWT. On
// success it injects the resolved Principal into the request context; on a
// missing / malformed / bad-signature / expired token it writes a 401
// `unauthenticated` envelope (the shape the web client's clear-token-and-
// redirect path expects) and aborts. Public routes bypass the token check.
func authMiddleware(verifier *auth.Verifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		if publicRoutes[publicRoute{c.Request.Method, c.FullPath()}] {
			c.Next()
			return
		}
		tok, ok := bearerToken(c.GetHeader("Authorization"))
		if !ok {
			Error(c, http.StatusUnauthorized, "unauthenticated", "missing or malformed authorization header")
			return
		}
		p, err := verifier.Parse(tok)
		if err != nil {
			Error(c, http.StatusUnauthorized, "unauthenticated", "invalid or expired token")
			return
		}
		c.Request = c.Request.WithContext(auth.WithPrincipal(c.Request.Context(), p))
		c.Next()
	}
}

// bearerToken extracts the token from an `Authorization: Bearer <token>` header
// (scheme is case-insensitive), returning ok=false when absent/malformed.
func bearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	if tok := strings.TrimSpace(header[len(prefix):]); tok != "" {
		return tok, true
	}
	return "", false
}

// principalOrAbort returns the authenticated principal placed by authMiddleware.
// On a protected route the middleware guarantees one; its absence is a
// programming error (a route mistakenly off the chain), so we fail CLOSED with
// 500 — never fall back to a zero-UUID owner.
func principalOrAbort(c *gin.Context) (auth.Principal, bool) {
	p, ok := auth.PrincipalFromContext(c.Request.Context())
	if !ok {
		Error(c, http.StatusInternalServerError, "internal_error", "missing authenticated principal")
		return auth.Principal{}, false
	}
	return p, true
}
