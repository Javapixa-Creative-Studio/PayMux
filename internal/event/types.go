// Package event owns PayMux's normalized event model: the stable vocabulary
// applications subscribe to, and the records PayMux publishes to them.
package event

import (
	"time"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
)

// Type is a PayMux event name (PRD §42).
//
// These names are part of the public contract: applications match on them, so
// a name is added, never repurposed. They deliberately describe PayMux's
// normalized lifecycle rather than any gateway's status vocabulary, so a
// receiving application need not learn Midtrans's terms: the gateway's own
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

	// Payout events. An application that disburses needs to hear about a
	// payout at least as urgently as about a payment: payout.failed is the one
	// telling them somebody did not get paid.
	PayoutRequested Type = "payout.requested"
	PayoutApproved  Type = "payout.approved"
	PayoutRejected  Type = "payout.rejected"
	PayoutSubmitted Type = "payout.submitted"
	PayoutCompleted Type = "payout.completed"
	PayoutFailed    Type = "payout.failed"
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
	PayoutRequested: true, PayoutApproved: true, PayoutRejected: true,
	PayoutSubmitted: true, PayoutCompleted: true, PayoutFailed: true,
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
		PayoutRequested, PayoutApproved, PayoutRejected,
		PayoutSubmitted, PayoutCompleted, PayoutFailed,
	}
}

// TypeForPayoutStatus maps a normalized payout status to the event announcing
// it.
//
// UNRESOLVED has no event on purpose. It says PayMux does not know what
// happened, which is a statement about PayMux rather than about the payout,
// and telling an application "we are unsure" invites them to act on it. They
// hear from us when there is an answer.
func TypeForPayoutStatus(status gateway.PayoutStatus) (Type, bool) {
	switch status {
	case gateway.PayoutApproved:
		return PayoutApproved, true
	case gateway.PayoutRejected:
		return PayoutRejected, true
	case gateway.PayoutSubmitted:
		return PayoutSubmitted, true
	case gateway.PayoutCompleted:
		return PayoutCompleted, true
	case gateway.PayoutFailed:
		return PayoutFailed, true
	default:
		return "", false
	}
}

// PayoutDedupeKey builds the key for a payout state event.
func PayoutDedupeKey(payoutID string, t Type) string {
	return "payout:" + payoutID + ":" + string(t)
}

// TypeForStatus maps a normalized payment status to the event announcing it.
//
// PENDING has no event of its own on creation: payment.created already says
// a payment exists, but a later transition back into a pending-like state
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
	PayoutID       string
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
// but are genuinely different: successive partial refunds each move the
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
	ID                   string `json:"id"`
	Type                 Type   `json:"type"`
	Gateway              string `json:"gateway"`
	ApplicationID        string `json:"application_id"`
	PaymentID            string `json:"payment_id,omitempty"`
	RefundID             string `json:"refund_id,omitempty"`
	SubscriptionID       string `json:"subscription_id,omitempty"`
	PayoutID             string `json:"payout_id,omitempty"`
	ApplicationOrderID   string `json:"application_order_id,omitempty"`
	ApplicationPayoutID  string `json:"application_payout_id,omitempty"`
	GatewayOrderID       string `json:"gateway_order_id,omitempty"`
	GatewayTransactionID string `json:"gateway_transaction_id,omitempty"`
	ReferenceNo          string `json:"reference_no,omitempty"`
	// Status is the subject's normalized status as a plain string. Payments
	// and payouts have different vocabularies and this is a wire type, so it
	// carries the word rather than either domain's enum.
	Status             string `json:"status,omitempty"`
	GatewayStatus      string `json:"gateway_status,omitempty"`
	FraudStatus        string `json:"fraud_status,omitempty"`
	PaymentType        string `json:"payment_type,omitempty"`
	Amount             int64  `json:"amount,omitempty"`
	RefundedAmount     int64  `json:"refunded_amount,omitempty"`
	Currency           string `json:"currency,omitempty"`
	BeneficiaryName    string `json:"beneficiary_name,omitempty"`
	BeneficiaryAccount string `json:"beneficiary_account,omitempty"`
	BeneficiaryBank    string `json:"beneficiary_bank,omitempty"`
	Notes              string `json:"notes,omitempty"`
	// A failure is the one thing a receiving application must be able to act
	// on, so the reason travels with the event rather than living only in
	// PayMux's dashboard.
	FailureCode   string         `json:"failure_code,omitempty"`
	FailureReason string         `json:"failure_reason,omitempty"`
	RejectReason  string         `json:"reject_reason,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	CompletedAt   *time.Time     `json:"completed_at,omitempty"`
	GatewayData   map[string]any `json:"gateway_data,omitempty"`
}
