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
		{ExchangeControl, "topic"}, // retyped from direct (add-task-control-api) so workers wildcard-bind by task_id
		{ExchangeEvents, "topic"},
		{ExchangeDLX, "direct"},
		{ExchangeCost, "topic"},
	}

	// retypableExchanges names exchanges whose `kind` was changed by an
	// OpenSpec change relative to their original declaration. `DeclareTopology`
	// pre-deletes each before the declare loop so the broker's existing
	// (potentially stale-type) entity is replaced cleanly without tripping the
	// FAIL-FAST check.
	//
	// MUST be append-only across releases: once an exchange enters this list,
	// future versions MUST keep its entry indefinitely. An operator rolling
	// forward against a database whose corresponding exchange is still the
	// OLD type (because they skipped this version) MUST still be able to
	// recover. Removing entries silently regresses FAIL-FAST on stale envs.
	// Reviewer S12 — bound in api-messaging spec.
	retypableExchanges = []string{
		ExchangeControl, // add-task-control-api: direct → topic
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
	// Pre-delete any exchange whose type changed since its original declare.
	// ExchangeDelete(name, ifUnused, noWait) — we pass ifUnused=false so the
	// delete proceeds even if bindings exist, and noWait=false so the
	// subsequent ExchangeDeclare sees a settled state. The amqp091-go API
	// does NOT have an `ifEmpty` argument (that's queue-deletion semantics).
	// Reviewer S2.
	for _, name := range retypableExchanges {
		if err := ch.ExchangeDelete(name, false /*ifUnused*/, false /*noWait*/); err != nil {
			return fmt.Errorf("pre-delete exchange %s for retyping: %w", name, err)
		}
	}

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
