package integration

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/crypto"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway/midtrans"
)

// These tests talk to the real Midtrans sandbox (PRD §86).
//
// They exist because a stub can only prove PayMux is self-consistent. Whether
// PayMux's understanding of Midtrans is *correct*: the Snap payload it sends,
// the signature scheme, the status vocabulary: can only be settled by the
// real service.
//
// They are skipped unless sandbox credentials are present, so the ordinary
// suite needs no external connectivity, and they never run against production:
// the account is pinned to the sandbox environment below.

// sandboxAdapter builds an adapter against the real Midtrans sandbox, or skips.
func sandboxAdapter(t *testing.T) *midtrans.Adapter {
	t.Helper()

	serverKey := os.Getenv("MIDTRANS_SANDBOX_SERVER_KEY")
	if serverKey == "" {
		t.Skip("set MIDTRANS_SANDBOX_SERVER_KEY to run the Midtrans sandbox tests")
	}

	account := &gateway.Account{
		ID:      "gwa_sandbox",
		Gateway: midtrans.Name,
		// Pinned, never read from the environment: a sandbox test must not be
		// able to reach production by misconfiguration.
		Environment: gateway.Sandbox,
		MerchantID:  os.Getenv("MIDTRANS_SANDBOX_MERCHANT_ID"),
		ClientKey:   os.Getenv("MIDTRANS_SANDBOX_CLIENT_KEY"),
		ServerKey:   crypto.Secret(serverKey),
		Enabled:     true,
	}

	adapter, err := midtrans.NewAdapter(account, gateway.NewHTTPClient(30*time.Second))
	if err != nil {
		t.Fatalf("build sandbox adapter: %v", err)
	}
	return adapter.(*midtrans.Adapter)
}

// sandboxOrderID keeps each run's orders distinct; Midtrans rejects a reused
// order id, and a shared sandbox account may be in use by someone else.
func sandboxOrderID(t *testing.T) string {
	t.Helper()
	return "pmx-ci-" + time.Now().UTC().Format("20060102T150405") + "-" + t.Name()
}

func TestSandboxCreatesASnapTransaction(t *testing.T) {
	adapter := sandboxAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	orderID := sandboxOrderID(t)
	payment, err := adapter.CreatePayment(ctx, gateway.CreatePaymentRequest{
		OrderID:  orderID,
		Amount:   150000,
		Currency: "IDR",
		Customer: gateway.Customer{
			FirstName: "PayMux",
			LastName:  "CI",
			Email:     "ci@paymux.test",
		},
		Items: []gateway.Item{
			{SKU: "CI-1", Name: "Continuous integration item", Price: 150000, Quantity: 1},
		},
	})
	if err != nil {
		t.Fatalf("CreatePayment against the sandbox: %v", err)
	}

	if payment.Token == "" {
		t.Error("the sandbox returned no Snap token")
	}
	if payment.RedirectURL == "" {
		t.Error("the sandbox returned no redirect URL")
	}
	if payment.Normalized != gateway.StatusPending {
		t.Errorf("a new payment normalized to %q, want PENDING", payment.Normalized)
	}
}

func TestSandboxReportsTransactionStatus(t *testing.T) {
	adapter := sandboxAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	orderID := sandboxOrderID(t)
	if _, err := adapter.CreatePayment(ctx, gateway.CreatePaymentRequest{
		OrderID: orderID, Amount: 25000, Currency: "IDR",
	}); err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}

	// A Snap transaction that has not been paid has no Core API transaction
	// yet, so "not found" is a legitimate answer here: what matters is that
	// PayMux recognises it as such rather than treating it as an outage.
	txn, err := adapter.GetTransaction(ctx, orderID)
	switch {
	case err == nil:
		if txn.OrderID != orderID {
			t.Errorf("status returned order %q, want %q", txn.OrderID, orderID)
		}
	case isNotFound(err):
		t.Logf("no transaction yet for %s, which is expected before payment", orderID)
	default:
		t.Fatalf("GetTransaction: %v", err)
	}
}

func TestSandboxExpiresATransaction(t *testing.T) {
	adapter := sandboxAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	orderID := sandboxOrderID(t)
	if _, err := adapter.CreatePayment(ctx, gateway.CreatePaymentRequest{
		OrderID: orderID, Amount: 25000, Currency: "IDR",
	}); err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}

	// Expiring an unpaid Snap transaction is how PayMux retires a checkout
	// session, so it is worth confirming the endpoint behaves as assumed.
	txn, err := adapter.ExpireTransaction(ctx, orderID)
	switch {
	case err == nil:
		if txn.Normalized != gateway.StatusExpired {
			t.Errorf("expire produced %q, want EXPIRED", txn.Normalized)
		}
	case isNotFound(err):
		t.Logf("nothing to expire for %s before payment has started", orderID)
	default:
		t.Fatalf("ExpireTransaction: %v", err)
	}
}

func TestSandboxRejectsAnUnknownTransaction(t *testing.T) {
	adapter := sandboxAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Confirms PayMux translates Midtrans's "doesn't exist" into the domain's
	// own sentinel rather than surfacing it as a gateway failure.
	if _, err := adapter.GetTransaction(ctx, "pmx-ci-definitely-not-a-real-order"); !isNotFound(err) {
		t.Fatalf("GetTransaction for an unknown order = %v, want ErrTransactionNotFound", err)
	}
}

func isNotFound(err error) bool {
	return errors.Is(err, gateway.ErrTransactionNotFound)
}
