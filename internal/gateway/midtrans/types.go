// Package midtrans implements PayMux's Midtrans gateway adapter.
//
// Everything Midtrans-specific lives here: its request and response shapes,
// its status vocabulary, its signature scheme and its endpoints. The rest of
// PayMux sees only the normalized types in package gateway (PRD §91 rule 6).
package midtrans

// Name is this adapter's identifier.
const Name = "midtrans"

// Base URLs. Snap and the Core API are separate hosts, and each has its own
// sandbox, so an environment mix-up cannot happen silently (PRD §13).
const (
	snapSandboxURL    = "https://app.sandbox.midtrans.com"
	snapProductionURL = "https://app.midtrans.com"
	coreSandboxURL    = "https://api.sandbox.midtrans.com"
	coreProductionURL = "https://api.midtrans.com"
)

// Midtrans transaction statuses (PRD §25). This list reflects the documented
// values; an unrecognised status is preserved verbatim and reported rather
// than dropped, because a new status must never be silently ignored.
const (
	StatusPending       = "pending"
	StatusCapture       = "capture"
	StatusSettlement    = "settlement"
	StatusDeny          = "deny"
	StatusCancel        = "cancel"
	StatusExpire        = "expire"
	StatusFailure       = "failure"
	StatusRefund        = "refund"
	StatusPartialRefund = "partial_refund"
	StatusAuthorize     = "authorize"
)

// Fraud statuses Midtrans reports for card transactions.
const (
	FraudAccept    = "accept"
	FraudChallenge = "challenge"
	FraudDeny      = "deny"
)

// snapRequest is the Snap "create transaction" payload.
//
// Fields are typed rather than passed through as free-form JSON so PayMux
// validates what it sends (PRD §18).
type snapRequest struct {
	TransactionDetails snapTransactionDetails `json:"transaction_details"`
	ItemDetails        []snapItem             `json:"item_details,omitempty"`
	CustomerDetails    *snapCustomer          `json:"customer_details,omitempty"`
	EnabledPayments    []string               `json:"enabled_payments,omitempty"`
	CreditCard         *snapCreditCard        `json:"credit_card,omitempty"`
	BankTransfer       *snapBankTransfer      `json:"bank_transfer,omitempty"`
	Callbacks          *snapCallbacks         `json:"callbacks,omitempty"`
	Expiry             *snapExpiry            `json:"expiry,omitempty"`
	PageExpiry         *snapPageExpiry        `json:"page_expiry,omitempty"`
	CustomField1       string                 `json:"custom_field1,omitempty"`
	CustomField2       string                 `json:"custom_field2,omitempty"`
	CustomField3       string                 `json:"custom_field3,omitempty"`
	UserID             string                 `json:"user_id,omitempty"`
	Gopay              *snapGopay             `json:"gopay,omitempty"`
	ShopeePay          *snapShopeePay         `json:"shopeepay,omitempty"`
	Cstore             *snapCstore            `json:"cstore,omitempty"`
}

type snapTransactionDetails struct {
	OrderID     string `json:"order_id"`
	GrossAmount string `json:"gross_amount"`
}

type snapItem struct {
	ID           string `json:"id,omitempty"`
	Name         string `json:"name"`
	Price        string `json:"price"`
	Quantity     int    `json:"quantity"`
	Brand        string `json:"brand,omitempty"`
	Category     string `json:"category,omitempty"`
	MerchantName string `json:"merchant_name,omitempty"`
	URL          string `json:"url,omitempty"`
}

type snapCustomer struct {
	FirstName       string       `json:"first_name,omitempty"`
	LastName        string       `json:"last_name,omitempty"`
	Email           string       `json:"email,omitempty"`
	Phone           string       `json:"phone,omitempty"`
	BillingAddress  *snapAddress `json:"billing_address,omitempty"`
	ShippingAddress *snapAddress `json:"shipping_address,omitempty"`
}

