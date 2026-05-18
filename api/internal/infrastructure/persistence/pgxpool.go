// Package persistence wires the PostgreSQL connection pool, the migration
// runner, and (eventually) the sqlc-generated query layer.
package persistence

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/config"
)

// Pool wraps pgxpool.Pool so callers stay decoupled from the underlying driver.
type Pool struct {
	*pgxpool.Pool
}

// NewPool builds a pgxpool.Pool from the loaded Config and runs a startup
// `SELECT 1` probe so the process fails fast when PostgreSQL is unreachable.
//
// The caller MUST call Close during shutdown.
func NewPool(ctx context.Context, cfg *config.Config) (*Pool, error) {
	pcfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	pcfg.MaxConns = cfg.DBMaxConns
	pcfg.MinConns = cfg.DBMinConns
	pcfg.MaxConnLifetime = cfg.DBMaxConnLifetime
	pcfg.MaxConnIdleTime = cfg.DBMaxConnIdleTime

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("open pgxpool: %w", err)
	}

	probeCtx, cancel := context.WithTimeout(ctx, cfg.DBConnectTimeout)
	defer cancel()
	if err := pool.Ping(probeCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Pool{Pool: pool}, nil
}

// Probe is the readiness check exposed at /readyz for postgres. It acquires a
// connection and runs `SELECT 1` with a tight deadline.
func (p *Pool) Probe(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return p.Ping(ctx)
}
