package messaging

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// DeliveryHandler processes a single delivery. It owns the ack/nack decision
// (the consumer never acks on the handler's behalf), so handlers stay
// unit-testable without a broker. A returned error is logged; the handler is
// still expected to have settled the delivery.
type DeliveryHandler func(ctx context.Context, d amqp.Delivery) error

// Consumer subscribes to one queue and dispatches deliveries to a handler,
// re-subscribing on channel/connection loss. Unlike the Relayer it needs no
// leader election: q.task.events is a work queue, RabbitMQ load-balances
// across replicas, and the handler is idempotent.
//
// The Run/Stop shape mirrors the Relayer (atomic.Bool + ctx).
type Consumer struct {
	conn      *Connection
	queue     string
	prefetch  int
	handler   DeliveryHandler
	logger    *slog.Logger
	connected *atomic.Bool // mirrors the EventConsumerConnected gauge
	metrics   *observability.Metrics

	stopped atomic.Bool
}

// NewConsumer builds a Consumer. prefetch <= 0 falls back to 1.
func NewConsumer(
	conn *Connection,
	queue string,
	prefetch int,
	handler DeliveryHandler,
	logger *slog.Logger,
	m *observability.Metrics,
) *Consumer {
	if prefetch <= 0 {
		prefetch = 1
	}
	return &Consumer{
		conn:      conn,
		queue:     queue,
		prefetch:  prefetch,
		handler:   handler,
		logger:    logger,
		connected: &atomic.Bool{},
		metrics:   m,
	}
}

// Run blocks until ctx is cancelled or Stop is called, (re)subscribing to the
// queue and dispatching deliveries. On any channel/connection error it backs
// off and re-subscribes — the underlying Connection reconnects on its own.
func (c *Consumer) Run(ctx context.Context) {
	const baseBackoff = 500 * time.Millisecond
	const maxBackoff = 30 * time.Second
	backoff := baseBackoff

	for {
		if c.stopped.Load() {
			return
		}
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := c.subscribeAndServe(ctx); err != nil {
			c.setConnected(false)
			c.logger.Warn("event_consumer_subscribe_failed",
				slog.String("queue", c.queue),
				slog.String("err", err.Error()),
			)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < maxBackoff {
				backoff *= 2
			}
			continue
		}
		// Clean return from subscribeAndServe means the delivery channel
		// closed (channel/conn loss); reset backoff and retry promptly.
		backoff = baseBackoff
	}
}

// Stop signals Run to exit at the next iteration.
func (c *Consumer) Stop() {
	c.stopped.Store(true)
}

// subscribeAndServe opens a channel, sets prefetch, starts consuming, and
// dispatches deliveries until the deliveries channel closes (channel/conn
// loss) or ctx/Stop fires. Returns an error only when subscription setup
// fails; a closed deliveries channel is a clean (nil) return so Run retries.
func (c *Consumer) subscribeAndServe(ctx context.Context) error {
	ch, err := c.conn.Channel()
	if err != nil {
		return err
	}
	defer func() { _ = ch.Close() }()

	if err := ch.Qos(c.prefetch, 0, false); err != nil {
		return err
	}
	deliveries, err := ch.Consume(c.queue, "", false /* autoAck */, false, false, false, nil)
	if err != nil {
		return err
	}

	c.setConnected(true)
	defer c.setConnected(false)
	c.logger.Info("event_consumer_subscribed", slog.String("queue", c.queue))

	closeCh := ch.NotifyClose(make(chan *amqp.Error, 1))
	for {
		select {
		case <-ctx.Done():
			return nil
		case amqpErr := <-closeCh:
			if amqpErr != nil {
				c.logger.Warn("event_consumer_channel_closed", slog.String("err", amqpErr.Error()))
			}
			return nil
		case d, ok := <-deliveries:
			if !ok {
				return nil
			}
			if c.stopped.Load() {
				// Don't process new work during shutdown; requeue for another
				// replica / next start.
				_ = d.Nack(false, true)
				return nil
			}
			if err := c.handler(ctx, d); err != nil {
				c.logger.Warn("event_consumer_handler_error", slog.String("err", err.Error()))
			}
		}
	}
}

func (c *Consumer) setConnected(v bool) {
	c.connected.Store(v)
	if c.metrics == nil {
		return
	}
	if v {
		c.metrics.EventConsumerConnected.Set(1)
	} else {
		c.metrics.EventConsumerConnected.Set(0)
	}
}
