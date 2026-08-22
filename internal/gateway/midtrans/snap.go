package midtrans

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anggapixa/paymux/internal/gateway"
	"github.com/anggapixa/paymux/internal/money"
)

// Options are the Midtrans-specific parameters an application may set on a
// payment (PRD §17, §18).
//
// They are typed and validated rather than passed through as free-form JSON:
// forwarding an unvalidated blob to a payment gateway would let an application
// send anything at all under the merchant's credentials.
type Options struct {
	// CreditCard controls card behaviour, including 3-D Secure.
	CreditCard *CreditCardOptions `json:"credit_card,omitempty"`
	// BankTransfer configures virtual-account behaviour.
	BankTransfer *BankTransferOptions `json:"bank_transfer,omitempty"`
	// Gopay and ShopeePay configure their respective wallets.
	Gopay     *WalletOptions `json:"gopay,omitempty"`
	ShopeePay *WalletOptions `json:"shopeepay,omitempty"`
	// Cstore configures convenience-store payments.
	Cstore *CstoreOptions `json:"cstore,omitempty"`
	// PageExpiryMinutes limits how long the Snap page itself stays open.
	PageExpiryMinutes int `json:"page_expiry_minutes,omitempty"`
	// ErrorRedirectURL is where Snap sends the payer after a failure.
	ErrorRedirectURL string `json:"error_redirect_url,omitempty"`
	// UserID lets Midtrans associate the transaction with a saved-card user.
	UserID string `json:"user_id,omitempty"`
}

// CreditCardOptions configures Midtrans's card channel.
type CreditCardOptions struct {
	// Secure enables 3-D Secure. It defaults to true: shifting liability to
	// the issuer is the safer default for a shared merchant account.
	Secure         *bool               `json:"secure,omitempty"`
	Bank           string              `json:"bank,omitempty"`
	Channel        string              `json:"channel,omitempty"`
	Type           string              `json:"type,omitempty"`
	Authentication string              `json:"authentication,omitempty"`
	SaveCard       bool                `json:"save_card,omitempty"`
	WhitelistBins  []string            `json:"whitelist_bins,omitempty"`
	Installment    *InstallmentOptions `json:"installment,omitempty"`
}

// InstallmentOptions configures card instalment terms.
type InstallmentOptions struct {
	Required bool             `json:"required"`
	Terms    map[string][]int `json:"terms,omitempty"`
}

// BankTransferOptions configures virtual-account payments.
type BankTransferOptions struct {
	Bank          string `json:"bank,omitempty"`
	VANumber      string `json:"va_number,omitempty"`
	RecipientName string `json:"recipient_name,omitempty"`
	FreeText      string `json:"free_text,omitempty"`
}

// WalletOptions configures an e-wallet channel.
type WalletOptions struct {
	CallbackURL    string `json:"callback_url,omitempty"`
	EnableCallback bool   `json:"enable_callback,omitempty"`
	TokenizationID string `json:"tokenization_id,omitempty"`
}

// CstoreOptions configures convenience-store payments.
type CstoreOptions struct {
	Store   string `json:"store,omitempty"`
	Message string `json:"message,omitempty"`
}

// ParseOptions decodes and validates gateway options, rejecting unknown
// fields so a typo silently changing payment behaviour is impossible.
func ParseOptions(raw json.RawMessage) (*Options, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return &Options{}, nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()

	var opts Options
	if err := decoder.Decode(&opts); err != nil {
		return nil, fmt.Errorf("midtrans: gateway_options are not valid: %w", err)
	}
	if err := opts.validate(); err != nil {
		return nil, err
	}
	return &opts, nil
}

func (o *Options) validate() error {
	if o.PageExpiryMinutes < 0 {
		return fmt.Errorf("midtrans: page_expiry_minutes must not be negative")
	}
	if o.BankTransfer != nil && o.BankTransfer.Bank != "" && !knownBank(o.BankTransfer.Bank) {
		return fmt.Errorf("midtrans: bank_transfer.bank %q is not a recognised bank", o.BankTransfer.Bank)
	}
	if o.Cstore != nil && o.Cstore.Store != "" && !knownStore(o.Cstore.Store) {
		return fmt.Errorf("midtrans: cstore.store %q is not a recognised store", o.Cstore.Store)
	}
	return nil
}

