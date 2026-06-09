package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/whoisnian/agent-example/api/internal/infrastructure/observability"
)

// Envelope carries one outbound delivery from the relayer to the publisher.
// `Payload` IS the wire body: per ARCHITECTURE §5.3 the message body is the
// flat domain message (e.g. the execute message `{msg_id, idempotency_key,
// task_id, version_id, ...}`), which the worker parses directly — it is NOT
// re-wrapped here. `MsgID` and `OccurredAt` travel as AMQP properties
// (message_id / timestamp), not in the body. The payload already carries its
// own msg_id / idempotency_key, so no envelope-level idempotency key is added.
type Envelope struct {
	MsgID      string    // → AMQP message_id property
	Payload    any       // → JSON body (the flat §5.3 message)
	OccurredAt time.Time // → AMQP timestamp property
}

// Publisher hides amqp091 details so application code never touches Channel.Publish.
type Publisher interface {
	Publish(ctx context.Context, exchange, routingKey string, env Envelope) error
	Close() error
}

// ConfirmingPublisher implements Publisher with publisher confirms enabled.
// Each call waits for the broker's ack/nack with a configurable timeout
// (default 5s, per design D6 / spec).
type ConfirmingPublisher struct {
	conn           *Connection
	confirmTimeout time.Duration
	metrics        *observability.Metrics

	mu      sync.Mutex
	channel *amqp.Channel
	acks    chan amqp.Confirmation
}

// NewConfirmingPublisher opens a channel, switches it to confirm mode, and
// retains it for subsequent Publish calls. The publisher is single-channel by
// design; that channel is rebuilt lazily on failure.
func NewConfirmingPublisher(conn *Connection, confirmTimeout time.Duration, m *observability.Metrics) (*ConfirmingPublisher, error) {
	p := &ConfirmingPublisher{
		conn:           conn,
		confirmTimeout: confirmTimeout,
		metrics:        m,
	}
	if err := p.openChannel(); err != nil {
		return nil, err
	}
	return p, nil
}

// openChannel (re)opens the underlying channel and switches it into confirm mode.
func (p *ConfirmingPublisher) openChannel() error {
	ch, err := p.conn.Channel()
	if err != nil {
		return fmt.Errorf("open channel: %w", err)
	}
	if err := ch.Confirm(false); err != nil {
		_ = ch.Close()
		return fmt.Errorf("enable publisher confirms: %w", err)
	}
	p.mu.Lock()
	p.channel = ch
	p.acks = ch.NotifyPublish(make(chan amqp.Confirmation, 16))
	p.mu.Unlock()
	return nil
}

// encodeBody renders the AMQP body for a delivery. The body IS the flat
// payload (ARCHITECTURE §5.3) — the worker parses it straight into its
// TaskExecuteMessage — so we serialise `env.Payload`, never the Envelope.
// Extracted as a seam so the wire contract is unit-testable without a channel.
func encodeBody(env Envelope) ([]byte, error) {
	return json.Marshal(env.Payload)
}

// Publish serialises the payload, injects the current trace context, and
// blocks on the confirm channel. Returns nil on ack, error on nack/timeout.
func (p *ConfirmingPublisher) Publish(ctx context.Context, exchange, routingKey string, env Envelope) error {
	start := time.Now()
	body, err := encodeBody(env)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	headers := amqp.Table{}
	for k, v := range carrier {
		headers[k] = v
	}

	p.mu.Lock()
	ch := p.channel
	acks := p.acks
	p.mu.Unlock()
	if ch == nil {
		p.failure(exchange, "channel_nil")
		return errors.New("publisher channel not open")
	}

	pubErr := ch.PublishWithContext(ctx, exchange, routingKey, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent, // delivery_mode=2 per spec
		MessageId:    env.MsgID,
		Timestamp:    env.OccurredAt,
		Headers:      headers,
		Body:         body,
	})
	if pubErr != nil {
		p.failure(exchange, "publish_error")
		return fmt.Errorf("publish: %w", pubErr)
	}

	timer := time.NewTimer(p.confirmTimeout)
	defer timer.Stop()

	select {
	case conf, ok := <-acks:
		p.metrics.MQPublishDuration.WithLabelValues(exchange).Observe(time.Since(start).Seconds())
		if !ok {
			p.failure(exchange, "confirm_closed")
			return errors.New("publish confirm channel closed")
		}
		if !conf.Ack {
			p.failure(exchange, "nack")
			return fmt.Errorf("broker nacked publish (delivery tag=%d)", conf.DeliveryTag)
		}
		return nil
	case <-timer.C:
		p.failure(exchange, "timeout")
		return fmt.Errorf("publisher confirm timeout after %s", p.confirmTimeout)
	case <-ctx.Done():
		p.failure(exchange, "ctx_done")
		return ctx.Err()
	}
}

func (p *ConfirmingPublisher) failure(exchange, reason string) {
	if p.metrics != nil {
		p.metrics.MQPublishFailures.WithLabelValues(exchange, reason).Inc()
	}
}

// Close releases the publisher's channel.
func (p *ConfirmingPublisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.channel == nil {
		return nil
	}
	return p.channel.Close()
}
