// Package payment owns PayMux's payment lifecycle: creation through a
// gateway, state transitions driven by notifications, and the operations
// applications perform on a payment.
package payment

import (
	"time"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
)

// Payment is one payment PayMux owns.
//
// Both order identifiers are kept: the application's own reference, which is
// only unique within that application, and PayMux's gateway order id, which is
// globally unique. That pairing is what lets two products both use "INV-001"
// without colliding at the gateway (PRD §22).
type Payment struct {
	ID               string
	ApplicationID    string
	GatewayAccountID string
	Gateway          string

	ApplicationOrderID   string
	GatewayOrderID       string
	GatewayTransactionID string

	Amount   int64
	Currency string

	NormalizedStatus gateway.Status
	GatewayStatus    string
	FraudStatus      string

	PaymentMethod string
	PaymentType   string

	SnapToken       string
	SnapRedirectURL string

	RefundedAmount int64

	Metadata       map[string]any
	GatewayOptions map[string]any
	GatewayData    map[string]any

	Customer *Customer
	Items    []Item

	ExpiresAt    *time.Time
	PaidAt       *time.Time
	CanceledAt   *time.Time
	ExpiredAt    *time.Time
	RefundedAt   *time.Time
	LastSyncedAt *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Customer is the payer attached to a payment.
type Customer struct {
	FirstName string
	LastName  string
	Email     string
	Phone     string
	Billing   *Address
	Shipping  *Address
}

// Address is a postal address.
type Address struct {
	FirstName   string `json:"first_name,omitempty"`
	LastName    string `json:"last_name,omitempty"`
	Phone       string `json:"phone,omitempty"`
	Address     string `json:"address,omitempty"`
	City        string `json:"city,omitempty"`
	PostalCode  string `json:"postal_code,omitempty"`
	CountryCode string `json:"country_code,omitempty"`
}

// Item is a line item on a payment.
type Item struct {
	ID       string
	SKU      string
	Name     string
	Price    int64
	Quantity int
	Category string
	Merchant string
	URL      string
	Position int
}

// Total is the line's contribution to the payment amount.
func (i Item) Total() int64 { return i.Price * int64(i.Quantity) }

// RefundableAmount is how much of the payment may still be refunded.
func (p *Payment) RefundableAmount() int64 {
	if p == nil {
		return 0
	}
	remaining := p.Amount - p.RefundedAmount
	if remaining < 0 {
		return 0
	}
	return remaining
}

// Refundable reports whether a refund may be attempted against this payment.
func (p *Payment) Refundable() bool {
	return p != nil && p.NormalizedStatus.Settled() && p.RefundableAmount() > 0
}

// Cancelable reports whether the payment may still be cancelled. Once money
// has settled the operation is a refund, not a cancellation.
func (p *Payment) Cancelable() bool {
	if p == nil {
		return false
	}
	switch p.NormalizedStatus {
	case gateway.StatusPending, gateway.StatusAuthorized:
		return true
	default:
		return false
	}
}

// Refund is one refund against a payment.
type Refund struct {
	ID              string
	PaymentID       string
	ApplicationID   string
	GatewayRefundID string
	RefundKey       string
	Amount          int64
	Currency        string
	Reason          string
	Status          gateway.RefundStatus
	GatewayStatus   string
	FailureReason   string
	RawResponse     map[string]any
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