func knownBank(bank string) bool {
	switch strings.ToLower(bank) {
	case "bca", "bni", "bri", "permata", "cimb", "mandiri", "danamon", "maybank", "mega":
		return true
	}
	return false
}

func knownStore(store string) bool {
	switch strings.ToLower(store) {
	case "indomaret", "alfamart":
		return true
	}
	return false
}

// CreateSnapTransaction opens a Snap checkout session (PRD §14).
func (a *Adapter) CreateSnapTransaction(ctx context.Context, req gateway.CreatePaymentRequest) (*gateway.Payment, error) {
	opts, err := ParseOptions(req.Options)
	if err != nil {
		return nil, err
	}
	payload, err := buildSnapRequest(req, opts)
	if err != nil {
		return nil, err
	}

	var resp snapResponse
	if err := a.client.do(ctx, "POST", a.client.snapEndpoint("/snap/v1/transactions"), payload, &resp); err != nil {
		return nil, err
	}
	if resp.Token == "" {
		return nil, &gateway.Error{
			Gateway: Name,
			Message: "the gateway did not return a checkout token",
		}
	}

	return &gateway.Payment{
		OrderID:     req.OrderID,
		Status:      StatusPending,
		Normalized:  gateway.StatusPending,
		Token:       resp.Token,
		RedirectURL: resp.RedirectURL,
		ExpiresAt:   req.ExpiresAt,
		Raw: map[string]any{
			"token":        resp.Token,
			"redirect_url": resp.RedirectURL,
		},
	}, nil
}

// buildSnapRequest translates a normalized request into Snap's payload.
func buildSnapRequest(req gateway.CreatePaymentRequest, opts *Options) (*snapRequest, error) {
	grossAmount, err := money.Format(req.Amount, req.Currency)
	if err != nil {
		return nil, fmt.Errorf("midtrans: %w", err)
	}

	payload := &snapRequest{
		TransactionDetails: snapTransactionDetails{
			OrderID:     req.OrderID,
			GrossAmount: grossAmount,
		},
		EnabledPayments: req.EnabledPaymentMethods,
	}

	if items, err := buildItems(req); err != nil {
		return nil, err
	} else {
		payload.ItemDetails = items
	}

	if customer := buildCustomer(req.Customer); customer != nil {
		payload.CustomerDetails = customer
	}

	// Snap allows three custom fields; anything beyond that is dropped rather
	// than failing the payment.
	for i, field := range req.CustomFields {
		switch i {
		case 0:
			payload.CustomField1 = field
		case 1:
			payload.CustomField2 = field
		case 2:
			payload.CustomField3 = field
		}
	}

	if req.CallbackURL != "" || (opts.ErrorRedirectURL != "") {
		payload.Callbacks = &snapCallbacks{Finish: req.CallbackURL, Error: opts.ErrorRedirectURL}
	}
	if expiry := buildExpiry(req.ExpiresAt); expiry != nil {
		payload.Expiry = expiry
	}
	if opts.PageExpiryMinutes > 0 {
		payload.PageExpiry = &snapPageExpiry{Duration: opts.PageExpiryMinutes, Unit: "minutes"}
	}
	payload.UserID = opts.UserID

	applyOptions(payload, opts)
	return payload, nil
}

