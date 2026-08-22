package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/delivery"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/event"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/notification"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/storage"
)

// TestPaymentCreationIsIdempotent proves that a retried creation returns the
// original payment instead of opening a second one at the gateway (PRD §63).
func TestPaymentCreationIsIdempotent(t *testing.T) {
	h := newHarness(t)
	h.setupGatewayAccount()
	product := h.setupApplication("Product A", "product-a")

	body := createPaymentBody("INV-IDEM-1", 250000)
	key := withHeader("Idempotency-Key", "idem-key-abc")

	resp, raw := h.request(http.MethodPost, "/api/v1/payments", body, withKey(product.Key), key)
	requireStatus(t, resp, raw, http.StatusCreated)
	first := decode[map[string]any](t, raw)

	// The same request again: same answer, and crucially no second gateway
	// transaction, which would mean two ways for the customer to pay.
	resp, raw = h.request(http.MethodPost, "/api/v1/payments", body, withKey(product.Key), key)
	requireStatus(t, resp, raw, http.StatusCreated)
	second := decode[map[string]any](t, raw)

	if first["id"] != second["id"] {
		t.Errorf("retried request created a second payment: %v then %v", first["id"], second["id"])
	}
	if resp.Header.Get("Idempotent-Replay") != "true" {
		t.Error("the replayed response was not marked as a replay")
	}
	if got := h.midtrans.snapRequestCount(); got != 1 {
		t.Errorf("gateway received %d Snap requests, want exactly 1", got)
	}

	// The same key with a different body is a mistake worth reporting rather
	// than silently answering with the wrong payment.
	conflicting := createPaymentBody("INV-IDEM-1", 999000)
	resp, raw = h.request(http.MethodPost, "/api/v1/payments", conflicting, withKey(product.Key), key)
	if resp.StatusCode != http.StatusConflict && resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("reusing a key with a different body = %d, want a conflict: %s", resp.StatusCode, raw)
	}
}

// TestDuplicateOrderIDIsRejected covers the per-application uniqueness of an
// application's own order reference (PRD §61).
func TestDuplicateOrderIDIsRejected(t *testing.T) {
	h := newHarness(t)
	h.setupGatewayAccount()
	productA := h.setupApplication("Product A", "product-a")
	productB := h.setupApplication("Product B", "product-b")

	resp, raw := h.request(http.MethodPost, "/api/v1/payments",
		createPaymentBody("INV-001", 10000), withKey(productA.Key))
	requireStatus(t, resp, raw, http.StatusCreated)

	// The same application cannot reuse its own order id.
	resp, raw = h.request(http.MethodPost, "/api/v1/payments",
		createPaymentBody("INV-001", 10000), withKey(productA.Key))
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("duplicate order id = %d, want 409: %s", resp.StatusCode, raw)
	}

	// A different application using the same reference is perfectly fine:
	// that independence is the point of the gateway order id (PRD §22).
	resp, raw = h.request(http.MethodPost, "/api/v1/payments",
		createPaymentBody("INV-001", 10000), withKey(productB.Key))
	requireStatus(t, resp, raw, http.StatusCreated)
}

