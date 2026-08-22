package integration

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/anggapixa/paymux/internal/crypto"
	"github.com/anggapixa/paymux/internal/gateway/midtrans"
)

const millisecond = time.Millisecond

// createPaymentBody is the request an application sends to open a payment.
func createPaymentBody(orderID string, amount int64) map[string]any {
	return map[string]any{
		"application_order_id": orderID,
		"amount":               amount,
		"currency":             "IDR",
		"customer": map[string]any{
			"first_name": "John",
			"last_name":  "Doe",
			"email":      "john@example.com",
			"phone":      "+628123456789",
		},
		"items": []map[string]any{
			{"id": "PROD-001", "name": "Example Product", "price": amount, "quantity": 1},
		},
	}
}

// signedNotification builds a signed Midtrans notification for an order.
func signedNotification(orderID, grossAmount, status, fraud string) map[string]any {
	signature := midtrans.Signature(midtrans.SignatureInput{
		OrderID:     orderID,
		StatusCode:  "200",
		GrossAmount: grossAmount,
	}, crypto.Secret(testServerKey))

	return map[string]any{
		"status_code":        "200",
		"status_message":     "midtrans payment notification",
		"order_id":           orderID,
		"transaction_id":     "txn-" + orderID,
		"gross_amount":       grossAmount,
		"currency":           "IDR",
		"payment_type":       "bank_transfer",
		"transaction_time":   "2026-08-20 10:00:00",
		"settlement_time":    "2026-08-20 10:05:00",
		"transaction_status": status,
		"fraud_status":       fraud,
		"signature_key":      signature,
	}
}

// TestAcceptanceScenario walks the whole PRD §90 scenario: three products
// share one Midtrans account, one of them takes a payment, and only that one
// hears about it.
func TestAcceptanceScenario(t *testing.T) {
	h := newHarness(t)
	h.setupGatewayAccount()

	productA := h.setupApplication("Product A", "product-a")
	productB := h.setupApplication("Product B", "product-b")
	productC := h.setupApplication("Product C", "product-c")

	h.runWorker()

	// Product B opens a payment through PayMux rather than calling Midtrans.
	resp, raw := h.request(http.MethodPost, "/api/v1/payments",
		createPaymentBody("INV-000123", 150000), withKey(productB.Key))
	requireStatus(t, resp, raw, http.StatusCreated)

	payment := decode[map[string]any](t, raw)
	paymentID, _ := payment["id"].(string)
	gatewayOrderID, _ := payment["gateway_order_id"].(string)

	if paymentID == "" || gatewayOrderID == "" {
		t.Fatalf("payment is missing identifiers: %s", raw)
	}
	if token, _ := payment["snap_token"].(string); token == "" {
		t.Error("no Snap token was returned; the application could not open checkout")
	}
	if redirect, _ := payment["redirect_url"].(string); redirect == "" {
		t.Error("no redirect URL was returned; redirect checkout would be impossible")
	}
	if status, _ := payment["status"].(string); status != "PENDING" {
		t.Errorf("new payment status = %v, want PENDING", payment["status"])
	}
	if got, _ := payment["application_order_id"].(string); got != "INV-000123" {
		t.Errorf("application_order_id = %q", got)
	}

	// PayMux, not the application, chose the order id sent to the gateway.
	sent := h.midtrans.lastSnapRequest()
	details, _ := sent["transaction_details"].(map[string]any)
	if orderID, _ := details["order_id"].(string); orderID != gatewayOrderID {
		t.Errorf("gateway received order_id %q, want PayMux's %q", orderID, gatewayOrderID)
	}
	if amount, _ := details["gross_amount"].(string); amount != "150000" {
		t.Errorf("gateway received gross_amount %q, want 150000", amount)
	}

	// The customer pays; Midtrans notifies PayMux.
	resp, raw = h.request(http.MethodPost, "/webhooks/midtrans",
		signedNotification(gatewayOrderID, "150000.00", "settlement", "accept"))
	requireStatus(t, resp, raw, http.StatusOK)

	// Product B receives signed events: payment.created when the payment was
	// opened, then payment.paid once Midtrans reported settlement.
	eventually(t, 5*time.Second, "Product B to receive payment.paid", func() bool {
		return findDelivered(productB.Receiver.all(), "payment.paid") != nil
	})

	if findDelivered(productB.Receiver.all(), "payment.created") == nil {
		t.Error("payment.created was never delivered")
	}
	delivered := findDelivered(productB.Receiver.all(), "payment.paid")

	timestamp, err := strconv.ParseInt(delivered.Timestamp, 10, 64)
	if err != nil {
		t.Fatalf("delivery timestamp is not a unix time: %q", delivered.Timestamp)
	}
	deliveryID := delivered.Headers.Get("PayMux-Delivery-Id")
	if !delivered.verifySignature(productB.Secret, deliveryID, timestamp) {
		t.Error("the delivered webhook signature did not verify against the destination secret")
	}
	// The signature must be bound to this destination's secret alone.
	if delivered.verifySignature(productA.Secret, deliveryID, timestamp) {
		t.Error("the webhook verified under another application's secret")
	}

	var payload map[string]any
	if err := json.Unmarshal(delivered.Body, &payload); err != nil {
		t.Fatalf("delivered payload is not JSON: %v", err)
	}
	if payload["payment_id"] != paymentID {
		t.Errorf("delivered payment_id = %v, want %s", payload["payment_id"], paymentID)
	}
	if payload["application_id"] != productB.ID {
		t.Errorf("delivered application_id = %v, want %s", payload["application_id"], productB.ID)
	}
	if payload["status"] != "PAID" {
		t.Errorf("delivered status = %v, want PAID", payload["status"])
	}
	if payload["application_order_id"] != "INV-000123" {
		t.Errorf("delivered application_order_id = %v", payload["application_order_id"])
	}

	// Products A and C hear nothing: the event belongs to B alone (PRD §43).
	time.Sleep(300 * time.Millisecond)
	if productA.Receiver.count() != 0 {
		t.Errorf("Product A received %d events it does not own", productA.Receiver.count())
	}
	if productC.Receiver.count() != 0 {
		t.Errorf("Product C received %d events it does not own", productC.Receiver.count())
	}

	// Product B can read the payment back and sees the settled state.
	resp, raw = h.request(http.MethodGet, "/api/v1/payments/"+paymentID, nil, withKey(productB.Key))
	requireStatus(t, resp, raw, http.StatusOK)

	fetched := decode[map[string]any](t, raw)
	if fetched["status"] != "PAID" {
		t.Errorf("payment status after settlement = %v, want PAID", fetched["status"])
	}
	if fetched["paid_at"] == nil {
		t.Error("paid_at was not recorded")
	}
}