// buildItems converts line items, verifying they add up to the payment total.
//
// Midtrans rejects a transaction whose item details do not sum to the gross
// amount, so the mismatch is caught here where the message can be useful.
func buildItems(req gateway.CreatePaymentRequest) ([]snapItem, error) {
	if len(req.Items) == 0 {
		return nil, nil
	}
	items := make([]snapItem, 0, len(req.Items))
	var total int64
	for _, item := range req.Items {
		price, err := money.Format(item.Price, req.Currency)
		if err != nil {
			return nil, fmt.Errorf("midtrans: item %q: %w", item.Name, err)
		}
		total += item.Price * int64(item.Quantity)
		items = append(items, snapItem{
			ID:           item.SKU,
			Name:         truncate(item.Name, 50), // Snap caps item names at 50 characters
			Price:        price,
			Quantity:     item.Quantity,
			Category:     item.Category,
			MerchantName: item.Merchant,
			URL:          item.URL,
		})
	}
	if total != req.Amount {
		return nil, fmt.Errorf(
			"midtrans: item details total %d does not equal the payment amount %d", total, req.Amount)
	}
	return items, nil
}

func buildCustomer(c gateway.Customer) *snapCustomer {
	if c.FirstName == "" && c.LastName == "" && c.Email == "" && c.Phone == "" {
		return nil
	}
	out := &snapCustomer{
		FirstName: c.FirstName,
		LastName:  c.LastName,
		Email:     c.Email,
		Phone:     c.Phone,
	}
	if c.Billing != nil {
		out.BillingAddress = toSnapAddress(c.Billing)
	}
	if c.Shipping != nil {
		out.ShippingAddress = toSnapAddress(c.Shipping)
	}
	return out
}

func toSnapAddress(a *gateway.Address) *snapAddress {
	return &snapAddress{
		FirstName:   a.FirstName,
		LastName:    a.LastName,
		Phone:       a.Phone,
		Address:     a.Address,
		City:        a.City,
		PostalCode:  a.PostalCode,
		CountryCode: a.CountryCode,
	}
}

// buildExpiry converts an absolute expiry into Snap's start-plus-duration
// form, rounding up so a payment is never cut short by sub-minute truncation.
func buildExpiry(expiresAt *time.Time) *snapExpiry {
	if expiresAt == nil {
		return nil
	}
	start := time.Now()
	minutes := int((expiresAt.Sub(start) + time.Minute - 1) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	return &snapExpiry{
		StartTime: formatTime(start),
		Unit:      "minute",
		Duration:  minutes,
	}
}

// applyOptions folds validated gateway options into the Snap payload.
func applyOptions(payload *snapRequest, opts *Options) {
	// 3-D Secure is on unless an application deliberately turns it off.
	card := &snapCreditCard{Secure: true}
	if opts.CreditCard != nil {
		cc := opts.CreditCard
		if cc.Secure != nil {
			card.Secure = *cc.Secure
		}
		card.Bank = cc.Bank
		card.Channel = cc.Channel
		card.Type = cc.Type
		card.Authentication = cc.Authentication
		card.SaveCard = cc.SaveCard
		card.WhitelistBins = cc.WhitelistBins
		if cc.Installment != nil {
			card.Installment = &snapInstallment{Required: cc.Installment.Required, Terms: cc.Installment.Terms}
		}
	}
	payload.CreditCard = card

	if bt := opts.BankTransfer; bt != nil {
		payload.BankTransfer = &snapBankTransfer{Bank: strings.ToLower(bt.Bank), VANumber: bt.VANumber}
		if bt.RecipientName != "" {
			payload.BankTransfer.Permata = &snapPermata{RecipientName: bt.RecipientName}
		}
		if bt.FreeText != "" {
			payload.BankTransfer.FreeText = bt.FreeText
		}
	}
	if g := opts.Gopay; g != nil {
		payload.Gopay = &snapGopay{
			EnableCallback: g.EnableCallback,
			CallbackURL:    g.CallbackURL,
			TokenizationID: g.TokenizationID,
		}
	}
	if sp := opts.ShopeePay; sp != nil {
		payload.ShopeePay = &snapShopeePay{CallbackURL: sp.CallbackURL}
	}
	if cs := opts.Cstore; cs != nil {
		payload.Cstore = &snapCstore{Store: strings.ToLower(cs.Store), Message: cs.Message}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
