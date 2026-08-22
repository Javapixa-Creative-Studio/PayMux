package midtrans

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anggapixa/paymux/internal/crypto"
	"github.com/anggapixa/paymux/internal/gateway"
)

const testServerKey = "SB-Mid-server-testkey123"

// newTestAdapter builds an adapter pointed at a stub Midtrans.
func newTestAdapter(t *testing.T, handler http.HandlerFunc) (*Adapter, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	acc := &gateway.Account{
		ID:          "gwa_test",
		Gateway:     Name,
		Environment: gateway.Sandbox,
		ServerKey:   crypto.Secret(testServerKey),
		Enabled:     true,
	}
	adapter, err := NewAdapter(acc, server.Client())
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	a := adapter.(*Adapter)
	a.client.SnapURL = server.URL
	a.client.CoreURL = server.URL
	return a, server
}

// ---------------------------------------------------------------------------
// Adapter construction and environment
// ---------------------------------------------------------------------------

func TestNewAdapterValidatesAccount(t *testing.T) {
	cases := []struct {
		name string
		acc  *gateway.Account
	}{
		{"nil account", nil},
		{"wrong gateway", &gateway.Account{Gateway: "stripe", Environment: gateway.Sandbox, ServerKey: "k"}},
		{"bad environment", &gateway.Account{Gateway: Name, Environment: "staging", ServerKey: "k"}},
		{"no server key", &gateway.Account{Gateway: Name, Environment: gateway.Sandbox}},
	}
	for _, tc := range cases {
		if _, err := NewAdapter(tc.acc, http.DefaultClient); err == nil {
			t.Errorf("NewAdapter(%s) = nil error, want failure", tc.name)
		}
	}
}

func TestClientUsesEnvironmentSpecificHosts(t *testing.T) {
	sandbox := NewClient(gateway.Sandbox, "k", nil)
	if sandbox.SnapURL != snapSandboxURL || sandbox.CoreURL != coreSandboxURL {
		t.Errorf("sandbox client points at %s / %s", sandbox.SnapURL, sandbox.CoreURL)
	}
	production := NewClient(gateway.Production, "k", nil)
	if production.SnapURL != snapProductionURL || production.CoreURL != coreProductionURL {
		t.Errorf("production client points at %s / %s", production.SnapURL, production.CoreURL)
	}
	// A sandbox request must never be able to reach production.
	if sandbox.SnapURL == production.SnapURL || sandbox.CoreURL == production.CoreURL {
		t.Fatal("sandbox and production share a host")
	}
}

// ---------------------------------------------------------------------------
// Signature verification (PRD §38)
// ---------------------------------------------------------------------------

func TestSignatureMatchesMidtransSpecification(t *testing.T) {
	in := SignatureInput{OrderID: "pmx_order_1", StatusCode: "200", GrossAmount: "150000.00"}

	// Midtrans defines the signature as
	// SHA512(order_id + status_code + gross_amount + server_key).
	expected := sha512.Sum512([]byte("pmx_order_1" + "200" + "150000.00" + testServerKey))
	want := hex.EncodeToString(expected[:])

	if got := Signature(in, testServerKey); got != want {
		t.Fatalf("Signature() = %s, want %s", got, want)
	}
}

func TestVerifySignature(t *testing.T) {
	in := SignatureInput{OrderID: "pmx_order_1", StatusCode: "200", GrossAmount: "150000.00"}
	valid := Signature(in, testServerKey)

	if !VerifySignature(in, valid, testServerKey) {
		t.Fatal("valid signature rejected")
	}
	if !VerifySignature(in, strings.ToUpper(valid), testServerKey) {
		t.Error("uppercase signature rejected; Midtrans casing should not matter")
	}
	if VerifySignature(in, "", testServerKey) {
		t.Error("empty signature accepted")
	}
	if VerifySignature(in, valid, "SB-Mid-server-otherkey") {
		t.Error("signature verified under the wrong server key")
	}

	// Any change to a signed field must invalidate the signature.
	tampered := []SignatureInput{
		{OrderID: "pmx_order_2", StatusCode: "200", GrossAmount: "150000.00"},
		{OrderID: "pmx_order_1", StatusCode: "201", GrossAmount: "150000.00"},
		{OrderID: "pmx_order_1", StatusCode: "200", GrossAmount: "1500000.00"},
	}
	for _, in := range tampered {
		if VerifySignature(in, valid, testServerKey) {
			t.Errorf("signature survived tampering: %+v", in)
		}
	}
}

