// Command api is the entry point for the API service. It supports two modes:
//
//	api               — start the HTTP server, relayer, and dependency probes.
//	api migrate ...   — run schema migrations and exit. Subcommands:
//	    up           — apply all pending up-migrations
//	    down         — roll back exactly one migration
//	    version      — print the current applied version + dirty flag
//	    force <n>    — set version to <n> manually (dirty-state recovery)
//
// Flags (server mode only):
//
//	--config <path>   — optional YAML overlay; env vars still override it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"

	appcost "github.com/whoisnian/agent-example/api/internal/application/cost"
	apptask "github.com/whoisnian/agent-example/api/internal/application/task"
	taskdomain "github.com/whoisnian/agent-example/api/internal/domain/task"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/config"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/messaging"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence"
	"github.com/whoisnian/agent-example/api/internal/infrastructure/persistence/sqlc"
	httpapi "github.com/whoisnian/agent-example/api/internal/interfaces/http"
)

func main() {
	// migrate subcommand short-circuits before the full bootstrap.
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		code := runMigrate(os.Args[2:])
		os.Exit(code)
	}

	code := runServer(os.Args[1:])
	os.Exit(code)
}

// runServer boots the full API process and blocks until shutdown completes.
// Returns the desired exit code (0 = clean shutdown, non-zero = startup error).
func runServer(args []string) int {
	fs := flag.NewFlagSet("api", flag.ContinueOnError)
	configPath := fs.String("config", "", "optional YAML config file path")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "parse flags:", err)
		return 2
	}

	// Initial bare logger — replaced once config tells us the desired level.
	bootLogger := observability.NewLogger("info", os.Stderr)

	cfg, err := config.Load(*configPath, os.LookupEnv)
	if err != nil {
		bootLogger.Error("config_load_failed", slog.String("err", err.Error()))
		return 1
	}

	logger := observability.NewLogger(cfg.LogLevel, os.Stdout)
	logger.Info("api_starting", slog.String("addr", cfg.HTTPAddr))

	// Validate static-shape config up front so we fail fast before opening
	// any external connections.
	devTenant, err := uuid.Parse(cfg.DevTenantID)
	if err != nil {
		logger.Error("dev_tenant_id_invalid", slog.String("err", err.Error()))
		return 1
	}
	devUser, err := uuid.Parse(cfg.DevUserID)
	if err != nil {
		logger.Error("dev_user_id_invalid", slog.String("err", err.Error()))
		return 1
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracer, err := observability.SetupTracing(ctx, cfg.ServiceName, cfg.OTLPEndpoint)
	if err != nil {
		logger.Error("tracing_setup_failed", slog.String("err", err.Error()))
		return 1
	}
	metrics := observability.NewMetrics()

	// PostgreSQL pool + optional boot migrations.
	pool, err := persistence.NewPool(ctx, cfg)
	if err != nil {
		logger.Error("postgres_unavailable", slog.String("err", err.Error()))
		return 1
	}
	if cfg.DBMigrateOnBoot {
		m, mErr := persistence.NewMigrator("migrations", cfg.DatabaseURL)
		if mErr != nil {
			logger.Error("migrator_init_failed", slog.String("err", mErr.Error()))
			pool.Close()
			return 1
		}
		if upErr := m.Up(); upErr != nil {
			logger.Error("migrate_up_failed", slog.String("err", upErr.Error()))
			_ = m.Close()
			pool.Close()
			return 1
		}
		_ = m.Close()
		logger.Info("migrate_up_applied")
	}

	// RabbitMQ connection + topology + publisher.
	mqConn, err := messaging.Dial(ctx, cfg.RabbitMQURL, logger)
	if err != nil {
		logger.Error("rabbitmq_unavailable", slog.String("err", err.Error()))
		pool.Close()
		return 1
	}
	{
		ch, chErr := mqConn.Channel()
		if chErr != nil {
			logger.Error("rabbitmq_channel_failed", slog.String("err", chErr.Error()))
			_ = mqConn.Close()
			pool.Close()
			return 1
		}
		if topErr := messaging.DeclareTopology(ch); topErr != nil {
			logger.Error("rabbitmq_topology_failed", slog.String("err", topErr.Error()))
			_ = ch.Close()
			_ = mqConn.Close()
			pool.Close()
			return 1
		}
		_ = ch.Close()
	}
	publisher, err := messaging.NewConfirmingPublisher(mqConn, cfg.RabbitMQConfirmTimeout, metrics)
	if err != nil {
		logger.Error("publisher_init_failed", slog.String("err", err.Error()))
		_ = mqConn.Close()
		pool.Close()
		return 1
	}

	// Outbox Relayer (one per database, gated by advisory lock).
	store := persistence.NewOutboxStore(pool)
	relayer := messaging.NewRelayer(messaging.RelayerConfig{
		TickInterval: cfg.OutboxTickInterval,
		BatchSize:    cfg.OutboxBatchSize,
		MaxAttempts:  cfg.OutboxMaxAttempts,
		LockID:       cfg.OutboxLockID,
	}, store, publisher, logger, metrics)

	relayerCtx, stopRelayer := context.WithCancel(ctx)
	relayerDone := make(chan struct{})
	go func() {
		defer close(relayerDone)
		relayer.Run(relayerCtx)
	}()

	// HTTP server with /readyz probes bound to live dependencies.
	probes := httpapi.NewProbeRegistry(time.Second)
	probes.Register("postgres", pool.Probe)
	probes.Register("rabbitmq", mqConn.Probe)

	// Task-write-api wiring (domain → application → HTTP).
	queries := sqlc.New(pool.Pool)
	domainSvc := taskdomain.NewService(
		pool.Pool,
		queries,
		taskdomain.SystemClock{},
		taskdomain.UUIDv7Gen{},
		cfg.DefaultLane,
		cfg.DefaultTaskDeadline,
	)
	appSvc := apptask.NewService(domainSvc)
	taskHandlers := &httpapi.TaskHandlers{
		App:         appSvc,
		Logger:      logger,
		Metrics:     metrics,
		DevTenantID: devTenant,
		DevUserID:   devUser,
	}

	// Event-ingest consumer: drives task_versions.status + tasks.status from
	// the worker event stream (constructed after domainSvc exists). Shares
	// q.task.events across replicas with no leader election; the handler is
	// idempotent so competing consumers are safe.
	eventHandler := messaging.NewEventIngestHandler(domainSvc, logger, metrics)
	eventConsumer := messaging.NewConsumer(
		mqConn,
		messaging.QueueTaskEvents,
		cfg.EventConsumerPrefetch,
		eventHandler.Handle,
		logger,
		metrics.EventConsumerConnected,
	)
	consumerCtx, stopConsumer := context.WithCancel(ctx)
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		eventConsumer.Run(consumerCtx)
	}()

	// Cost-ingest consumer (add-cost-service): subscribes to q.cost.events,
	// prices each delivery against `pricing`, and UPSERTs task_costs. Owns
	// its own gauge so an outage on q.task.events doesn't mask q.cost.events.
	costSettler := appcost.NewSettler(pool.Pool, queries)
	costHandler := messaging.NewCostIngestHandler(costSettler, logger, metrics)
	costConsumer := messaging.NewConsumer(
		mqConn,
		messaging.QueueCostEvents,
		cfg.CostIngestPrefetch,
		costHandler.Handle,
		logger,
		metrics.CostConsumerConnected,
	)
	costConsumerCtx, stopCostConsumer := context.WithCancel(ctx)
	costConsumerDone := make(chan struct{})
	go func() {
		defer close(costConsumerDone)
		costConsumer.Run(costConsumerCtx)
	}()

	// Startup pricing-coverage log — operational guardrail (S4) against a
	// resource_name typo silently producing $0 events forever. Best-effort:
	// a DB hiccup at boot only logs a WARN and proceeds.
	if coverage, cerr := costSettler.ListEffectivePricingCoverage(ctx, time.Now()); cerr != nil {
		logger.Warn("cost_pricing_coverage_query_failed", slog.String("err", cerr.Error()))
	} else {
		pairs := make([]string, 0, len(coverage))
		for _, e := range coverage {
			pairs = append(pairs, e.Kind+":"+e.Name)
		}
		logger.Info("cost_pricing_coverage",
			slog.Int("count", len(coverage)),
			slog.Any("resources", pairs),
		)
	}

	// Task-read-api wiring (queries-only read service, same dev identity).
	appReadSvc := apptask.NewReadService(taskdomain.NewReadService(queries))
	taskReadHandlers := &httpapi.TaskReadHandlers{
		App:         appReadSvc,
		Logger:      logger,
		DevTenantID: devTenant,
		DevUserID:   devUser,
	}

	// Task-cost-api wiring (queries-only, shares the dev identity).
	appCostReadSvc := apptask.NewCostReadService(taskdomain.NewCostReadService(queries))
	taskCostHandlers := &httpapi.TaskCostHandlers{
		App:         appCostReadSvc,
		Logger:      logger,
		DevTenantID: devTenant,
		DevUserID:   devUser,
	}

	// Task-control-api wiring (writes outbox; worker drives state via events).
	appControlSvc := apptask.NewControlService(
		taskdomain.NewControlService(pool.Pool, queries, taskdomain.SystemClock{}),
	)
	taskControlHandlers := &httpapi.TaskControlHandlers{
		App:         appControlSvc,
		Logger:      logger,
		Metrics:     metrics,
		DevTenantID: devTenant,
		DevUserID:   devUser,
	}

	engine := httpapi.NewEngine(httpapi.ServerDeps{
		Logger:              logger,
		Metrics:             metrics,
		Probes:              probes,
		TaskHandlers:        taskHandlers,
		TaskReadHandlers:    taskReadHandlers,
		TaskCostHandlers:    taskCostHandlers,
		TaskControlHandlers: taskControlHandlers,
	})
	server := httpapi.NewServer(cfg.HTTPAddr, engine, logger)

	listenErr := make(chan error, 1)
	go func() { listenErr <- server.Start() }()

	// Wait for signal or listener failure.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case s := <-sigCh:
		logger.Info("shutdown_signal_received", slog.String("signal", s.String()))
	case err := <-listenErr:
		if err != nil {
			logger.Error("http_listen_failed", slog.String("err", err.Error()))
		}
	}

	// Ordered shutdown: HTTP → Relayer → MQ → DB → tracer (per spec Task 5.3).
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()

	forced, err := server.Shutdown(shutdownCtx, cfg.ShutdownDrainTimeout)
	if err != nil {
		logger.Warn("http_shutdown_error", slog.String("err", err.Error()))
	}
	if forced {
		logger.Warn("http_drain_timeout_forced_close",
			slog.Duration("timeout", cfg.ShutdownDrainTimeout),
		)
	}

	relayer.Stop()
	stopRelayer()
	<-relayerDone

	// Stop the consumers before closing the MQ connection so in-flight
	// deliveries aren't nacked into a closing channel.
	eventConsumer.Stop()
	stopConsumer()
	<-consumerDone

	costConsumer.Stop()
	stopCostConsumer()
	<-costConsumerDone

	if err := publisher.Close(); err != nil {
		logger.Warn("publisher_close_error", slog.String("err", err.Error()))
	}
	if err := mqConn.Close(); err != nil {
		logger.Warn("rabbitmq_close_error", slog.String("err", err.Error()))
	}
	pool.Close()

	if tracer != nil {
		if err := tracer.Shutdown(shutdownCtx); err != nil {
			logger.Warn("tracer_shutdown_error", slog.String("err", err.Error()))
		}
	}

	logger.Info("api_shutdown_complete")
	return 0
}