// findDelivered returns the first delivery of the given event type, or nil.
func findDelivered(received []receivedWebhook, eventType string) *receivedWebhook {
	for i := range received {
		if received[i].EventType == eventType {
			return &received[i]
		}
	}
	return nil
}

// TestApplicationIsolation proves one application cannot reach another's data
// (PRD §49).
func TestApplicationIsolation(t *testing.T) {
	h := newHarness(t)
	h.setupGatewayAccount()

	productA := h.setupApplication("Product A", "product-a")
	productB := h.setupApplication("Product B", "product-b")

	resp, raw := h.request(http.MethodPost, "/api/v1/payments",
		createPaymentBody("INV-B-1", 50000), withKey(productB.Key))
	requireStatus(t, resp, raw, http.StatusCreated)
	paymentB := decode[map[string]any](t, raw)
	paymentBID, _ := paymentB["id"].(string)

	// Reading, cancelling and refunding another application's payment must all
	// look exactly like "no such payment", never like "forbidden": a different
	// answer would confirm the identifier exists.
	cases := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/v1/payments/" + paymentBID, nil},
		{http.MethodPost, "/api/v1/payments/" + paymentBID + "/cancel", nil},
		{http.MethodPost, "/api/v1/payments/" + paymentBID + "/sync", nil},
		{http.MethodPost, "/api/v1/payments/" + paymentBID + "/expire", nil},
		{http.MethodGet, "/api/v1/payments/" + paymentBID + "/refunds", nil},
		{http.MethodPost, "/api/v1/payments/" + paymentBID + "/refunds", map[string]any{"amount": 1000}},
	}
	for _, tc := range cases {
		resp, raw := h.request(tc.method, tc.path, tc.body, withKey(productA.Key))
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s as another application = %d, want 404: %s",
				tc.method, tc.path, resp.StatusCode, raw)
		}
	}

	// Listing shows only the caller's own payments.
	resp, raw = h.request(http.MethodGet, "/api/v1/payments", nil, withKey(productA.Key))
	requireStatus(t, resp, raw, http.StatusOK)
	list := decode[struct {
		Data []map[string]any `json:"data"`
	}](t, raw)
	if len(list.Data) != 0 {
		t.Errorf("Product A sees %d payments, want none of its own", len(list.Data))
	}

	// Even asking for another application's payments by filter changes nothing.
	resp, raw = h.request(http.MethodGet, "/api/v1/payments?application_id="+productB.ID, nil, withKey(productA.Key))
	requireStatus(t, resp, raw, http.StatusOK)
	filtered := decode[struct {
		Data []map[string]any `json:"data"`
	}](t, raw)
	if len(filtered.Data) != 0 {
		t.Errorf("an application_id filter leaked %d payments across applications", len(filtered.Data))
	}
}

// TestAuthenticationIsRequired covers the unauthenticated and bad-credential
// paths on the application API.
func TestAuthenticationIsRequired(t *testing.T) {
	h := newHarness(t)
	h.setupGatewayAccount()
	product := h.setupApplication("Product A", "product-a")

	resp, raw := h.request(http.MethodGet, "/api/v1/payments", nil)
	requireStatus(t, resp, raw, http.StatusUnauthorized)

	resp, raw = h.request(http.MethodGet, "/api/v1/payments", nil, withKey("pmx_live_notarealkeyatallnotarealkeyatall"))
	requireStatus(t, resp, raw, http.StatusUnauthorized)

	resp, raw = h.request(http.MethodGet, "/api/v1/payments", nil,
		withHeader("Authorization", "Basic "+product.Key))
	requireStatus(t, resp, raw, http.StatusUnauthorized)

	// A revoked key stops working immediately.
	keys, err := h.container.Applications.ListAPIKeys(t.Context(), product.ID)
	if err != nil {
		t.Fatalf("list api keys: %v", err)
	}
	if _, err := h.container.Applications.RevokeAPIKey(t.Context(), product.ID, keys[0].ID); err != nil {
		t.Fatalf("revoke api key: %v", err)
	}
	resp, raw = h.request(http.MethodGet, "/api/v1/payments", nil, withKey(product.Key))
	requireStatus(t, resp, raw, http.StatusUnauthorized)
}
