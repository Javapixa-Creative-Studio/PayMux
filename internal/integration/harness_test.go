// Package integration exercises PayMux end to end against a real PostgreSQL
// database and a stub Midtrans.
//
// These tests are the ones that can actually prove the properties the product
// depends on — application isolation, idempotency, event ordering, signed
// delivery and retries — because those properties live in database constraints
// and concurrent behaviour that a unit test cannot reach.
//
// They are skipped unless PAYMUX_TEST_DATABASE_URL points at a disposable
// database, so the ordinary test suite needs no external services (PRD §86).
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anggapixa/paymux/internal/api"
	"github.com/anggapixa/paymux/internal/app"
	"github.com/anggapixa/paymux/internal/config"
	"github.com/anggapixa/paymux/internal/crypto"
	"github.com/anggapixa/paymux/internal/gateway"
	"github.com/anggapixa/paymux/internal/gateway/midtrans"
	"github.com/anggapixa/paymux/internal/storage"
)

const (
	testServerKey = "SB-Mid-server-integration-key"
	testAdminMail = "admin@paymux.test"
	testAdminPass = "correct horse battery staple"
)

// harness is one fully wired PayMux instance under test.
type harness struct {
	t         *testing.T
	container *app.Container
	server    *httptest.Server
	midtrans  *stubMidtrans
	db        *storage.DB
}

// newHarness builds PayMux against the test database and a stub gateway.
func newHarness(t *testing.T) *harness {
	t.Helper()

	databaseURL := os.Getenv("PAYMUX_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set PAYMUX_TEST_DATABASE_URL to run integration tests")
	}

	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("PAYMUX_ENCRYPTION_KEY", strings.Repeat("ab", 32))
	// Destinations in these tests are local test servers, so the SSRF guard is
	// relaxed exactly as a self-hosted deployment would (PRD §73).
	t.Setenv("PAYMUX_ALLOW_PRIVATE_WEBHOOK_DESTINATIONS", "true")
	t.Setenv("PAYMUX_WORKER_POLL_INTERVAL", "50ms")
	t.Setenv("PAYMUX_LOG_LEVEL", "error")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	ctx := context.Background()
	db, err := storage.Connect(ctx, storage.Options{URL: cfg.DatabaseURL, MaxConns: 10, MinConns: 1})
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(db.Close)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	truncateAll(t, db)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	container, err := app.Build(cfg, db, logger)
	if err != nil {
		t.Fatalf("build container: %v", err)
	}

	stub := newStubMidtrans(t)

	// Point the Midtrans adapter at the stub. Registering over the real
	// factory is how the registry supports test doubles.
	container.Gateways.Register(midtrans.Name, func(acc *gateway.Account, client *http.Client) (gateway.Gateway, error) {
		adapter, err := midtrans.NewAdapter(acc, stub.server.Client())
		if err != nil {
			return nil, err
		}
		return stub.rewire(adapter), nil
	})

	server := httptest.NewServer(api.New(api.Deps{
		Config:           cfg,
		DB:               db,
		Logger:           logger,
		Auth:             container.Auth,
		AuthMiddleware:   container.AuthMiddleware,
		Applications:     container.Applications,
		GatewayAccounts:  container.GatewayAccounts,
		Gateways:         container.Gateways,
		Auditor:          container.Auditor,
		Payments:         container.Payments,
		PaymentRepo:      container.PaymentRepo,
		Idempotency:      container.Idempotency,
		Events:           container.Events,
		Deliveries:       container.Deliveries,
		Notifications:    container.Notifications,
		NotificationRepo: container.NotificationRepo,
		Subscriptions:    container.Subscriptions,
	}))
	t.Cleanup(server.Close)

	return &harness{t: t, container: container, server: server, midtrans: stub, db: db}
}