func TestVerifyWebhookRejectsForgedNotification(t *testing.T) {
	a, _ := newTestAdapter(t, func(http.ResponseWriter, *http.Request) {})

	body := notificationJSON(t, "pmx_order_1", "150000.00", "settlement", "accept", "deadbeef")
	err := a.VerifyWebhook(context.Background(), gateway.WebhookRequest{Body: body})
	if !errors.Is(err, gateway.ErrInvalidSignature) {
		t.Fatalf("VerifyWebhook(forged) = %v, want ErrInvalidSignature", err)
	}

	signed := signedNotification(t, "pmx_order_1", "150000.00", "settlement", "accept")
	if err := a.VerifyWebhook(context.Background(), gateway.WebhookRequest{Body: signed}); err != nil {
		t.Fatalf("VerifyWebhook(valid) = %v", err)
	}
}

func TestVerifyWebhookRejectsMalformedBody(t *testing.T) {
	a, _ := newTestAdapter(t, func(http.ResponseWriter, *http.Request) {})

	if err := a.VerifyWebhook(context.Background(), gateway.WebhookRequest{Body: []byte("not json")}); err == nil {
		t.Error("malformed notification accepted")
	}
	if err := a.VerifyWebhook(context.Background(), gateway.WebhookRequest{Body: []byte(`{}`)}); err == nil {
		t.Error("notification without order_id accepted")
	}
}

// ---------------------------------------------------------------------------
// Status normalization (PRD §26)
// ---------------------------------------------------------------------------

func TestNormalizeStatus(t *testing.T) {
	cases := []struct {
		status, fraud string
		want          gateway.Status
		mapped        bool
	}{
		{"pending", "", gateway.StatusPending, true},
		{"settlement", "", gateway.StatusPaid, true},
		{"authorize", "", gateway.StatusAuthorized, true},
		{"deny", "", gateway.StatusFailed, true},
		{"cancel", "", gateway.StatusCanceled, true},
		{"expire", "", gateway.StatusExpired, true},
		{"failure", "", gateway.StatusFailed, true},
		{"refund", "", gateway.StatusRefunded, true},
		{"partial_refund", "", gateway.StatusPartiallyRefunded, true},

		// Card captures depend on fraud screening: a challenged capture is
		// not money in hand, and a denied one has failed.
		{"capture", "accept", gateway.StatusPaid, true},
		{"capture", "challenge", gateway.StatusAuthorized, true},
		{"capture", "deny", gateway.StatusFailed, true},
		{"capture", "", gateway.StatusPaid, true},

		{"SETTLEMENT", "", gateway.StatusPaid, true},
		{" settlement ", "", gateway.StatusPaid, true},

		// A status PayMux does not know must be reported, not guessed.
		{"quantum_superposition", "", "", false},
		{"", "", "", false},
	}
	for _, tc := range cases {
		got, mapped := NormalizeStatus(tc.status, tc.fraud)
		if got != tc.want || mapped != tc.mapped {
			t.Errorf("NormalizeStatus(%q, %q) = (%q, %v), want (%q, %v)",
				tc.status, tc.fraud, got, mapped, tc.want, tc.mapped)
		}
	}
}

// ---------------------------------------------------------------------------
// Snap payment creation (PRD §14)
// ---------------------------------------------------------------------------