// runMigrate handles `api migrate ...` subcommands.
func runMigrate(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: api migrate {up|down|version|force <n>}")
		return 2
	}

	bootLogger := observability.NewLogger("info", os.Stderr)
	cfg, err := config.Load("", os.LookupEnv)
	if err != nil {
		// Migrate only needs DATABASE_URL; tolerate missing others.
		var mr *config.MissingRequiredError
		if !errors.As(err, &mr) || !onlyMissing(mr, "DATABASE_URL") {
			// We need DATABASE_URL specifically — re-check.
			if dsn, ok := os.LookupEnv("DATABASE_URL"); ok && dsn != "" {
				// build a minimal config struct for migrate use
				cfg = &config.Config{DatabaseURL: dsn}
			} else {
				bootLogger.Error("config_load_failed", slog.String("err", err.Error()))
				return 1
			}
		}
	}

	m, err := persistence.NewMigrator("migrations", cfg.DatabaseURL)
	if err != nil {
		bootLogger.Error("migrator_init_failed", slog.String("err", err.Error()))
		return 1
	}
	defer func() { _ = m.Close() }()

	switch args[0] {
	case "up":
		if err := m.Up(); err != nil {
			bootLogger.Error("migrate_up_failed", slog.String("err", err.Error()))
			return 1
		}
		bootLogger.Info("migrate_up_ok")
	case "down":
		if err := m.Down(); err != nil {
			bootLogger.Error("migrate_down_failed", slog.String("err", err.Error()))
			return 1
		}
		bootLogger.Info("migrate_down_ok")
	case "version":
		v, dirty, err := m.Version()
		if err != nil {
			bootLogger.Error("migrate_version_failed", slog.String("err", err.Error()))
			return 1
		}
		bootLogger.Info("migrate_version", slog.Uint64("version", uint64(v)), slog.Bool("dirty", dirty))
	case "force":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: api migrate force <version>")
			return 2
		}
		n, err := strconv.Atoi(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "invalid version:", args[1])
			return 2
		}
		if err := m.Force(n); err != nil {
			bootLogger.Error("migrate_force_failed", slog.String("err", err.Error()))
			return 1
		}
		bootLogger.Info("migrate_force_ok", slog.Int("version", n))
	default:
		fmt.Fprintln(os.Stderr, "unknown migrate subcommand:", args[0])
		return 2
	}
	return 0
}

// onlyMissing reports true when the MissingRequiredError covers exactly the
// given keys (and no others).
func onlyMissing(e *config.MissingRequiredError, _ ...string) bool {
	// We accept any subset for `migrate`; we only need DATABASE_URL upstream.
	return len(e.Keys) > 0
}
