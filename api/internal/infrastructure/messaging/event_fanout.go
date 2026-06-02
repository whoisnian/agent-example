package messaging

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	amqp "github.com/rabbitmq/amqp091-go"
)

// FanoutEvent is the decoded worker task-event as the realtime gateway needs
// it. Unlike taskEventEnvelope (the ingest decoder, which drops `ts`) this
// shape INCLUDES the worker-stamped `ts` so the gateway can forward the
// authoritative event time (design D6). Ids stay as strings: the gateway only
// builds topic keys (`task:<id>` / `version:<id>`) from them, never parses.
type FanoutEvent struct {
	TaskID    string          `json:"task_id"`
	VersionID string          `json:"version_id"`
	RunID     string          `json:"run_id"`
	Seq       int64           `json:"seq"`
	Kind      string          `json:"kind"`
	Ts        string          `json:"ts"`
	Payload   json.RawMessage `json:"payload"`
}

// FanoutHandler receives one decoded event for local fan-out. It MUST be
// non-blocking (the gateway hub does a non-blocking per-conn send); a slow
// handler would stall the whole fan-out stream. The event is passed by pointer
// (it is read-only to the handler) to avoid copying the envelope per delivery.
type FanoutHandler func(ev *FanoutEvent)

// FanoutConsumer gives one gateway process its OWN view of every task event by
// declaring a server-named, exclusive, auto-delete, non-durable queue bound to
// ExchangeEvents with `event.#` and consuming it (design D1). It is distinct
// from the shared, durable q.task.events work queue used for DB ingest: that
// queue load-balances deliveries across replicas (each event reaches only one
// consumer), whereas fan-out needs EVERY replica to see EVERY event.
//
// Because an exclusive/auto-delete queue is bound to the AMQP *connection*, it
// is destroyed when that connection drops. So unlike Consumer (which only
// re-Consumes a durable queue), this consumer MUST re-declare AND re-bind its
// queue on every (re)connect — handled by re-running the full setup inside the
// Run loop. The malformed-drop counter is owned by the caller (metrics), so a
// nil malformedTotal disables that signal (tests).
type FanoutConsumer struct {
	conn           *Connection
	prefetch       int
	handler        FanoutHandler
	logger         *slog.Logger
	connectedGauge prometheus.Gauge
	malformedTotal prometheus.Counter

	stopped atomic.Bool
}

// NewFanoutConsumer builds the consumer. prefetch <= 0 falls back to 1. A nil
// connectedGauge / malformedTotal simply disables that metric (useful in tests).
func NewFanoutConsumer(
	conn *Connection,
	prefetch int,
	handler FanoutHandler,
	logger *slog.Logger,
	connectedGauge prometheus.Gauge,
	malformedTotal prometheus.Counter,
) *FanoutConsumer {
	if prefetch <= 0 {
		prefetch = 1
	}
	return &FanoutConsumer{
		conn:           conn,
		prefetch:       prefetch,
		handler:        handler,
		logger:         logger,
		connectedGauge: connectedGauge,
		malformedTotal: malformedTotal,
	}
}

// Run blocks until ctx is cancelled or Stop is called, (re)declaring the
// exclusive fan-out queue and dispatching deliveries. On any channel/connection
// error it backs off and re-runs the full declare+bind+consume setup — the
// queue vanished with the old connection, so re-Consume alone would silently
// stop delivering (design D1 risk).
func (c *FanoutConsumer) Run(ctx context.Context) {
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

		if err := c.declareAndServe(ctx); err != nil {
			c.setConnected(false)
			c.logger.Warn("ws_fanout_subscribe_failed", slog.String("err", err.Error()))
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
		// Clean return = delivery channel closed (channel/conn loss); reset
		// backoff and re-declare promptly.
		backoff = baseBackoff
	}
}

// Stop signals Run to exit at the next iteration.
func (c *FanoutConsumer) Stop() {
	c.stopped.Store(true)
}

// declareAndServe opens a channel, declares + binds the exclusive fan-out queue,
// sets prefetch, consumes, and dispatches until the deliveries channel closes
// or ctx/Stop fires. Returns an error only on setup failure; a closed deliveries
// channel is a clean (nil) return so Run re-declares.
func (c *FanoutConsumer) declareAndServe(ctx context.Context) error {
	ch, err := c.conn.Channel()
	if err != nil {
		return err
	}
	defer func() { _ = ch.Close() }()

	// Server-named (empty), exclusive, auto-delete, non-durable: RabbitMQ picks
	// a unique name (no cross-instance collision) and reclaims the queue when
	// this connection drops.
	q, err := ch.QueueDeclare("", false /*durable*/, true /*autoDelete*/, true /*exclusive*/, false, nil)
	if err != nil {
		return err
	}
	// `event.#` — the worker key is the 3-segment event.<task_type>.<kind>, so
	// `event.*` would NOT match; only `#` spans all segments (design D1).
	if err := ch.QueueBind(q.Name, "event.#", ExchangeEvents, false, nil); err != nil {
		return err
	}
	if err := ch.Qos(c.prefetch, 0, false); err != nil {
		return err
	}
	// autoAck=true: fan-out is best-effort live delivery, not the durable ingest
	// path. A redelivered event would just be a duplicate the client dedups by
	// seq; we never want to requeue onto an exclusive queue.
	deliveries, err := ch.Consume(q.Name, "", true /*autoAck*/, false, false, false, nil)
	if err != nil {
		return err
	}

	c.setConnected(true)
	defer c.setConnected(false)
	c.logger.Info("ws_fanout_subscribed", slog.String("queue", q.Name))

	closeCh := ch.NotifyClose(make(chan *amqp.Error, 1))
	for {
		select {
		case <-ctx.Done():
			return nil
		case amqpErr := <-closeCh:
			if amqpErr != nil {
				c.logger.Warn("ws_fanout_channel_closed", slog.String("err", amqpErr.Error()))
			}
			return nil
		case d, ok := <-deliveries:
			if !ok {
				return nil
			}
			if c.stopped.Load() {
				return nil
			}
			c.dispatch(d.Body)
		}
	}
}

// dispatch decodes one delivery and invokes the handler. An undecodable body
// (or one missing kind) is dropped + counted; it MUST NOT break the consumer
// or any connection (spec "Undecodable delivery does not break the connection").
func (c *FanoutConsumer) dispatch(body []byte) {
	var ev FanoutEvent
	if err := json.Unmarshal(body, &ev); err != nil || ev.Kind == "" {
		if c.malformedTotal != nil {
			c.malformedTotal.Inc()
		}
		c.logger.Warn("ws_fanout_malformed")
		return
	}
	c.handler(&ev)
}

func (c *FanoutConsumer) setConnected(v bool) {
	if c.connectedGauge == nil {
		return
	}
	if v {
		c.connectedGauge.Set(1)
	} else {
		c.connectedGauge.Set(0)
	}
}