func TestCreatePaymentSendsSnapRequest(t *testing.T) {
	var captured snapRequest
	var authHeader, path string

	a, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		path = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Errorf("request body is not valid JSON: %v", err)
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"token":        "snap-token-abc",
			"redirect_url": "https://app.sandbox.midtrans.com/snap/v4/redirection/snap-token-abc",
		})
	})

	expiresAt := time.Now().Add(2 * time.Hour)
	payment, err := a.CreatePayment(context.Background(), gateway.CreatePaymentRequest{
		OrderID:  "pmx_01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Amount:   150000,
		Currency: "IDR",
		Customer: gateway.Customer{FirstName: "John", LastName: "Doe", Email: "john@example.com"},
		Items: []gateway.Item{
			{SKU: "PROD-001", Name: "Example Product", Price: 150000, Quantity: 1},
		},
		EnabledPaymentMethods: []string{"gopay", "bca_va"},
		ExpiresAt:             &expiresAt,
		CallbackURL:           "https://product-b.example.com/return",
		CustomFields:          []string{"one", "two", "three", "dropped"},
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}

	if payment.Token != "snap-token-abc" {
		t.Errorf("Token = %q", payment.Token)
	}
	if payment.RedirectURL == "" {
		t.Error("RedirectURL is empty; redirect checkout would be impossible")
	}
	if payment.Normalized != gateway.StatusPending {
		t.Errorf("Normalized = %q, want PENDING", payment.Normalized)
	}

	if path != "/snap/v1/transactions" {
		t.Errorf("request path = %q", path)
	}
	if !strings.HasPrefix(authHeader, "Basic ") {
		t.Errorf("Authorization header = %q, want HTTP Basic", authHeader)
	}
	// IDR has no minor unit, so the gross amount must be a plain integer.
	if captured.TransactionDetails.GrossAmount != "150000" {
		t.Errorf("gross_amount = %q, want %q", captured.TransactionDetails.GrossAmount, "150000")
	}
	if captured.TransactionDetails.OrderID != "pmx_01ARZ3NDEKTSV4RRFFQ69G5FAV" {
		t.Errorf("order_id = %q", captured.TransactionDetails.OrderID)
	}
	if len(captured.ItemDetails) != 1 || captured.ItemDetails[0].Price != "150000" {
		t.Errorf("item_details = %+v", captured.ItemDetails)
	}
	if captured.CustomerDetails == nil || captured.CustomerDetails.Email != "john@example.com" {
		t.Errorf("customer_details = %+v", captured.CustomerDetails)
	}
	if len(captured.EnabledPayments) != 2 {
		t.Errorf("enabled_payments = %v", captured.EnabledPayments)
	}
	if captured.Expiry == nil || captured.Expiry.Duration < 119 {
		t.Errorf("expiry = %+v, want roughly 120 minutes", captured.Expiry)
	}
	if captured.Callbacks == nil || captured.Callbacks.Finish != "https://product-b.example.com/return" {
		t.Errorf("callbacks = %+v", captured.Callbacks)
	}
	if captured.CustomField1 != "one" || captured.CustomField3 != "three" {
		t.Errorf("custom fields = %q %q %q", captured.CustomField1, captured.CustomField2, captured.CustomField3)
	}
	// 3-D Secure must be on unless an application deliberately disables it.
	if captured.CreditCard == nil || !captured.CreditCard.Secure {
		t.Errorf("credit_card = %+v, want secure=true by default", captured.CreditCard)
	}
}

