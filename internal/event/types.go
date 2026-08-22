// Package event owns PayMux's normalized event model: the stable vocabulary
// applications subscribe to, and the records PayMux publishes to them.
package event

import (
	"time"

	"github.com/anggapixa/paymux/internal/gateway"
)

// Type is a PayMux event name (PRD §42).
//
// These names are part of the public contract: applications match on them, so
// a name is added, never repurposed. They deliberately describe PayMux's
// normalized lifecycle rather than any gateway's status vocabulary, so a
// receiving application need not learn Midtrans's terms — the gateway's own
// values remain available inside the payload.
type Type string

const (
	PaymentCreated           Type = "payment.created"
	PaymentPending           Type = "payment.pending"
	PaymentAuthorized        Type = "payment.authorized"
	PaymentPaid              Type = "payment.paid"
	PaymentFailed            Type = "payment.failed"
	PaymentCanceled          Type = "payment.canceled"
	PaymentExpired           Type = "payment.expired"
	PaymentRefunded          Type = "payment.refunded"
	PaymentPartiallyRefunded Type = "payment.partially_refunded"

	RefundCreated   Type = "refund.created"
	RefundCompleted Type = "refund.completed"
	RefundFailed    Type = "refund.failed"

	SubscriptionCreated  Type = "subscription.created"
	SubscriptionUpdated  Type = "subscription.updated"
	SubscriptionEnabled  Type = "subscription.enabled"
	SubscriptionDisabled Type = "subscription.disabled"
	SubscriptionCanceled Type = "subscription.canceled"
)

// String implements fmt.Stringer.
func (t Type) String() string { return string(t) }

// allTypes is every event PayMux can emit, used to validate subscriptions.
var allTypes = map[Type]bool{
	PaymentCreated: true, PaymentPending: true, PaymentAuthorized: true,
	PaymentPaid: true, PaymentFailed: true, PaymentCanceled: true,
	PaymentExpired: true, PaymentRefunded: true, PaymentPartiallyRefunded: true,
	RefundCreated: true, RefundCompleted: true, RefundFailed: true,
	SubscriptionCreated: true, SubscriptionUpdated: true, SubscriptionEnabled: true,
	SubscriptionDisabled: true, SubscriptionCanceled: true,
}

// Valid reports whether t is a known event type.
func (t Type) Valid() bool { return allTypes[t] }

// AllTypes lists every event type PayMux emits.
func AllTypes() []Type {
	return []Type{
		PaymentCreated, PaymentPending, PaymentAuthorized, PaymentPaid,
		PaymentFailed, PaymentCanceled, PaymentExpired,
		PaymentRefunded, PaymentPartiallyRefunded,
		RefundCreated, RefundCompleted, RefundFailed,
		SubscriptionCreated, SubscriptionUpdated, SubscriptionEnabled,
		SubscriptionDisabled, SubscriptionCanceled,
	}
}

// TypeForStatus maps a normalized payment status to the event announcing it.
//
// PENDING has no event of its own on creation — payment.created already says
// a payment exists — but a later transition back into a pending-like state
// from a gateway still reports payment.pending.
func TypeForStatus(status gateway.Status) (Type, bool) {
	switch status {
	case gateway.StatusPending:
		return PaymentPending, true
	case gateway.StatusAuthorized:
		return PaymentAuthorized, true
	case gateway.StatusPaid:
		return PaymentPaid, true
	case gateway.StatusFailed:
		return PaymentFailed, true
	case gateway.StatusCanceled:
		return PaymentCanceled, true
	case gateway.StatusExpired:
		return PaymentExpired, true
	case gateway.StatusRefunded:
		return PaymentRefunded, true
	case gateway.StatusPartiallyRefunded:
		return PaymentPartiallyRefunded, true
	default:
		return "", false
	}
}

// Event is a normalized PayMux event (PRD §41).
type Event struct {
	ID             string
	Sequence       int64
	ApplicationID  string
	Type           Type
	Gateway        string
	PaymentID      string
	RefundID       string
	SubscriptionID string
	GatewayEventID string
	// DedupeKey identifies the occurrence this event reports. Publishing an
	// event whose key already exists is a no-op (PRD §39).
	DedupeKey string
	Payload   Payload
	CreatedAt time.Time
}

// PaymentDedupeKey builds the key for a payment state event.
//
// The discriminator distinguishes occurrences that share a payment and a type
// but are genuinely different — successive partial refunds each move the
// refunded total, and each deserves its own event.
func PaymentDedupeKey(paymentID string, t Type, discriminator string) string {
	key := "payment:" + paymentID + ":" + string(t)
	if discriminator != "" {
		key += ":" + discriminator
	}
	return key
}

// RefundDedupeKey builds the key for a refund event.
func RefundDedupeKey(refundID string, t Type) string {
	return "refund:" + refundID + ":" + string(t)
}

// SubscriptionDedupeKey builds the key for a subscription event.
func SubscriptionDedupeKey(subscriptionID string, t Type, discriminator string) string {
	key := "subscription:" + subscriptionID + ":" + string(t)
	if discriminator != "" {
		key += ":" + discriminator
	}
	return key
}

// Payload is the JSON body delivered to an application.
//
// GatewayData carries the gateway's own view untouched, so an application that
// wants Midtrans's raw fields is never forced to go without them, while one
// that only wants the normalized shape can ignore it (PRD §42).
type Payload struct {
	ID                   string         `json:"id"`
	Type                 Type           `json:"type"`
	Gateway              string         `json:"gateway"`
	ApplicationID        string         `json:"application_id"`
	PaymentID            string         `json:"payment_id,omitempty"`
	RefundID             string         `json:"refund_id,omitempty"`
	SubscriptionID       string         `json:"subscription_id,omitempty"`
	ApplicationOrderID   string         `json:"application_order_id,omitempty"`
	GatewayOrderID       string         `json:"gateway_order_id,omitempty"`
	GatewayTransactionID string         `json:"gateway_transaction_id,omitempty"`
	Status               gateway.Status `json:"status,omitempty"`
	GatewayStatus        string         `json:"gateway_status,omitempty"`
	FraudStatus          string         `json:"fraud_status,omitempty"`
	PaymentType          string         `json:"payment_type,omitempty"`
	Amount               int64          `json:"amount,omitempty"`
	RefundedAmount       int64          `json:"refunded_amount,omitempty"`
	Currency             string         `json:"currency,omitempty"`
	Metadata             map[string]any `json:"metadata,omitempty"`
	CreatedAt            time.Time      `json:"created_at"`
	GatewayData          map[string]any `json:"gateway_data,omitempty"`
}