// truncateAll empties every table so each test starts from a clean state.
func truncateAll(t *testing.T, db *storage.DB) {
	t.Helper()
	_, err := db.Pool().Exec(context.Background(), `
		TRUNCATE
			audit_logs, delivery_attempts, deliveries, events, gateway_events,
			gateway_transactions, refunds, subscriptions, idempotency_keys,
			payment_items, payment_customers, payments,
			webhook_destinations, application_api_keys, applications,
			gateway_accounts, admin_sessions, admins
		RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("truncate test database: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Stub Midtrans
// ---------------------------------------------------------------------------

// stubMidtrans stands in for Midtrans. It records what PayMux sent and replies
// the way the real service does, including its habit of reporting failures in
// the body of a 200 response.
type stubMidtrans struct {
	server *httptest.Server

	mu           sync.Mutex
	snapRequests []map[string]any
	coreCalls    []string
	transactions map[string]map[string]any

	// failNextSnap makes the next Snap call fail, for testing rollback.
	failNextSnap bool
}

func newStubMidtrans(t *testing.T) *stubMidtrans {
	t.Helper()
	stub := &stubMidtrans{transactions: make(map[string]map[string]any)}
	stub.server = httptest.NewServer(http.HandlerFunc(stub.handle))
	t.Cleanup(stub.server.Close)
	return stub
}

// rewire points an adapter's client at the stub.
func (s *stubMidtrans) rewire(adapter gateway.Gateway) gateway.Gateway {
	if a, ok := adapter.(*midtrans.Adapter); ok {
		a.SetBaseURLs(s.server.URL, s.server.URL)
	}
	return adapter
}

func (s *stubMidtrans) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch {
	case r.URL.Path == "/snap/v1/transactions":
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		s.snapRequests = append(s.snapRequests, body)

		if s.failNextSnap {
			s.failNextSnap = false
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"status_code":    "400",
				"error_messages": []string{"transaction_details.gross_amount is not valid"},
			})
			return
		}

		details, _ := body["transaction_details"].(map[string]any)
		orderID, _ := details["order_id"].(string)
		grossAmount, _ := details["gross_amount"].(string)
		s.transactions[orderID] = map[string]any{
			"order_id":     orderID,
			"gross_amount": grossAmount,
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"token":        "snap-token-" + orderID,
			"redirect_url": "https://app.sandbox.midtrans.com/snap/v4/redirection/snap-token-" + orderID,
		})

	case strings.HasPrefix(r.URL.Path, "/v2/"):
		s.coreCalls = append(s.coreCalls, r.Method+" "+r.URL.Path)
		s.handleCore(w, r)

	default:
		writeJSON(w, http.StatusNotFound, map[string]any{
			"status_code": "404", "status_message": "not found",
		})
	}
}

func (s *stubMidtrans) handleCore(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		writeJSON(w, http.StatusNotFound, map[string]any{"status_code": "404"})
		return
	}
	orderID, action := parts[1], parts[2]

	txn, ok := s.transactions[orderID]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"status_code": "404", "status_message": "Transaction doesn't exist.",
		})
		return
	}

	status := "pending"
	switch action {
	case "cancel":
		status = "cancel"
	case "expire":
		status = "expire"
	case "refund":
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)

		amount, _ := body["amount"].(float64)
		refundKey, _ := body["refund_key"].(string)
		gross, _ := txn["gross_amount"].(string)

		status = "partial_refund"
		refundedTotal := fmt.Sprintf("%.2f", amount)
		if fmt.Sprintf("%.2f", amount) == gross+".00" || gross == fmt.Sprintf("%.0f", amount) {
			status = "refund"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status_code":        "200",
			"order_id":           orderID,
			"transaction_id":     "txn-" + orderID,
			"gross_amount":       gross,
			"transaction_status": status,
			"refund_amount":      refundedTotal,
			"refunds": []map[string]any{
				{"refund_key": refundKey, "refund_amount": refundedTotal},
			},
		})
		return
	case "status":
		if recorded, ok := txn["transaction_status"].(string); ok {
			status = recorded
		}
	}
	txn["transaction_status"] = status

	writeJSON(w, http.StatusOK, map[string]any{
		"status_code":        "200",
		"order_id":           orderID,
		"transaction_id":     "txn-" + orderID,
		"gross_amount":       txn["gross_amount"],
		"payment_type":       "bank_transfer",
		"transaction_status": status,
		"transaction_time":   "2026-08-20 10:00:00",
	})
}

// setStatus makes the stub report a status for an order on the next lookup.
func (s *stubMidtrans) setStatus(orderID, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if txn, ok := s.transactions[orderID]; ok {
		txn["transaction_status"] = status
	}
}

func (s *stubMidtrans) snapRequestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.snapRequests)
}

func (s *stubMidtrans) lastSnapRequest() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.snapRequests) == 0 {
		return nil
	}
	return s.snapRequests[len(s.snapRequests)-1]
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

// request performs an HTTP call against the PayMux instance under test.
func (h *harness) request(method, path string, body any, opts ...func(*http.Request)) (*http.Response, []byte) {
	h.t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("encode request: %v", err)
		}
		reader = strings.NewReader(string(encoded))
	}

	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		h.t.Fatalf("build request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, opt := range opts {
		opt(req)
	}

	resp, err := h.server.Client().Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatalf("read response: %v", err)
	}
	return resp, raw
}

// withKey authenticates a request with an application API key.
func withKey(key string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+key) }
}

// withHeader sets an arbitrary header.
func withHeader(name, value string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set(name, value) }
}

// decode unmarshals a JSON response body.
func decode[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode response: %v (%s)", err, string(raw))
	}
	return out
}

// requireStatus fails the test unless the response has the expected status.
func requireStatus(t *testing.T, resp *http.Response, raw []byte, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("%s %s = %d, want %d: %s",
			resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, want, string(raw))
	}
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

type testApplication struct {
	ID            string
	Slug          string
	Key           string
	DestinationID string
	Secret        string
	Receiver      *webhookReceiver
}

// setupGatewayAccount configures a sandbox Midtrans account.
func (h *harness) setupGatewayAccount() string {
	h.t.Helper()
	acc := &gateway.Account{
		Gateway:     midtrans.Name,
		Name:        "midtrans-sandbox",
		Environment: gateway.Sandbox,
		MerchantID:  "M123456",
		ClientKey:   "SB-Mid-client-abc",
		ServerKey:   crypto.Secret(testServerKey),
		Enabled:     true,
		IsDefault:   true,
	}
	if err := h.container.GatewayAccounts.Create(context.Background(), acc); err != nil {
		h.t.Fatalf("create gateway account: %v", err)
	}
	return acc.ID
}

// setupApplication creates an application with a test key and a destination
// pointed at a receiver this test controls.
func (h *harness) setupApplication(name, slug string) *testApplication {
	h.t.Helper()
	ctx := context.Background()

	app, err := h.container.Applications.Create(ctx, applicationInput(name, slug))
	if err != nil {
		h.t.Fatalf("create application %s: %v", slug, err)
	}

	issued, err := h.container.Applications.CreateAPIKey(ctx, app.ID, "integration", crypto.KeyModeTest, nil)
	if err != nil {
		h.t.Fatalf("create api key for %s: %v", slug, err)
	}

	receiver := newWebhookReceiver(h.t)
	destination, err := h.container.Applications.CreateDestination(ctx, app.ID, destinationInput(receiver.URL()))
	if err != nil {
		h.t.Fatalf("create destination for %s: %v", slug, err)
	}

	return &testApplication{
		ID:            app.ID,
		Slug:          app.Slug,
		Key:           issued.Plaintext.Reveal(),
		DestinationID: destination.ID,
		Secret:        destination.Secret.Reveal(),
		Receiver:      receiver,
	}
}

// runWorker starts the delivery worker for the duration of a test.
func (h *harness) runWorker() {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	worker := newTestWorker(h.container)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = worker.Run(ctx)
	}()
	h.t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			h.t.Error("delivery worker did not stop within five seconds")
		}
	})
}

// eventually retries a condition until it holds or the deadline passes. It is
// how the tests wait for asynchronous delivery without sleeping blindly.
func eventually(t *testing.T, timeout time.Duration, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}
