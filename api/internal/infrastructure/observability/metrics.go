package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Metrics groups every prometheus collector the service publishes.
//
// The registry is owned by this struct and exposed via `Registry()`. We do not
// touch `prometheus.DefaultRegisterer` so tests can stand up isolated metric
// state.
type Metrics struct {
	Registry *prometheus.Registry

	// HTTP-layer metrics
	HTTPRequestsTotal   *prometheus.CounterVec
	HTTPRequestDuration *prometheus.HistogramVec
	HTTPPanicsTotal     prometheus.Counter

	// Messaging / outbox metrics
	MQPublishDuration   *prometheus.HistogramVec
	MQPublishFailures   *prometheus.CounterVec
	MQOutboxPending     prometheus.Gauge
	MQOutboxPublished   prometheus.Counter
	OutboxFailedTotal   prometheus.Counter
	OutboxRelayerLeader prometheus.Gauge

	// Task-write-api business metrics
	TasksCreatedTotal  *prometheus.CounterVec
	TasksIteratedTotal *prometheus.CounterVec

	// Event-ingest / status-sync metrics
	EventsIngestedTotal         *prometheus.CounterVec
	EventStatusTransitionsTotal prometheus.Counter
	EventIngestMalformedTotal   prometheus.Counter
	EventConsumerConnected      prometheus.Gauge

	// Cost-ingest / settlement metrics (add-cost-service §"Observability Metrics")
	CostEventsConsumedTotal        *prometheus.CounterVec
	CostEventsSettledTotal         *prometheus.CounterVec
	CostPricingMissingTotal        *prometheus.CounterVec
	CostAmountSettledUSDTotal      prometheus.Counter
	CostEventSettleDurationSeconds prometheus.Histogram
	CostConsumerConnected          prometheus.Gauge

	// Task control metrics (add-task-control-api §"Observability")
	TaskControlRequestsTotal *prometheus.CounterVec
}

// NewMetrics builds the registry and every collector.
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()

	m := &Metrics{
		Registry: reg,
		HTTPRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_requests_total",
				Help: "Total HTTP requests handled, labelled by route/method/status.",
			},
			[]string{"route", "method", "status"},
		),
		HTTPRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "http_request_duration_seconds",
				Help:    "HTTP request latency in seconds.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"route", "method"},
		),
		HTTPPanicsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "http_panics_total",
			Help: "Total panics recovered by the HTTP recovery middleware.",
		}),
		MQPublishDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "mq_publish_duration_seconds",
				Help:    "Time spent publishing a message and awaiting confirm.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"exchange"},
		),
		MQPublishFailures: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "mq_publish_failures_total",
				Help: "Failed publish attempts (nack / timeout / channel-closed).",
			},
			[]string{"exchange", "reason"},
		),
		MQOutboxPending: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "mq_outbox_pending",
			Help: "Current outbox rows in pending status.",
		}),
		MQOutboxPublished: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mq_outbox_published_total",
			Help: "Total outbox rows successfully published.",
		}),
		OutboxFailedTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "outbox_failed_total",
			Help: "Total outbox rows moved to status=failed after exhausting retries.",
		}),
		OutboxRelayerLeader: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "outbox_relayer_lock_owner",
			Help: "1 when this replica currently holds the relayer advisory lock, else 0.",
		}),
		TasksCreatedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "tasks_created_total",
				Help: "Tasks successfully created via POST /api/v1/tasks, labelled by task_type.",
			},
			[]string{"task_type"},
		),
		TasksIteratedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "tasks_iterated_total",
				Help: "Iterate-task attempts via POST /api/v1/tasks/{id}/iterate. Outcome: success|conflict|not_found|invalid|error.",
			},
			[]string{"outcome"},
		),
		EventsIngestedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "events_ingested_total",
				Help: "Worker task events successfully persisted, labelled by kind.",
			},
			[]string{"kind"},
		),
		EventStatusTransitionsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "event_status_transitions_total",
			Help: "Version/task state-machine transitions actually applied from ingested events.",
		}),
		EventIngestMalformedTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "event_ingest_malformed_total",
			Help: "Undecodable / invalid task-event deliveries dead-lettered without requeue.",
		}),
		EventConsumerConnected: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "event_consumer_connected",
			Help: "1 when the task-events consumer is subscribed, else 0.",
		}),
		CostEventsConsumedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cost_events_consumed_total",
				Help: "Worker cost events received by the Cost Service, labelled by kind.",
			},
			[]string{"kind"},
		),
		CostEventsSettledTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cost_events_settled_total",
				Help: "Cost events settled by the Cost Service. result: ok|missing_pricing|duplicate|error.",
			},
			[]string{"kind", "result"},
		),
		CostPricingMissingTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cost_pricing_missing_total",
				Help: "Cost events whose (kind, resource_name) had no pricing row at occurred_at — settled with amount_usd=0 and pricing_id NULL.",
			},
			[]string{"kind", "resource"},
		),
		CostAmountSettledUSDTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cost_amount_settled_usd_total",
			Help: "Cumulative USD amount across successfully settled cost events (best-effort float64; exact value lives in DB).",
		}),
		CostEventSettleDurationSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "cost_event_settle_duration_seconds",
			Help:    "End-to-end per-delivery settlement latency (pricing lookup + tx).",
			Buckets: prometheus.DefBuckets,
		}),
		CostConsumerConnected: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cost_consumer_connected",
			Help: "1 when the cost-events consumer is subscribed, else 0. Independent of event_consumer_connected.",
		}),
		TaskControlRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "task_control_requests_total",
				Help: "POST /tasks/{id}/control requests. action ∈ {pause,resume,cancel,unknown}; outcome ∈ {accepted,conflict,not_found,invalid}; unknown action pairs only with invalid outcome.",
			},
			[]string{"action", "outcome"},
		),
	}

	reg.MustRegister(
		m.HTTPRequestsTotal,
		m.HTTPRequestDuration,
		m.HTTPPanicsTotal,
		m.MQPublishDuration,
		m.MQPublishFailures,
		m.MQOutboxPending,
		m.MQOutboxPublished,
		m.OutboxFailedTotal,
		m.OutboxRelayerLeader,
		m.TasksCreatedTotal,
		m.TasksIteratedTotal,
		m.EventsIngestedTotal,
		m.EventStatusTransitionsTotal,
		m.EventIngestMalformedTotal,
		m.EventConsumerConnected,
		m.CostEventsConsumedTotal,
		m.CostEventsSettledTotal,
		m.CostPricingMissingTotal,
		m.CostAmountSettledUSDTotal,
		m.CostEventSettleDurationSeconds,
		m.CostConsumerConnected,
		m.TaskControlRequestsTotal,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}