func TestCreatePaymentRejectsItemTotalMismatch(t *testing.T) {
	a, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("gateway was called despite an invalid request")
	})

	_, err := a.CreatePayment(context.Background(), gateway.CreatePaymentRequest{
		OrderID:  "pmx_1",
		Amount:   150000,
		Currency: "IDR",
		Items: []gateway.Item{
			{Name: "Product", Price: 100000, Quantity: 1},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "does not equal the payment amount") {
		t.Fatalf("CreatePayment = %v, want an item-total mismatch error", err)
	}
}

func TestCreatePaymentRejectsUnknownCurrency(t *testing.T) {
	a, _ := newTestAdapter(t, func(http.ResponseWriter, *http.Request) {
		t.Error("gateway was called despite an unknown currency")
	})
	_, err := a.CreatePayment(context.Background(), gateway.CreatePaymentRequest{
		OrderID: "pmx_1", Amount: 1000, Currency: "XYZ",
	})
	if err == nil {
		t.Fatal("CreatePayment accepted an unknown currency")
	}
}

func TestCreatePaymentAppliesGatewayOptions(t *testing.T) {
	var captured snapRequest
	a, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		writeJSON(w, http.StatusCreated, map[string]any{"token": "tok", "redirect_url": "https://example.com"})
	})

	options := json.RawMessage(`{
		"credit_card": {"secure": false, "bank": "bca"},
		"bank_transfer": {"bank": "bni"},
		"page_expiry_minutes": 30,
		"gopay": {"enable_callback": true, "callback_url": "https://product.example.com/gopay"}
	}`)
	if _, err := a.CreatePayment(context.Background(), gateway.CreatePaymentRequest{
		OrderID: "pmx_1", Amount: 1000, Currency: "IDR", Options: options,
	}); err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}

	if captured.CreditCard == nil || captured.CreditCard.Secure {
		t.Errorf("credit_card.secure was not overridden: %+v", captured.CreditCard)
	}
	if captured.BankTransfer == nil || captured.BankTransfer.Bank != "bni" {
		t.Errorf("bank_transfer = %+v", captured.BankTransfer)
	}
	if captured.PageExpiry == nil || captured.PageExpiry.Duration != 30 {
		t.Errorf("page_expiry = %+v", captured.PageExpiry)
	}
	if captured.Gopay == nil || !captured.Gopay.EnableCallback {
		t.Errorf("gopay = %+v", captured.Gopay)
	}
}

func TestParseOptionsRejectsUnknownAndInvalidValues(t *testing.T) {
	// An unvalidated blob must never reach Midtrans (PRD §18).
	if _, err := ParseOptions(json.RawMessage(`{"totally_made_up": true}`)); err == nil {
		t.Error("ParseOptions accepted an unknown field")
	}
	if _, err := ParseOptions(json.RawMessage(`{"bank_transfer": {"bank": "not_a_bank"}}`)); err == nil {
		t.Error("ParseOptions accepted an unrecognised bank")
	}
	if _, err := ParseOptions(json.RawMessage(`{"cstore": {"store": "seven_eleven"}}`)); err == nil {
		t.Error("ParseOptions accepted an unrecognised store")
	}
	if _, err := ParseOptions(json.RawMessage(`{"page_expiry_minutes": -5}`)); err == nil {
		t.Error("ParseOptions accepted a negative page expiry")
	}
	for _, raw := range []json.RawMessage{nil, json.RawMessage("null"), json.RawMessage("{}")} {
		if _, err := ParseOptions(raw); err != nil {
			t.Errorf("ParseOptions(%s) = %v, want nil", raw, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Transaction operations
// ---------------------------------------------------------------------------

func TestGetTransaction(t *testing.T) {
	a, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/pmx_order_1/status" {
			t.Errorf("path = %q", r.URL.Path)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status_code":        "200",
			"transaction_id":     "txn-123",
			"order_id":           "pmx_order_1",
			"gross_amount":       "150000.00",
			"payment_type":       "bank_transfer",
			"transaction_time":   "2026-08-20 10:00:00",
			"settlement_time":    "2026-08-20 10:05:00",
			"transaction_status": "settlement",
			"fraud_status":       "accept",
			"signature_key":      "should-not-be-stored",
		})
	})

	txn, err := a.GetTransaction(context.Background(), "pmx_order_1")
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if txn.Normalized != gateway.StatusPaid {
		t.Errorf("Normalized = %q, want PAID", txn.Normalized)
	}
	if txn.GrossAmount != 150000 {
		t.Errorf("GrossAmount = %d, want 150000", txn.GrossAmount)
	}
	if txn.Currency != "IDR" {
		t.Errorf("Currency = %q, want IDR (Midtrans omits it for rupiah)", txn.Currency)
	}
	// Midtrans reports times in WIB (UTC+7).
	if txn.TransactionTime == nil || txn.TransactionTime.UTC().Hour() != 3 {
		t.Errorf("TransactionTime = %v, want 03:00 UTC for 10:00 WIB", txn.TransactionTime)
	}
	if _, present := txn.Raw["signature_key"]; present {
		t.Error("signature_key was kept in the stored payload")
	}
}

