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
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}
