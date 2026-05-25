package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// ServerDeps groups the wiring inputs for the HTTP server. Keeping them in a
// struct keeps the constructor signature stable as we add dependencies.
type ServerDeps struct {
	Logger           *slog.Logger
	Metrics          *observability.Metrics
	Probes           *ProbeRegistry
	TaskHandlers     *TaskHandlers     // optional; nil disables the write routes
	TaskReadHandlers *TaskReadHandlers // optional; nil disables the read routes
}

// NewEngine assembles the gin engine and the documented middleware chain:
//
//	request_id → tracing → metrics → access-log → recovery → auth (no-op stub) → handler
//
// Routes registered here are scaffold-only: /healthz, /readyz, /metrics.
// Business routes attach later in feature proposals via the returned engine.
func NewEngine(deps ServerDeps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	e := gin.New()

	// Middleware order matters: request_id first so every downstream layer can
	// log it; tracing before metrics so spans wrap the histogram observation;
	// recovery sits inside auth so a panicking handler still gets a 500.
	e.Use(
		requestIDMiddleware(),
		tracingMiddleware(),
		metricsMiddleware(deps.Metrics),
		accessLogMiddleware(deps.Logger),
		recoveryMiddleware(deps.Logger, deps.Metrics),
		authMiddleware(),
	)

	// Health & metrics endpoints — exempt from the business envelope per spec.
	e.GET("/healthz", healthzHandler())
	e.GET("/readyz", readyzHandler(deps.Probes))
	e.GET("/metrics", gin.WrapH(promhttp.HandlerFor(deps.Metrics.Registry, promhttp.HandlerOpts{})))

	// Business routes under /api/v1. Each handler set stays optional so tests
	// can spin up an engine with only the write or only the read side; the v1
	// group is created once and shared so both register on the same prefix.
	if deps.TaskHandlers != nil || deps.TaskReadHandlers != nil {
		v1 := e.Group("/api/v1")
		if deps.TaskHandlers != nil {
			deps.TaskHandlers.Register(v1)
		}
		if deps.TaskReadHandlers != nil {
			deps.TaskReadHandlers.Register(v1)
		}
	}

	return e
}

// Server wraps net/http.Server so the lifecycle code can call Start / Shutdown.
type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
}

// NewServer binds the engine to the given address.
func NewServer(addr string, engine *gin.Engine, logger *slog.Logger) *Server {
	return &Server{
		httpServer: &http.Server{
			Addr:              addr,
			Handler:           engine,
			ReadHeaderTimeout: 10 * time.Second,
		},
		logger: logger,
	}
}

// Start begins serving on the configured address. Returns when the listener
// closes; http.ErrServerClosed is mapped to nil so the caller can treat it as
// a normal shutdown signal.
func (s *Server) Start() error {
	s.logger.Info("http_listen_started", slog.String("addr", s.httpServer.Addr))
	if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown drains in-flight requests up to `timeout` then force-closes. The
// number of connections that were still active at deadline is returned so the
// caller can emit a structured warning per the api-bootstrap spec.
func (s *Server) Shutdown(ctx context.Context, timeout time.Duration) (forced bool, err error) {
	shutdownCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	err = s.httpServer.Shutdown(shutdownCtx)
	if errors.Is(err, context.DeadlineExceeded) {
		// Drain timeout hit; force-close remaining connections per spec.
		closeErr := s.httpServer.Close()
		if closeErr != nil {
			s.logger.Warn("http_force_close_error", slog.String("err", closeErr.Error()))
		}
		return true, nil
	}
	return false, err
}