func TestGetTransactionNotFound(t *testing.T) {
	a, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"status_code":    "404",
			"status_message": "Transaction doesn't exist.",
		})
	})
	_, err := a.GetTransaction(context.Background(), "pmx_missing")
	if !errors.Is(err, gateway.ErrTransactionNotFound) {
		t.Fatalf("GetTransaction = %v, want ErrTransactionNotFound", err)
	}
}

func TestCancelAndExpireTransaction(t *testing.T) {
	var paths []string
	a, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		status := "cancel"
		if strings.HasSuffix(r.URL.Path, "/expire") {
			status = "expire"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status_code":        "200",
			"order_id":           "pmx_order_1",
			"transaction_id":     "txn-1",
			"gross_amount":       "150000.00",
			"transaction_status": status,
		})
	})

	canceled, err := a.CancelTransaction(context.Background(), "pmx_order_1")
	if err != nil {
		t.Fatalf("CancelTransaction: %v", err)
	}
	if canceled.Normalized != gateway.StatusCanceled {
		t.Errorf("cancel produced %q", canceled.Normalized)
	}

	expired, err := a.ExpireTransaction(context.Background(), "pmx_order_1")
	if err != nil {
		t.Fatalf("ExpireTransaction: %v", err)
	}
	if expired.Normalized != gateway.StatusExpired {
		t.Errorf("expire produced %q", expired.Normalized)
	}

	want := []string{"POST /v2/pmx_order_1/cancel", "POST /v2/pmx_order_1/expire"}
	for i, path := range want {
		if paths[i] != path {
			t.Errorf("call %d = %q, want %q", i, paths[i], path)
		}
	}
}

func TestCancelCheckoutSessionExpiresTheTransaction(t *testing.T) {
	var called string
	a, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		called = r.URL.Path
		writeJSON(w, http.StatusOK, map[string]any{
			"status_code": "200", "order_id": "pmx_1", "gross_amount": "1000.00",
			"transaction_status": "expire",
		})
	})
	if err := a.CancelCheckoutSession(context.Background(), "pmx_1"); err != nil {
		t.Fatalf("CancelCheckoutSession: %v", err)
	}
	if called != "/v2/pmx_1/expire" {
		t.Errorf("CancelCheckoutSession called %q", called)
	}
}

// ---------------------------------------------------------------------------
// Refunds (PRD §31)
// ---------------------------------------------------------------------------

func TestRefundTransaction(t *testing.T) {
	var captured refundRequest
	a, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/pmx_order_1/refund" {
			t.Errorf("path = %q", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		writeJSON(w, http.StatusOK, map[string]any{
			"status_code":        "200",
			"order_id":           "pmx_order_1",
			"transaction_id":     "txn-1",
			"gross_amount":       "150000.00",
			"transaction_status": "partial_refund",
			"refund_amount":      "50000.00",
			"refunds": []map[string]any{
				{"refund_key": "rfd_key_1", "refund_amount": "50000.00", "reason": "Customer requested refund"},
			},
		})
	})

	refund, err := a.RefundTransaction(context.Background(), gateway.RefundRequest{
		OrderID:   "pmx_order_1",
		Amount:    50000,
		Reason:    "Customer requested refund",
		RefundKey: "rfd_key_1",
	})
	if err != nil {
		t.Fatalf("RefundTransaction: %v", err)
	}
	if captured.Amount != 50000 || captured.RefundKey != "rfd_key_1" {
		t.Errorf("refund request = %+v", captured)
	}
	if refund.Amount != 50000 {
		t.Errorf("refund amount = %d", refund.Amount)
	}
	if refund.RefundedAmount != 50000 {
		t.Errorf("cumulative refunded = %d", refund.RefundedAmount)
	}
	if refund.PaymentStatus != gateway.StatusPartiallyRefunded {
		t.Errorf("payment status after refund = %q", refund.PaymentStatus)
	}
}

