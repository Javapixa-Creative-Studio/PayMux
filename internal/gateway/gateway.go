package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// Environment distinguishes a gateway's sandbox from its production system.
// It is explicit everywhere so a sandbox request can never be sent to
// production by accident (PRD §13).
type Environment string

const (
	Sandbox    Environment = "sandbox"
	Production Environment = "production"
)

// Valid reports whether e is a known environment.
func (e Environment) Valid() bool { return e == Sandbox || e == Production }

// Gateway is the contract every payment gateway adapter implements.
//
// Capabilities that not every gateway supports: refunds, subscriptions,
// hosted-checkout sessions: are declared through the narrower interfaces
// below rather than forcing meaningless methods onto every adapter (PRD §11).
type Gateway interface {
	// Name is the adapter's stable identifier, e.g. "midtrans".
	Name() string

	// CreatePayment opens a payment at the gateway.
	CreatePayment(ctx context.Context, req CreatePaymentRequest) (*Payment, error)

	// GetTransaction fetches the gateway's authoritative transaction state.
	GetTransaction(ctx context.Context, orderID string) (*Transaction, error)

	// CancelTransaction cancels a transaction that has not settled.
	CancelTransaction(ctx context.Context, orderID string) (*Transaction, error)

	// ExpireTransaction expires a pending transaction.
	ExpireTransaction(ctx context.Context, orderID string) (*Transaction, error)

	// VerifyWebhook authenticates an inbound notification.
	VerifyWebhook(ctx context.Context, req WebhookRequest) error

	// ParseWebhook converts a verified notification into a normalized event.
	ParseWebhook(ctx context.Context, req WebhookRequest) (*Event, error)
}

// RefundGateway is implemented by gateways that support refunds (PRD §31).
type RefundGateway interface {
	RefundTransaction(ctx context.Context, req RefundRequest) (*Refund, error)
}

// CheckoutGateway is implemented by gateways with a hosted checkout session
// that can be cancelled independently of the transaction (PRD §30).
type CheckoutGateway interface {
	CancelCheckoutSession(ctx context.Context, token string) error

	// CheckoutScriptURL is the browser script that renders the gateway's
	// hosted checkout in place, for applications that would rather show a
	// dialog than send the customer away.
	//
	// PayMux reports it so an application never has to hardcode a gateway's
	// hostnames or know which environment it is pointed at. That is the whole
	// arrangement: the merchant configures the gateway once, here.
	CheckoutScriptURL() string
}

// SubscriptionGateway is implemented by gateways supporting recurring
// subscriptions (PRD §33).
type SubscriptionGateway interface {
	CreateSubscription(ctx context.Context, req CreateSubscriptionRequest) (*Subscription, error)
	GetSubscription(ctx context.Context, id string) (*Subscription, error)
	UpdateSubscription(ctx context.Context, id string, req UpdateSubscriptionRequest) (*Subscription, error)
	EnableSubscription(ctx context.Context, id string) error
	DisableSubscription(ctx context.Context, id string) error
	CancelSubscription(ctx context.Context, id string) error
}

// Capabilities describes what a configured gateway account can actually do.
//
// Availability depends on merchant configuration, not only on what the
// adapter implements, so this is reported per account and never hard-coded
// (PRD §85).
type Capabilities struct {
	Checkout      bool `json:"checkout"`
	Refund        bool `json:"refund"`
	PartialRefund bool `json:"partial_refund"`
	Subscriptions bool `json:"subscriptions"`
	Cancel        bool `json:"cancel"`
	Expire        bool `json:"expire"`
	// Disbursement stays false until an account actually holds disbursement
	// credentials. An adapter implementing DisbursementGateway is necessary
	// but not sufficient: Midtrans gates payouts behind separate approval and
	// separate keys, so what the code can do and what the account may do are
	// different questions.
	Disbursement bool `json:"disbursement"`
}

// CapabilityReporter is implemented by adapters that can describe themselves.
type CapabilityReporter interface {
	Capabilities() Capabilities
}

// ---------------------------------------------------------------------------
// Requests and responses
// ---------------------------------------------------------------------------

// Customer is the payer's details as PayMux passes them to a gateway.
type Customer struct {
	FirstName string   `json:"first_name,omitempty"`
	LastName  string   `json:"last_name,omitempty"`
	Email     string   `json:"email,omitempty"`
	Phone     string   `json:"phone,omitempty"`
	Billing   *Address `json:"billing_address,omitempty"`
	Shipping  *Address `json:"shipping_address,omitempty"`
}

// Address is a postal address attached to a customer.
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
	SKU      string `json:"id,omitempty"`
	Name     string `json:"name"`
	Price    int64  `json:"price"`
	Quantity int    `json:"quantity"`
	Category string `json:"category,omitempty"`
	Merchant string `json:"merchant_name,omitempty"`
	URL      string `json:"url,omitempty"`
}

