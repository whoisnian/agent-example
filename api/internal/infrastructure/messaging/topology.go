package messaging

import (
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Exchange / queue names per api-messaging spec & ARCHITECTURE §3.6.
const (
	ExchangeTask    = "task.exchange"
	ExchangeControl = "task.control"
	ExchangeEvents  = "task.events"
	ExchangeDLX     = "task.dlx"
	ExchangeCost    = "cost.exchange"

	QueueTaskEvents = "q.task.events"
	QueueCostEvents = "q.cost.events"
	QueueTaskDLQ    = "q.task.dlq"
)

// exchangeDecl describes one declaration entry; kept private to enforce that
// callers go through DeclareTopology instead of hand-rolling Declare calls.
type exchangeDecl struct {
	name string
	kind string
}

// queueDecl describes a queue + its bindings.
type queueDecl struct {
	name     string
	bindings []binding
}

type binding struct {
	exchange string
	key      string
}

var (
	exchanges = []exchangeDecl{
		{ExchangeTask, "topic"},
		{ExchangeControl, "direct"},
		{ExchangeEvents, "topic"},
		{ExchangeDLX, "direct"},
		{ExchangeCost, "topic"},
	}

	queues = []queueDecl{
		{QueueTaskEvents, []binding{{ExchangeEvents, "event.#"}}},
		{QueueCostEvents, []binding{{ExchangeCost, "cost.#"}}},
		{QueueTaskDLQ, []binding{{ExchangeDLX, ""}}}, // direct-dlx: empty routing key
	}
)

// DeclareTopology idempotently declares the API-side exchanges, queues, and
// bindings. Per spec, queues are `quorum` type. The function fails fast on
// incompatible existing entities so operators can spot drift early.
func DeclareTopology(ch *amqp.Channel) error {
	// Exchanges: durable=true, autoDelete=false, internal=false.
	for _, e := range exchanges {
		if err := ch.ExchangeDeclare(e.name, e.kind, true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare exchange %s (%s): %w", e.name, e.kind, err)
		}
	}

	// Queues: durable=true, autoDelete=false, exclusive=false, args declare quorum type.
	args := amqp.Table{"x-queue-type": "quorum"}
	for _, q := range queues {
		if _, err := ch.QueueDeclare(q.name, true, false, false, false, args); err != nil {
			return fmt.Errorf("declare queue %s: %w", q.name, err)
		}
		for _, b := range q.bindings {
			if err := ch.QueueBind(q.name, b.key, b.exchange, false, nil); err != nil {
				return fmt.Errorf("bind queue %s -> %s (%s): %w", q.name, b.exchange, b.key, err)
			}
		}
	}
	return nil
}