func TestRefundNotSupportedIsTranslated(t *testing.T) {
	a, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"status_code":    "412",
			"status_message": "Refund is not allowed for this payment type",
		})
	})
	_, err := a.RefundTransaction(context.Background(), gateway.RefundRequest{OrderID: "pmx_1", Amount: 1000})
	if !errors.Is(err, gateway.ErrNotSupported) {
		t.Fatalf("RefundTransaction = %v, want ErrNotSupported", err)
	}
}

// ---------------------------------------------------------------------------
// Notifications (PRD §39)
// ---------------------------------------------------------------------------

func TestParseWebhook(t *testing.T) {
	a, _ := newTestAdapter(t, func(http.ResponseWriter, *http.Request) {})
	body := signedNotification(t, "pmx_order_1", "150000.00", "settlement", "accept")

	event, err := a.ParseWebhook(context.Background(), gateway.WebhookRequest{Body: body})
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if event.OrderID != "pmx_order_1" {
		t.Errorf("OrderID = %q", event.OrderID)
	}
	if event.Normalized != gateway.StatusPaid {
		t.Errorf("Normalized = %q, want PAID", event.Normalized)
	}
	if event.GrossAmount != 150000 {
		t.Errorf("GrossAmount = %d", event.GrossAmount)
	}
	if event.DedupeKey == "" {
		t.Error("DedupeKey is empty; duplicates could not be detected")
	}
}

func TestParseWebhookKeepsUnknownStatuses(t *testing.T) {
	a, _ := newTestAdapter(t, func(http.ResponseWriter, *http.Request) {})
	body := signedNotification(t, "pmx_order_1", "150000.00", "brand_new_status", "")

	event, err := a.ParseWebhook(context.Background(), gateway.WebhookRequest{Body: body})
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	// The gateway's own status is preserved even though PayMux cannot map it,
	// so the event can be recorded rather than dropped.
	if event.Status != "brand_new_status" {
		t.Errorf("Status = %q, want the gateway value preserved", event.Status)
	}
	if event.Normalized != "" {
		t.Errorf("Normalized = %q, want empty for an unmapped status", event.Normalized)
	}
}

func TestDedupeKeyIsStableAndDistinguishesStates(t *testing.T) {
	base := &transactionResponse{
		OrderID: "pmx_1", TransactionID: "txn-1",
		TransactionStatus: "settlement", FraudStatus: "accept", StatusCode: "200",
	}
	repeat := *base
	if DedupeKey(base) != DedupeKey(&repeat) {
		t.Fatal("the same notification produced different dedupe keys")
	}

	// A genuine state change must produce a different key so it is not
	// mistaken for a duplicate.
	changed := *base
	changed.TransactionStatus = "refund"
	if DedupeKey(base) == DedupeKey(&changed) {
		t.Error("a status change did not change the dedupe key")
	}
	fraudChanged := *base
	fraudChanged.FraudStatus = "challenge"
	if DedupeKey(base) == DedupeKey(&fraudChanged) {
		t.Error("a fraud status change did not change the dedupe key")
	}
}

// ---------------------------------------------------------------------------
// Error handling
// ---------------------------------------------------------------------------