// CreatePaymentRequest asks a gateway to open a payment.
type CreatePaymentRequest struct {
	// OrderID is PayMux's own order identifier, unique across applications.
	OrderID  string
	Amount   int64
	Currency string

	Customer Customer
	Items    []Item

	// EnabledPaymentMethods optionally restricts the channels offered. Empty
	// means "whatever the merchant account has activated" (PRD §20).
	EnabledPaymentMethods []string

	// ExpiresAt bounds how long the payment may be completed.
	ExpiresAt *time.Time

	// CallbackURL is where the gateway returns the payer after checkout.
	CallbackURL string

	// CustomFields carries short gateway-supported free-text fields.
	CustomFields []string

	// Options carries validated gateway-specific parameters. Adapters decode
	// it into their own typed options; it is never forwarded verbatim.
	Options json.RawMessage
}

// Payment is the gateway's response to opening a payment.
type Payment struct {
	OrderID       string
	TransactionID string
	Status        string // the gateway's own status
	Normalized    Status
	PaymentType   string
	// Token and RedirectURL drive hosted checkout (Snap token / redirect URL).
	Token       string
	RedirectURL string
	ExpiresAt   *time.Time
	Raw         map[string]any
}

// Transaction is the gateway's authoritative view of a transaction.
type Transaction struct {
	OrderID         string
	TransactionID   string
	Status          string
	FraudStatus     string
	Normalized      Status
	PaymentType     string
	GrossAmount     int64
	Currency        string
	TransactionTime *time.Time
	SettlementTime  *time.Time
	ExpiresAt       *time.Time
	Raw             map[string]any
}

// WebhookRequest is an inbound gateway notification.
type WebhookRequest struct {
	Headers http.Header
	Body    []byte
}

// Event is a verified, normalized inbound notification.
type Event struct {
	OrderID         string
	TransactionID   string
	Status          string
	FraudStatus     string
	Normalized      Status
	PaymentType     string
	GrossAmount     int64
	Currency        string
	TransactionTime *time.Time
	SettlementTime  *time.Time
	// DedupeKey identifies this exact state report. Redelivering the same
	// notification produces the same key (PRD §39).
	DedupeKey string
	Raw       map[string]any
}

// RefundRequest asks a gateway to refund part or all of a payment.
type RefundRequest struct {
	OrderID       string
	TransactionID string
	Amount        int64
	Reason        string
	// RefundKey is a caller-supplied idempotency key for the refund.
	RefundKey string
}

// Refund is a gateway's response to a refund request.
type Refund struct {
	RefundID       string
	RefundKey      string
	Amount         int64
	Status         string
	Normalized     RefundStatus
	PaymentStatus  Status
	RefundedAmount int64
	Raw            map[string]any
}

// CreateSubscriptionRequest asks a gateway to start a subscription.
type CreateSubscriptionRequest struct {
	Name          string
	Amount        int64
	Currency      string
	PaymentType   string
	PaymentToken  string
	IntervalUnit  string
	IntervalCount int
	MaxInterval   int
	StartTime     *time.Time
	Customer      Customer
	Metadata      map[string]any
	Options       json.RawMessage
}

// UpdateSubscriptionRequest changes a live subscription. Nil fields are left
// as they are.
type UpdateSubscriptionRequest struct {
	Name          *string
	Amount        *int64
	IntervalUnit  *string
	IntervalCount *int
	PaymentToken  *string
	Options       json.RawMessage
}

// Subscription is a gateway's view of a subscription.
type Subscription struct {
	ID            string
	Name          string
	Amount        int64
	Currency      string
	Status        string
	Normalized    SubscriptionStatus
	PaymentType   string
	IntervalUnit  string
	IntervalCount int
	Raw           map[string]any
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

// Errors an adapter raises that the payment domain reacts to.
var (
	// ErrNotSupported reports an operation this gateway or account cannot do.
	ErrNotSupported = errors.New("gateway: operation is not supported")
	// ErrTransactionNotFound reports a transaction the gateway does not know.
	ErrTransactionNotFound = errors.New("gateway: transaction not found")
	// ErrInvalidSignature reports a notification that failed verification.
	ErrInvalidSignature = errors.New("gateway: notification signature is invalid")
)

// Error is a gateway-reported failure carrying the gateway's own codes.
//
// The message is safe to surface to an application: it describes what the
// gateway rejected, and adapters must not put credentials or raw transport
// detail into it.
type Error struct {
	Gateway    string
	StatusCode int
	Code       string
	Message    string
	Retryable  bool
	Err        error
}

// Error implements error.
func (e *Error) Error() string {
	msg := e.Message
	if msg == "" {
		msg = "request failed"
	}
	if e.Code != "" {
		return e.Gateway + ": " + e.Code + ": " + msg
	}
	return e.Gateway + ": " + msg
}

// Unwrap exposes the underlying transport error.
func (e *Error) Unwrap() error { return e.Err }

// IsRetryable reports whether err is worth retrying unchanged.
func IsRetryable(err error) bool {
	var gwErr *Error
	if errors.As(err, &gwErr) {
		return gwErr.Retryable
	}
	return false
}