type snapAddress struct {
	FirstName   string `json:"first_name,omitempty"`
	LastName    string `json:"last_name,omitempty"`
	Phone       string `json:"phone,omitempty"`
	Address     string `json:"address,omitempty"`
	City        string `json:"city,omitempty"`
	PostalCode  string `json:"postal_code,omitempty"`
	CountryCode string `json:"country_code,omitempty"`
}

type snapCreditCard struct {
	Secure            bool                   `json:"secure"`
	Bank              string                 `json:"bank,omitempty"`
	Channel           string                 `json:"channel,omitempty"`
	Type              string                 `json:"type,omitempty"`
	Authentication    string                 `json:"authentication,omitempty"`
	SaveCard          bool                   `json:"save_card,omitempty"`
	Installment       *snapInstallment       `json:"installment,omitempty"`
	WhitelistBins     []string               `json:"whitelist_bins,omitempty"`
	DynamicDescriptor *snapDynamicDescriptor `json:"dynamic_descriptor,omitempty"`
}

type snapInstallment struct {
	Required bool             `json:"required"`
	Terms    map[string][]int `json:"terms,omitempty"`
}

type snapDynamicDescriptor struct {
	MerchantName string `json:"merchant_name,omitempty"`
	CityName     string `json:"city_name,omitempty"`
	CountryCode  string `json:"country_code,omitempty"`
}

type snapBankTransfer struct {
	Bank     string       `json:"bank,omitempty"`
	VANumber string       `json:"va_number,omitempty"`
	Permata  *snapPermata `json:"permata,omitempty"`
	FreeText any          `json:"free_text,omitempty"`
}

type snapPermata struct {
	RecipientName string `json:"recipient_name,omitempty"`
}

type snapCallbacks struct {
	Finish string `json:"finish,omitempty"`
	Error  string `json:"error,omitempty"`
}

// snapExpiry bounds how long the transaction may be paid.
type snapExpiry struct {
	StartTime string `json:"start_time,omitempty"`
	Unit      string `json:"unit"`
	Duration  int    `json:"duration"`
}

type snapPageExpiry struct {
	Duration int    `json:"duration"`
	Unit     string `json:"unit"`
}

type snapGopay struct {
	EnableCallback bool   `json:"enable_callback,omitempty"`
	CallbackURL    string `json:"callback_url,omitempty"`
	TokenizationID string `json:"tokenization_id,omitempty"`
}

type snapShopeePay struct {
	CallbackURL string `json:"callback_url,omitempty"`
}

type snapCstore struct {
	Store             string `json:"store,omitempty"`
	Message           string `json:"message,omitempty"`
	AlfamartFreeText1 string `json:"alfamart_free_text_1,omitempty"`
	AlfamartFreeText2 string `json:"alfamart_free_text_2,omitempty"`
	AlfamartFreeText3 string `json:"alfamart_free_text_3,omitempty"`
}

// snapResponse is Snap's reply to a create-transaction call.
type snapResponse struct {
	Token         string   `json:"token"`
	RedirectURL   string   `json:"redirect_url"`
	ErrorMessages []string `json:"error_messages"`
}

