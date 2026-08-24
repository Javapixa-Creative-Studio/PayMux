package midtrans

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/money"
)

// jakarta is the timezone Midtrans reports timestamps in.
//
// A fixed offset is used rather than a tzdata lookup: Western Indonesian Time
// is UTC+7 all year with no daylight saving, and a fixed zone keeps the
// binaries free of a timezone database dependency.
var jakarta = time.FixedZone("WIB", 7*60*60)

// midtransTimeLayout is the format Midtrans uses in transaction payloads.
const midtransTimeLayout = "2006-01-02 15:04:05"

// midtransTimeWithZoneLayout is what Midtrans expects for expiry start times.
const midtransTimeWithZoneLayout = "2006-01-02 15:04:05 -0700"

// parseTime converts a Midtrans timestamp into UTC.
//
// An unparseable or empty timestamp yields nil rather than an error: a
// timestamp PayMux cannot read is worth losing, but a payment notification is
// not worth rejecting over it.
func parseTime(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	for _, layout := range []string{midtransTimeWithZoneLayout, midtransTimeLayout, time.RFC3339} {
		if parsed, err := time.ParseInLocation(layout, value, jakarta); err == nil {
			utc := parsed.UTC()
			return &utc
		}
	}
	return nil
}

// formatTime renders a time the way Midtrans expects it in requests.
func formatTime(t time.Time) string {
	return t.In(jakarta).Format(midtransTimeWithZoneLayout)
}

// NormalizeStatus maps a Midtrans transaction status onto PayMux's normalized
// status (PRD §26).
//
// Card payments are the subtle case: a "capture" is only money in hand when
// fraud screening accepted it. A challenged capture is authorized but awaiting
// a manual decision, and a denied one has failed: collapsing all three into
// PAID would tell applications a payment succeeded when it has not.
func NormalizeStatus(transactionStatus, fraudStatus string) (gateway.Status, bool) {
	switch strings.ToLower(strings.TrimSpace(transactionStatus)) {
	case StatusPending:
		return gateway.StatusPending, true

	case StatusAuthorize:
		return gateway.StatusAuthorized, true

	case StatusCapture:
		switch strings.ToLower(strings.TrimSpace(fraudStatus)) {
		case FraudChallenge:
			return gateway.StatusAuthorized, true
		case FraudDeny:
			return gateway.StatusFailed, true
		default: // accept, or not reported for non-card channels
			return gateway.StatusPaid, true
		}

	case StatusSettlement:
		return gateway.StatusPaid, true

	case StatusDeny, StatusFailure:
		return gateway.StatusFailed, true

	case StatusCancel:
		return gateway.StatusCanceled, true

	case StatusExpire:
		return gateway.StatusExpired, true

	case StatusRefund:
		return gateway.StatusRefunded, true

	case StatusPartialRefund:
		return gateway.StatusPartiallyRefunded, true
	}

	// An unknown status is reported as unmapped so the caller can persist and
	// surface it instead of guessing (PRD §91 rule 15).
	return "", false
}

// normalizeSubscriptionStatus maps Midtrans's subscription status.
func normalizeSubscriptionStatus(status string) gateway.SubscriptionStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "active":
		return gateway.SubscriptionActive
	case "inactive":
		return gateway.SubscriptionInactive
	default:
		return gateway.SubscriptionCanceled
	}
}

// toTransaction converts a Core API transaction into the normalized form.
func toTransaction(resp *transactionResponse) *gateway.Transaction {
	currency := resp.Currency
	if currency == "" {
		currency = "IDR" // Midtrans omits the currency for IDR transactions
	}
	amount, err := money.Parse(resp.GrossAmount, currency)
	if err != nil {
		amount = 0
	}
	normalized, _ := NormalizeStatus(resp.TransactionStatus, resp.FraudStatus)

	return &gateway.Transaction{
		OrderID:         resp.OrderID,
		TransactionID:   resp.TransactionID,
		Status:          resp.TransactionStatus,
		FraudStatus:     resp.FraudStatus,
		Normalized:      normalized,
		PaymentType:     resp.PaymentType,
		GrossAmount:     amount,
		Currency:        money.NormalizeCurrency(currency),
		TransactionTime: parseTime(resp.TransactionTime),
		SettlementTime:  parseTime(resp.SettlementTime),
		ExpiresAt:       parseTime(resp.ExpiryTime),
		Raw:             toRaw(resp),
	}
}

// toRaw re-encodes a response as a generic map for storage.
//
// The signature key is dropped: it is derived from the server key, so keeping
// it in the stored payload would be storing a credential-derived secret
// alongside the data it protects (PRD §62).
func toRaw(v any) map[string]any {
	encoded, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		return map[string]any{}
	}
	delete(out, "signature_key")
	return out
}

// RedactPayload removes sensitive fields from a raw Midtrans payload before it
// is persisted or displayed.
func RedactPayload(payload map[string]any) map[string]any {
	if payload == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(payload))
	for key, value := range payload {
		switch strings.ToLower(key) {
		case "signature_key", "card_number", "card_cvv", "card_exp_month", "card_exp_year":
			continue
		default:
			out[key] = value
		}
	}
	return out
}
