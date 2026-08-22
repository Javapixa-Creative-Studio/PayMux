// Package metrics exposes PayMux's Prometheus instrumentation (PRD §67).
//
// Every metric here answers an operational question someone would otherwise
// have to reconstruct from logs: is the API healthy, is the gateway
// responding, are webhooks getting through. Labels are kept deliberately
// coarse — route patterns rather than paths, outcomes rather than status
// codes — because a payment identifier in a label would make the time series
// unbounded and the metric useless.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds PayMux's collectors and the registry they belong to.
//
// It is passed explicitly rather than kept in a package global so tests can
// build an isolated set, and so a process that has metrics disabled simply
// holds a nil pointer.
type Metrics struct {
	registry *prometheus.Registry

	httpRequests   *prometheus.CounterVec
	httpDuration   *prometheus.HistogramVec
	paymentsTotal  *prometheus.CounterVec
	gatewayTotal   *prometheus.CounterVec
	gatewayLatency *prometheus.HistogramVec
	webhooksTotal  *prometheus.CounterVec
	deliveryTotal  *prometheus.CounterVec
	deliveryFailed *prometheus.CounterVec
	deliveryTime   *prometheus.HistogramVec
	queueDepth     *prometheus.GaugeVec
}

// New builds the collectors and registers them.
func New() *Metrics {
	registry := prometheus.NewRegistry()

	// Process and Go runtime metrics come for free and are what an operator
	// reaches for first when a service misbehaves.
	registry.MustRegister(
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		collectors.NewGoCollector(),
	)

	m := &Metrics{
		registry: registry,

		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "paymux_http_requests_total",
			Help: "HTTP requests handled, by route pattern and status class.",
		}, []string{"method", "route", "status"}),

		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "paymux_http_request_duration_seconds",
			Help:    "How long PayMux took to handle a request.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),

		paymentsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "paymux_payments_created_total",
			Help: "Payments opened at a gateway, by gateway and outcome.",
		}, []string{"gateway", "outcome"}),

		gatewayTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "paymux_gateway_requests_total",
			Help: "Requests PayMux made to a payment gateway.",
		}, []string{"gateway", "operation", "outcome"}),

		gatewayLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "paymux_gateway_request_duration_seconds",
			Help:    "How long a payment gateway took to answer.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		}, []string{"gateway", "operation"}),

		webhooksTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "paymux_webhook_received_total",
			Help: "Gateway notifications received, by how PayMux routed them.",
		}, []string{"gateway", "routing"}),

		deliveryTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "paymux_delivery_total",
			Help: "Webhook delivery attempts, by outcome.",
		}, []string{"outcome"}),

		deliveryFailed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "paymux_delivery_failures_total",
			Help: "Webhook deliveries that failed, by reason.",
		}, []string{"reason"}),

		deliveryTime: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "paymux_delivery_duration_seconds",
			Help:    "How long an application took to accept a delivery.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		}, []string{"outcome"}),

		queueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "paymux_delivery_queue_depth",
			Help: "Deliveries waiting in the queue, by state.",
		}, []string{"state"}),
	}

	registry.MustRegister(
		m.httpRequests, m.httpDuration,
		m.paymentsTotal,
		m.gatewayTotal, m.gatewayLatency,
		m.webhooksTotal,
		m.deliveryTotal, m.deliveryFailed, m.deliveryTime,
		m.queueDepth,
	)
	return m
}

// Handler serves the metrics endpoint.
func (m *Metrics) Handler() http.Handler {
	if m == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "metrics are disabled", http.StatusNotFound)
		})
	}
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		// A broken collector should not take the endpoint down; report it and
		// serve what still works.
		ErrorHandling: promhttp.ContinueOnError,
	})
}

// Registry exposes the underlying registry, for tests.
func (m *Metrics) Registry() *prometheus.Registry {
	if m == nil {
		return nil
	}
	return m.registry
}

// Every recorder below tolerates a nil receiver, so instrumentation can be
// called unconditionally and disabling metrics costs one nil check.

// RecordHTTPRequest records a handled request.
func (m *Metrics) RecordHTTPRequest(method, route string, status int, duration time.Duration) {
	if m == nil {
		return
	}
	if route == "" {
		// An unmatched path would otherwise create a series per URL.
		route = "unmatched"
	}
	m.httpRequests.WithLabelValues(method, route, statusClass(status)).Inc()
	m.httpDuration.WithLabelValues(method, route).Observe(duration.Seconds())
}

// RecordPaymentCreated records the outcome of opening a payment.
func (m *Metrics) RecordPaymentCreated(gateway string, err error) {
	if m == nil {
		return
	}
	m.paymentsTotal.WithLabelValues(gateway, outcome(err)).Inc()
}

// RecordGatewayRequest records a call PayMux made to a gateway.
func (m *Metrics) RecordGatewayRequest(gateway, operation string, duration time.Duration, err error) {
	if m == nil {
		return
	}
	m.gatewayTotal.WithLabelValues(gateway, operation, outcome(err)).Inc()
	m.gatewayLatency.WithLabelValues(gateway, operation).Observe(duration.Seconds())
}

// RecordWebhookReceived records an inbound gateway notification and what
// PayMux did with it.
func (m *Metrics) RecordWebhookReceived(gateway, routing string) {
	if m == nil {
		return
	}
	m.webhooksTotal.WithLabelValues(gateway, routing).Inc()
}

// RecordDelivery records one delivery attempt.
func (m *Metrics) RecordDelivery(result string, duration time.Duration) {
	if m == nil {
		return
	}
	m.deliveryTotal.WithLabelValues(result).Inc()
	m.deliveryTime.WithLabelValues(result).Observe(duration.Seconds())
}

// RecordDeliveryFailure records why a delivery did not succeed.
func (m *Metrics) RecordDeliveryFailure(reason string) {
	if m == nil {
		return
	}
	m.deliveryFailed.WithLabelValues(reason).Inc()
}

// SetQueueDepth reports how many deliveries are waiting in each state.
func (m *Metrics) SetQueueDepth(state string, count float64) {
	if m == nil {
		return
	}
	m.queueDepth.WithLabelValues(state).Set(count)
}

// statusClass collapses a status code to its class.
//
// Recording the exact code would multiply every route's series by the number
// of codes it can return, for information a log line already carries.
func statusClass(status int) string {
	switch {
	case status >= 500:
		return "5xx"
	case status >= 400:
		return "4xx"
	case status >= 300:
		return "3xx"
	case status >= 200:
		return "2xx"
	default:
		return strconv.Itoa(status)
	}
}

func outcome(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}
