package observability

import "github.com/prometheus/client_golang/prometheus"

// Registry is the process-wide Prometheus registry, served at /metrics.
var Registry = prometheus.NewRegistry()

var (
	HTTPInFlight = registerGauge(prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "http_requests_in_flight",
		Help: "Number of HTTP requests currently being served.",
	}))

	HTTPDuration = registerHistogram(prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"}))

	HTTPRequests = registerCounter(prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total HTTP requests, by method, route and status class.",
	}, []string{"method", "route", "status"}))
)

func registerGauge(g prometheus.Gauge) prometheus.Gauge {
	Registry.MustRegister(g)
	return g
}

func registerHistogram(h *prometheus.HistogramVec) *prometheus.HistogramVec {
	Registry.MustRegister(h)
	return h
}

func registerCounter(c *prometheus.CounterVec) *prometheus.CounterVec {
	Registry.MustRegister(c)
	return c
}

var (
	KafkaMessages = registerCounter(prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "wuzz", Subsystem: "kafka", Name: "messages_total",
		Help: "Kafka messages by topic, direction and result.",
	}, []string{"topic", "direction", "result"}))

	// OutboxPending is the lag of the write side behind the event stream. It is
	// the number to alert on: a growing outbox means consumers are drifting away
	// from the write model.
	OutboxPending = registerGauge(prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "wuzz", Subsystem: "outbox", Name: "pending_events",
		Help: "Events written but not yet published to Kafka.",
	}))
)