func TestGatewayErrorsAreClassified(t *testing.T) {
	cases := []struct {
		name          string
		httpStatus    int
		body          map[string]any
		wantRetryable bool
	}{
		{"unauthorized", http.StatusUnauthorized,
			map[string]any{"status_code": "401", "status_message": "Access denied due to unauthorized transaction"}, false},
		{"validation", http.StatusBadRequest,
			map[string]any{"status_code": "400", "error_messages": []string{"transaction_details.gross_amount is required"}}, false},
		{"server error", http.StatusInternalServerError,
			map[string]any{"status_code": "500", "status_message": "Internal server error"}, true},
		{"rate limited", http.StatusTooManyRequests,
			map[string]any{"status_code": "429", "status_message": "Too many requests"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, tc.httpStatus, tc.body)
			})
			_, err := a.CreatePayment(context.Background(), gateway.CreatePaymentRequest{
				OrderID: "pmx_1", Amount: 1000, Currency: "IDR",
			})
			if err == nil {
				t.Fatal("expected an error")
			}
			var gwErr *gateway.Error
			if !errors.As(err, &gwErr) {
				t.Fatalf("error is not a *gateway.Error: %v", err)
			}
			if gwErr.Retryable != tc.wantRetryable {
				t.Errorf("Retryable = %v, want %v", gwErr.Retryable, tc.wantRetryable)
			}
			if strings.Contains(err.Error(), testServerKey) {
				t.Fatalf("the server key leaked into an error: %v", err)
			}
		})
	}
}

func TestFailureReportedInsideA200BodyIsAnError(t *testing.T) {
	// Midtrans sometimes reports a rejection with HTTP 200 and a 4xx
	// status_code in the body; treating that as success would create a
	// payment that does not exist at the gateway.
	a, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status_code":    "406",
			"status_message": "The request could not be completed due to a conflict",
		})
	})
	_, err := a.CreatePayment(context.Background(), gateway.CreatePaymentRequest{
		OrderID: "pmx_1", Amount: 1000, Currency: "IDR",
	})
	if err == nil {
		t.Fatal("a failure reported inside a 200 response was treated as success")
	}
}

func TestTransportFailureIsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	acc := &gateway.Account{
		ID: "gwa_1", Gateway: Name, Environment: gateway.Sandbox,
		ServerKey: crypto.Secret(testServerKey), Enabled: true,
	}
	adapter, err := NewAdapter(acc, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	a := adapter.(*Adapter)
	a.client.SnapURL = server.URL
	a.client.CoreURL = server.URL
	server.Close() // the gateway is now unreachable

	_, err = a.GetTransaction(context.Background(), "pmx_1")
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if !gateway.IsRetryable(err) {
		t.Errorf("transport failure is not retryable: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Capabilities
// ---------------------------------------------------------------------------

func TestCapabilities(t *testing.T) {
	a, _ := newTestAdapter(t, func(http.ResponseWriter, *http.Request) {})
	caps := a.Capabilities()
	if !caps.Checkout || !caps.Refund || !caps.Cancel || !caps.Expire {
		t.Errorf("capabilities = %+v", caps)
	}
	// Recurring billing needs merchant activation, so it is not assumed.
	if caps.Subscriptions {
		t.Error("subscriptions reported available without account activation")
	}

	a.account.Capabilities.Subscriptions = true
	if !a.Capabilities().Subscriptions {
		t.Error("activated subscriptions were not reported")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func notificationJSON(t *testing.T, orderID, grossAmount, status, fraud, signature string) []byte {
	t.Helper()
	payload := map[string]any{
		"status_code":        "200",
		"order_id":           orderID,
		"transaction_id":     "txn-" + orderID,
		"gross_amount":       grossAmount,
		"payment_type":       "bank_transfer",
		"transaction_time":   "2026-08-20 10:00:00",
		"transaction_status": status,
		"fraud_status":       fraud,
		"signature_key":      signature,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	return body
}

func signedNotification(t *testing.T, orderID, grossAmount, status, fraud string) []byte {
	t.Helper()
	signature := Signature(SignatureInput{
		OrderID: orderID, StatusCode: "200", GrossAmount: grossAmount,
	}, testServerKey)
	return notificationJSON(t, orderID, grossAmount, status, fraud, signature)
}
