package metrics

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestNilMetricsIsUsable(t *testing.T) {
	// Instrumentation is called unconditionally throughout the domain, so a
	// disabled collector must be safe rather than a nil-pointer panic.
	var m *Metrics
	m.RecordHTTPRequest("GET", "/health", 200, time.Millisecond)
	m.RecordPaymentCreated("midtrans", nil)
	m.RecordGatewayRequest("midtrans", "snap.create", time.Millisecond, nil)
	m.RecordWebhookReceived("midtrans", "routed")
	m.RecordDelivery("succeeded", time.Millisecond)
	m.RecordDeliveryFailure("transport")
	m.SetQueueDepth("pending", 3)

	if m.Registry() != nil {
		t.Error("a disabled collector should have no registry")
	}

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("disabled metrics endpoint = %d, want 404", rr.Code)
	}
}

func TestHTTPRequestsAreCountedByStatusClass(t *testing.T) {
	m := New()
	m.RecordHTTPRequest("GET", "/api/v1/payments", 200, 10*time.Millisecond)
	m.RecordHTTPRequest("GET", "/api/v1/payments", 201, 10*time.Millisecond)
	m.RecordHTTPRequest("GET", "/api/v1/payments", 404, 10*time.Millisecond)

	// 200 and 201 collapse into one series; recording the exact code would
	// multiply every route's series for information the logs already carry.
	if got := testutil.ToFloat64(m.httpRequests.WithLabelValues("GET", "/api/v1/payments", "2xx")); got != 2 {
		t.Errorf("2xx count = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.httpRequests.WithLabelValues("GET", "/api/v1/payments", "4xx")); got != 1 {
		t.Errorf("4xx count = %v, want 1", got)
	}
}

func TestUnmatchedRoutesShareOneSeries(t *testing.T) {
	m := New()
	// Without this, every probe of a nonexistent URL would create a series.
	m.RecordHTTPRequest("GET", "", 404, time.Millisecond)
	m.RecordHTTPRequest("GET", "", 404, time.Millisecond)

	if got := testutil.ToFloat64(m.httpRequests.WithLabelValues("GET", "unmatched", "4xx")); got != 2 {
		t.Errorf("unmatched count = %v, want 2", got)
	}
}

func TestOutcomesAreDerivedFromErrors(t *testing.T) {
	m := New()
	m.RecordPaymentCreated("midtrans", nil)
	m.RecordPaymentCreated("midtrans", errors.New("gateway rejected the request"))
	m.RecordGatewayRequest("midtrans", "snap.create", time.Millisecond, nil)

	if got := testutil.ToFloat64(m.paymentsTotal.WithLabelValues("midtrans", "success")); got != 1 {
		t.Errorf("successful payments = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.paymentsTotal.WithLabelValues("midtrans", "error")); got != 1 {
		t.Errorf("failed payments = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.gatewayTotal.WithLabelValues("midtrans", "snap.create", "success")); got != 1 {
		t.Errorf("gateway requests = %v, want 1", got)
	}
}

func TestHandlerExposesTheDocumentedMetricNames(t *testing.T) {
	m := New()
	m.RecordHTTPRequest("POST", "/api/v1/payments", 201, 5*time.Millisecond)
	m.RecordPaymentCreated("midtrans", nil)
	m.RecordGatewayRequest("midtrans", "snap.create", 5*time.Millisecond, nil)
	m.RecordWebhookReceived("midtrans", "routed")
	m.RecordDelivery("succeeded", 5*time.Millisecond)
	m.RecordDeliveryFailure("destination_5xx")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("metrics endpoint = %d", rr.Code)
	}

	// These names are what an operator's dashboards and alerts reference, so
	// they are part of the contract (PRD §67).
	body := rr.Body.String()
	for _, name := range []string{
		"paymux_http_requests_total",
		"paymux_payments_created_total",
		"paymux_gateway_requests_total",
		"paymux_webhook_received_total",
		"paymux_delivery_total",
		"paymux_delivery_failures_total",
		"paymux_delivery_duration_seconds",
	} {
		if !strings.Contains(body, name) {
			t.Errorf("%s is missing from the metrics output", name)
		}
	}
}

func TestQueueDepthIsAGaugeNotACounter(t *testing.T) {
	m := New()
	m.SetQueueDepth("pending", 12)
	m.SetQueueDepth("pending", 4)

	// Depth is a level, so a later reading replaces the earlier one.
	if got := testutil.ToFloat64(m.queueDepth.WithLabelValues("pending")); got != 4 {
		t.Errorf("queue depth = %v, want 4", got)
	}
}

func TestStatusClass(t *testing.T) {
	cases := map[int]string{200: "2xx", 204: "2xx", 302: "3xx", 400: "4xx", 404: "4xx", 500: "5xx", 503: "5xx"}
	for status, want := range cases {
		if got := statusClass(status); got != want {
			t.Errorf("statusClass(%d) = %q, want %q", status, got, want)
		}
	}
}