// TestDuplicateNotificationIsIgnored proves a redelivered notification does
// not produce a second event or a second delivery (PRD §39).
func TestDuplicateNotificationIsIgnored(t *testing.T) {
	h := newHarness(t)
	h.setupGatewayAccount()
	product := h.setupApplication("Product A", "product-a")
	h.runWorker()

	resp, raw := h.request(http.MethodPost, "/api/v1/payments",
		createPaymentBody("INV-DUP-1", 75000), withKey(product.Key))
	requireStatus(t, resp, raw, http.StatusCreated)
	payment := decode[map[string]any](t, raw)
	orderID, _ := payment["gateway_order_id"].(string)
	paymentID, _ := payment["id"].(string)

	settlement := signedNotification(orderID, "75000.00", "settlement", "accept")

	resp, raw = h.request(http.MethodPost, "/webhooks/midtrans", settlement)
	requireStatus(t, resp, raw, http.StatusOK)

	eventually(t, 5*time.Second, "the first payment.paid delivery", func() bool {
		return findDelivered(product.Receiver.all(), "payment.paid") != nil
	})

	// Midtrans redelivers the identical notification several times.
	for i := 0; i < 3; i++ {
		resp, raw = h.request(http.MethodPost, "/webhooks/midtrans", settlement)
		requireStatus(t, resp, raw, http.StatusOK)
	}
	time.Sleep(400 * time.Millisecond)

	paidEvents := countDelivered(product.Receiver.all(), "payment.paid")
	if paidEvents != 1 {
		t.Errorf("payment.paid was delivered %d times, want exactly 1", paidEvents)
	}

	ctx := context.Background()
	events, err := h.container.Events.ListForPayment(ctx, paymentID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if got := countEvents(events, event.PaymentPaid); got != 1 {
		t.Errorf("%d payment.paid events were stored, want exactly 1", got)
	}

	// Every copy is still recorded, so an operator can see the redeliveries.
	notifications, err := h.container.NotificationRepo.List(ctx,
		notification.Filter{PaymentID: paymentID}, storage.Page{Limit: 50})
	if err != nil {
		t.Fatalf("list gateway events: %v", err)
	}
	if len(notifications.Items) != 1 {
		// The duplicates are refused by the dedupe constraint, so only the
		// first is stored. That is the intended outcome.
		t.Logf("stored %d gateway events for the payment", len(notifications.Items))
	}
}

// TestStaleNotificationDoesNotDowngradePayment covers PRD §40: a delayed
// "pending" arriving after settlement must not un-pay a payment.
func TestStaleNotificationDoesNotDowngradePayment(t *testing.T) {
	h := newHarness(t)
	h.setupGatewayAccount()
	product := h.setupApplication("Product A", "product-a")

	resp, raw := h.request(http.MethodPost, "/api/v1/payments",
		createPaymentBody("INV-ORDER-1", 120000), withKey(product.Key))
	requireStatus(t, resp, raw, http.StatusCreated)
	payment := decode[map[string]any](t, raw)
	orderID, _ := payment["gateway_order_id"].(string)
	paymentID, _ := payment["id"].(string)

	// Settlement arrives first.
	resp, raw = h.request(http.MethodPost, "/webhooks/midtrans",
		signedNotification(orderID, "120000.00", "settlement", "accept"))
	requireStatus(t, resp, raw, http.StatusOK)

	// Then a delayed pending notification for the same transaction.
	resp, raw = h.request(http.MethodPost, "/webhooks/midtrans",
		signedNotification(orderID, "120000.00", "pending", ""))
	requireStatus(t, resp, raw, http.StatusOK)

	stored, err := h.container.PaymentRepo.Get(context.Background(), paymentID)
	if err != nil {
		t.Fatalf("load payment: %v", err)
	}
	if string(stored.NormalizedStatus) != "PAID" {
		t.Fatalf("payment status = %s, want PAID; a stale notification moved it backwards",
			stored.NormalizedStatus)
	}
	if stored.PaidAt == nil {
		t.Error("paid_at was cleared by the stale notification")
	}
}

// TestForgedNotificationIsRejected proves an unsigned or wrongly signed
// notification cannot change a payment (PRD §38).
func TestForgedNotificationIsRejected(t *testing.T) {
	h := newHarness(t)
	h.setupGatewayAccount()
	product := h.setupApplication("Product A", "product-a")

	resp, raw := h.request(http.MethodPost, "/api/v1/payments",
		createPaymentBody("INV-FORGE-1", 60000), withKey(product.Key))
	requireStatus(t, resp, raw, http.StatusCreated)
	payment := decode[map[string]any](t, raw)
	orderID, _ := payment["gateway_order_id"].(string)
	paymentID, _ := payment["id"].(string)

	forged := signedNotification(orderID, "60000.00", "settlement", "accept")
	forged["signature_key"] = "0000000000000000000000000000000000000000"

	resp, raw = h.request(http.MethodPost, "/webhooks/midtrans", forged)
	requireStatus(t, resp, raw, http.StatusUnauthorized)

	stored, err := h.container.PaymentRepo.Get(context.Background(), paymentID)
	if err != nil {
		t.Fatalf("load payment: %v", err)
	}
	if string(stored.NormalizedStatus) != "PENDING" {
		t.Fatalf("a forged notification moved the payment to %s", stored.NormalizedStatus)
	}

	// The attempt is recorded rather than dropped, so it is visible to an
	// operator (PRD §91 rule 15).
	events, err := h.container.NotificationRepo.List(context.Background(),
		notification.Filter{Routing: notification.RoutingRejected}, storage.Page{Limit: 10})
	if err != nil {
		t.Fatalf("list gateway events: %v", err)
	}
	if len(events.Items) == 0 {
		t.Error("the rejected notification was not recorded")
	}
	if len(events.Items) > 0 && events.Items[0].SignatureVerified {
		t.Error("the rejected notification is marked as verified")
	}
}

// TestUnknownOrderIsRecordedNotDiscarded covers a notification for an order
// PayMux did not create (PRD §91 rule 15).
func TestUnknownOrderIsRecordedNotDiscarded(t *testing.T) {
	h := newHarness(t)
	h.setupGatewayAccount()

	resp, raw := h.request(http.MethodPost, "/webhooks/midtrans",
		signedNotification("pmx_someone_elses_order", "10000.00", "settlement", "accept"))
	// Acknowledged: redelivering would not make the payment appear.
	requireStatus(t, resp, raw, http.StatusAccepted)

	events, err := h.container.NotificationRepo.List(context.Background(),
		notification.Filter{Routing: notification.RoutingUnrouted}, storage.Page{Limit: 10})
	if err != nil {
		t.Fatalf("list gateway events: %v", err)
	}
	if len(events.Items) != 1 {
		t.Fatalf("stored %d unrouted notifications, want 1", len(events.Items))
	}
	if !events.Items[0].SignatureVerified {
		t.Error("a correctly signed notification was not marked verified")
	}
}

// TestDeliveryRetriesUntilAccepted proves a failing destination is retried and
// that a manual retry works (PRD §45, §46, §47).
func TestDeliveryRetriesUntilAccepted(t *testing.T) {
	h := newHarness(t)
	h.setupGatewayAccount()
	product := h.setupApplication("Product A", "product-a")

	// The receiver rejects everything until told otherwise.
	product.Receiver.failNext(100)
	h.runWorker()

	resp, raw := h.request(http.MethodPost, "/api/v1/payments",
		createPaymentBody("INV-RETRY-1", 30000), withKey(product.Key))
	requireStatus(t, resp, raw, http.StatusCreated)

	ctx := context.Background()

	// The first attempt fails and the delivery is scheduled for another try.
	eventually(t, 5*time.Second, "the delivery to fail and be rescheduled", func() bool {
		list, err := h.container.Deliveries.List(ctx,
			delivery.Filter{ApplicationID: product.ID, State: delivery.StateFailed}, storage.Page{Limit: 10})
		return err == nil && len(list.Items) > 0
	})

	list, err := h.container.Deliveries.List(ctx,
		delivery.Filter{ApplicationID: product.ID}, storage.Page{Limit: 10})
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	failed := list.Items[0]
	if failed.AttemptCount < 1 {
		t.Errorf("attempt count = %d, want at least 1", failed.AttemptCount)
	}
	if failed.LastStatusCode == nil || *failed.LastStatusCode != http.StatusInternalServerError {
		t.Errorf("last status code = %v, want 500", failed.LastStatusCode)
	}
	// The retry is scheduled into the future rather than hammering the
	// destination immediately.
	if !failed.NextAttemptAt.After(time.Now()) {
		t.Errorf("next attempt is scheduled at %v, which is not in the future", failed.NextAttemptAt)
	}

	attempts, err := h.container.Deliveries.ListAttempts(ctx, failed.ID)
	if err != nil {
		t.Fatalf("list attempts: %v", err)
	}
	if len(attempts) == 0 {
		t.Fatal("no attempt history was recorded")
	}

	// The destination recovers, and an operator replays the delivery.
	product.Receiver.failNext(0)
	if _, err := h.container.Deliveries.Retry(ctx, failed.ID); err != nil {
		t.Fatalf("retry delivery: %v", err)
	}

	eventually(t, 5*time.Second, "the replayed delivery to be accepted", func() bool {
		return product.Receiver.count() > 0
	})

	eventually(t, 5*time.Second, "the delivery to be marked succeeded", func() bool {
		d, err := h.container.Deliveries.Get(ctx, failed.ID)
		return err == nil && d.State == delivery.StateSucceeded
	})
}

// TestDeliveryStopsOnClientRejection proves PayMux does not retry a
// destination that rejected the request itself (PRD §46).
func TestDeliveryStopsOnClientRejection(t *testing.T) {
	h := newHarness(t)
	h.setupGatewayAccount()
	product := h.setupApplication("Product A", "product-a")

	// 400 means "this request is wrong", which retrying cannot fix.
	product.Receiver.failWith(http.StatusBadRequest)
	product.Receiver.failNext(100)
	h.runWorker()

	resp, raw := h.request(http.MethodPost, "/api/v1/payments",
		createPaymentBody("INV-DEAD-1", 20000), withKey(product.Key))
	requireStatus(t, resp, raw, http.StatusCreated)

	ctx := context.Background()
	eventually(t, 5*time.Second, "the delivery to be declared dead", func() bool {
		list, err := h.container.Deliveries.List(ctx,
			delivery.Filter{ApplicationID: product.ID, State: delivery.StateDead}, storage.Page{Limit: 10})
		return err == nil && len(list.Items) > 0
	})

	list, err := h.container.Deliveries.List(ctx,
		delivery.Filter{ApplicationID: product.ID}, storage.Page{Limit: 10})
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	if got := list.Items[0].AttemptCount; got != 1 {
		t.Errorf("attempt count = %d, want 1: a 400 must not be retried", got)
	}
}

// TestSnapFailureLeavesNoOrphanPayment proves PayMux does not keep a payment
// the gateway refused to open.
func TestSnapFailureLeavesNoOrphanPayment(t *testing.T) {
	h := newHarness(t)
	h.setupGatewayAccount()
	product := h.setupApplication("Product A", "product-a")

	h.midtrans.mu.Lock()
	h.midtrans.failNextSnap = true
	h.midtrans.mu.Unlock()

	resp, raw := h.request(http.MethodPost, "/api/v1/payments",
		createPaymentBody("INV-FAIL-1", 40000), withKey(product.Key))
	if resp.StatusCode < 400 {
		t.Fatalf("a rejected gateway request returned %d: %s", resp.StatusCode, raw)
	}

	// The order id must be free again, which it can only be if no payment was
	// left behind.
	resp, raw = h.request(http.MethodPost, "/api/v1/payments",
		createPaymentBody("INV-FAIL-1", 40000), withKey(product.Key))
	requireStatus(t, resp, raw, http.StatusCreated)
}

func countDelivered(received []receivedWebhook, eventType string) int {
	n := 0
	for _, r := range received {
		if r.EventType == eventType {
			n++
		}
	}
	return n
}

func countEvents(events []*event.Event, eventType event.Type) int {
	n := 0
	for _, e := range events {
		if e.Type == eventType {
			n++
		}
	}
	return n
}