// transactionResponse is the Core API's transaction representation. It is
// shared by status, cancel, expire and notification payloads, which all use
// the same shape.
type transactionResponse struct {
	StatusCode        string `json:"status_code"`
	StatusMessage     string `json:"status_message"`
	TransactionID     string `json:"transaction_id"`
	OrderID           string `json:"order_id"`
	GrossAmount       string `json:"gross_amount"`
	Currency          string `json:"currency"`
	PaymentType       string `json:"payment_type"`
	TransactionTime   string `json:"transaction_time"`
	TransactionStatus string `json:"transaction_status"`
	FraudStatus       string `json:"fraud_status"`
	SettlementTime    string `json:"settlement_time"`
	ExpiryTime        string `json:"expiry_time"`
	SignatureKey      string `json:"signature_key"`
	MerchantID        string `json:"merchant_id"`
	ApprovalCode      string `json:"approval_code"`
	Bank              string `json:"bank"`
	VANumbers         []struct {
		Bank     string `json:"bank"`
		VANumber string `json:"va_number"`
	} `json:"va_numbers"`
	PermataVANumber        string        `json:"permata_va_number"`
	BillKey                string        `json:"bill_key"`
	BillerCode             string        `json:"biller_code"`
	PaymentCode            string        `json:"payment_code"`
	Store                  string        `json:"store"`
	Issuer                 string        `json:"issuer"`
	Acquirer               string        `json:"acquirer"`
	MaskedCard             string        `json:"masked_card"`
	CardType               string        `json:"card_type"`
	ChannelResponseCode    string        `json:"channel_response_code"`
	ChannelResponseMessage string        `json:"channel_response_message"`
	RefundAmount           string        `json:"refund_amount"`
	RefundChargebackAmount string        `json:"refund_chargeback_amount"`
	Refunds                []refundEntry `json:"refunds"`
}

type refundEntry struct {
	RefundChargebackID int    `json:"refund_chargeback_id"`
	RefundAmount       string `json:"refund_amount"`
	Reason             string `json:"reason"`
	RefundKey          string `json:"refund_key"`
	RefundMethod       string `json:"refund_method"`
	BankConfirmedAt    string `json:"bank_confirmed_at"`
	CreatedAt          string `json:"created_at"`
}

// refundRequest is the Core API refund payload.
type refundRequest struct {
	RefundKey string `json:"refund_key,omitempty"`
	Amount    int64  `json:"amount,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// subscriptionRequest creates a Midtrans subscription.
type subscriptionRequest struct {
	Name            string               `json:"name"`
	Amount          string               `json:"amount"`
	Currency        string               `json:"currency"`
	PaymentType     string               `json:"payment_type"`
	Token           string               `json:"token"`
	Schedule        subscriptionSchedule `json:"schedule"`
	Metadata        map[string]any       `json:"metadata,omitempty"`
	CustomerDetails *snapCustomer        `json:"customer_details,omitempty"`
	Gopay           *subscriptionGopay   `json:"gopay,omitempty"`
	Retry           *subscriptionRetry   `json:"retry_schedule,omitempty"`
}

type subscriptionSchedule struct {
	Interval     int    `json:"interval"`
	IntervalUnit string `json:"interval_unit"`
	MaxInterval  int    `json:"max_interval,omitempty"`
	StartTime    string `json:"start_time,omitempty"`
}

type subscriptionGopay struct {
	AccountID string `json:"account_id,omitempty"`
}

type subscriptionRetry struct {
	Interval     int    `json:"interval,omitempty"`
	IntervalUnit string `json:"interval_unit,omitempty"`
	MaxInterval  int    `json:"max_interval,omitempty"`
}

// subscriptionResponse is Midtrans's subscription representation.
type subscriptionResponse struct {
	ID             string               `json:"id"`
	Name           string               `json:"name"`
	Amount         string               `json:"amount"`
	Currency       string               `json:"currency"`
	CreatedAt      string               `json:"created_at"`
	Schedule       subscriptionSchedule `json:"schedule"`
	Status         string               `json:"status"`
	Token          string               `json:"token"`
	PaymentType    string               `json:"payment_type"`
	TransactionIDs []string             `json:"transaction_ids"`
	Metadata       map[string]any       `json:"metadata"`
	StatusMessage  string               `json:"status_message"`
	StatusCode     string               `json:"status_code"`
}

// errorResponse is Midtrans's error envelope.
type errorResponse struct {
	StatusCode         string   `json:"status_code"`
	StatusMessage      string   `json:"status_message"`
	ErrorMessages      []string `json:"error_messages"`
	ID                 string   `json:"id"`
	ValidationMessages []string `json:"validation_messages"`
}
