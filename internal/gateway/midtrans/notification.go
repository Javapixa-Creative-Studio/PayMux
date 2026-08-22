package midtrans

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/money"
)

// VerifyWebhook authenticates a Midtrans notification (PRD §38).
//
// Verification uses Midtrans's documented signature: the digest covers the
// order id, status code and gross amount, keyed by the merchant's server key.
// A notification that fails is rejected outright — PayMux never updates a
// payment from an unauthenticated message.
func (a *Adapter) VerifyWebhook(_ context.Context, req gateway.WebhookRequest) error {
	var payload transactionResponse
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		return fmt.Errorf("midtrans: notification body is not valid JSON: %w", err)
	}
	if payload.OrderID == "" {
		return fmt.Errorf("midtrans: notification is missing order_id")
	}
	ok := VerifySignature(SignatureInput{
		OrderID:     payload.OrderID,
		StatusCode:  payload.StatusCode,
		GrossAmount: payload.GrossAmount,
	}, payload.SignatureKey, a.account.ServerKey)
	if !ok {
		return gateway.ErrInvalidSignature
	}
	return nil
}

// ParseWebhook converts a verified notification into a normalized event.
//
// Callers must verify first: parsing does not authenticate, and this method
// deliberately does not re-check the signature so the two concerns stay
// separable in tests and in the notification pipeline.
func (a *Adapter) ParseWebhook(_ context.Context, req gateway.WebhookRequest) (*gateway.Event, error) {
	var payload transactionResponse
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		return nil, fmt.Errorf("midtrans: notification body is not valid JSON: %w", err)
	}
	if payload.OrderID == "" {
		return nil, fmt.Errorf("midtrans: notification is missing order_id")
	}

	currency := payload.Currency
	if currency == "" {
		currency = "IDR"
	}
	amount, err := money.Parse(payload.GrossAmount, currency)
	if err != nil {
		return nil, fmt.Errorf("midtrans: notification gross_amount is not usable: %w", err)
	}

	// An unmapped status still produces an event: PayMux records it, reports
	// it as unrouted and leaves the payment untouched rather than discarding
	// a message from the gateway (PRD §91 rule 15).
	normalized, _ := NormalizeStatus(payload.TransactionStatus, payload.FraudStatus)

	return &gateway.Event{
		OrderID:         payload.OrderID,
		TransactionID:   payload.TransactionID,
		Status:          payload.TransactionStatus,
		FraudStatus:     payload.FraudStatus,
		Normalized:      normalized,
		PaymentType:     payload.PaymentType,
		GrossAmount:     amount,
		Currency:        money.NormalizeCurrency(currency),
		TransactionTime: parseTime(payload.TransactionTime),
		SettlementTime:  parseTime(payload.SettlementTime),
		DedupeKey:       DedupeKey(&payload),
		Raw:             toRaw(&payload),
	}, nil
}

// DedupeKey derives a stable identity for one reported transaction state
// (PRD §39).
//
// Midtrans redelivers notifications, and a redelivery of the same state is
// byte-identical in the fields below. Including the status and fraud status —
// not just the transaction id — is what lets a genuine state change through
// while collapsing repeats of a state PayMux already handled.
func DedupeKey(payload *transactionResponse) string {
	identity := fmt.Sprintf("%s|%s|%s|%s|%s",
		payload.OrderID,
		payload.TransactionID,
		payload.TransactionStatus,
		payload.FraudStatus,
		payload.StatusCode,
	)
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])
}
