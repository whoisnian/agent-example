// Package messaging implements RabbitMQ wiring: connection management with
// auto-reconnect, topology declaration, publisher abstraction with publisher
// confirms, and the Outbox Relayer loop.
package messaging

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Connection wraps an amqp091 connection and offers Channel(), background
// reconnect, and a last_connected_at signal used by the /readyz probe.
type Connection struct {
	url    string
	logger *slog.Logger

	mu               sync.Mutex
	conn             *amqp.Connection
	lastConnectedAt  atomic.Int64 // unix nanoseconds; 0 = never connected
	lastDisconnected atomic.Int64 // unix nanos of last observed close

	closed atomic.Bool
}

// Dial opens an initial RabbitMQ connection and starts a background watcher
// that reconnects on close. Failure to establish the FIRST connection is
// returned so the caller can fail-fast at startup per api-messaging spec.
func Dial(ctx context.Context, url string, logger *slog.Logger) (*Connection, error) {
	c := &Connection{url: url, logger: logger}
	if err := c.connectOnce(); err != nil {
		return nil, err
	}
	go c.watchLoop(ctx)
	return c, nil
}

func (c *Connection) connectOnce() error {
	conn, err := amqp.Dial(c.url)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	c.lastConnectedAt.Store(time.Now().UnixNano())
	c.lastDisconnected.Store(0)
	return nil
}

// watchLoop blocks on the connection's NotifyClose channel and tries to
// reconnect with bounded exponential backoff.
func (c *Connection) watchLoop(ctx context.Context) {
	for {
		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()
		if conn == nil {
			return
		}
		closeCh := conn.NotifyClose(make(chan *amqp.Error, 1))

		select {
		case <-ctx.Done():
			return
		case reason, ok := <-closeCh:
			if c.closed.Load() {
				return
			}
			c.lastDisconnected.Store(time.Now().UnixNano())
			if c.logger != nil {
				c.logger.Warn("rabbitmq_disconnected",
					slog.Bool("clean", !ok),
					slog.String("reason", amqpReason(reason)),
				)
			}
			c.reconnect(ctx)
		}
	}
}

func amqpReason(e *amqp.Error) string {
	if e == nil {
		return ""
	}
	return e.Reason
}

// reconnect retries with exponential backoff (cap 30s) until success or ctx done.
func (c *Connection) reconnect(ctx context.Context) {
	backoff := time.Second
	for {
		if c.closed.Load() {
			return
		}
		if err := c.connectOnce(); err == nil {
			if c.logger != nil {
				c.logger.Info("rabbitmq_reconnected")
			}
			return
		} else if c.logger != nil {
			c.logger.Warn("rabbitmq_reconnect_failed", slog.String("err", err.Error()))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// Channel returns a fresh AMQP channel. The caller owns its lifecycle.
func (c *Connection) Channel() (*amqp.Channel, error) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil || conn.IsClosed() {
		return nil, errors.New("rabbitmq connection not established")
	}
	return conn.Channel()
}

// Close terminates the connection and prevents further reconnects.
func (c *Connection) Close() error {
	c.closed.Store(true)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// Probe is the /readyz check for RabbitMQ. Per api-messaging spec, transient
// reconnect attempts MUST NOT flip readiness for the first 10s of disconnect.
func (c *Connection) Probe(_ context.Context) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn != nil && !conn.IsClosed() {
		return nil
	}
	// Disconnected — only report failure if we've been disconnected for >=10s.
	disc := c.lastDisconnected.Load()
	if disc == 0 {
		// Never connected: treat as unavailable so /readyz fails before any
		// successful handshake, but only after startup probe — startup probe
		// is enforced via Dial() returning an error.
		return errors.New("rabbitmq: connection not established")
	}
	if time.Since(time.Unix(0, disc)) < 10*time.Second {
		return nil // still inside grace window
	}
	return errors.New("rabbitmq: sustained disconnect (>=10s)")
}
