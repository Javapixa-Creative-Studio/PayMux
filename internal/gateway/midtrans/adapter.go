package midtrans

import (
	"context"
	"errors"
	"net/http"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
)

// Adapter implements gateway.Gateway for Midtrans.
//
// One adapter serves one configured account, because the account determines
// both the credentials and the environment. Adapters are cheap: they hold a
// client over a shared HTTP transport and no other state.
type Adapter struct {
	account *gateway.Account
	client  *Client
}

// Compile-time proof that the adapter satisfies every capability it claims.
var (
	_ gateway.Gateway             = (*Adapter)(nil)
	_ gateway.RefundGateway       = (*Adapter)(nil)
	_ gateway.CheckoutGateway     = (*Adapter)(nil)
	_ gateway.SubscriptionGateway = (*Adapter)(nil)
	_ gateway.CapabilityReporter  = (*Adapter)(nil)
)

// NewAdapter builds a Midtrans adapter for an account. It matches
// gateway.Factory so the registry can construct it.
func NewAdapter(acc *gateway.Account, client *http.Client) (gateway.Gateway, error) {
	if acc == nil {
		return nil, errors.New("midtrans: no gateway account supplied")
	}
	if acc.Gateway != Name {
		return nil, errors.New("midtrans: account is not configured for midtrans")
	}
	if !acc.Environment.Valid() {
		return nil, errors.New("midtrans: account environment must be sandbox or production")
	}
	if acc.ServerKey == "" {
		return nil, errors.New("midtrans: account has no server key")
	}
	return &Adapter{
		account: acc,
		client:  NewClient(acc.Environment, acc.ServerKey, client),
	}, nil
}

// SetMetrics attaches a recorder to this adapter's client.
func (a *Adapter) SetMetrics(recorder RequestRecorder) {
	a.client.Metrics = recorder
}

// CheckoutScriptURL returns Snap's browser script for this account's
// environment. The client key that goes with it is on the account and is safe
// to expose: it identifies the merchant to the script and authorises nothing.
func (a *Adapter) CheckoutScriptURL() string {
	if a.account.Environment == gateway.Sandbox {
		return snapSandboxURL + "/snap/snap.js"
	}
	return snapProductionURL + "/snap/snap.js"
}

// Name identifies the adapter.
func (a *Adapter) Name() string { return Name }

// Account exposes the account this adapter serves.
func (a *Adapter) Account() *gateway.Account { return a.account }

// CreatePayment opens a payment. Snap is PayMux's V1 payment interface, so it
// is what CreatePayment uses (PRD §14, §91 rule 4).
func (a *Adapter) CreatePayment(ctx context.Context, req gateway.CreatePaymentRequest) (*gateway.Payment, error) {
	return a.CreateSnapTransaction(ctx, req)
}

// CancelCheckoutSession retires a Snap checkout session (PRD §30).
//
// Midtrans has no endpoint that revokes a Snap token on its own. Expiring the
// underlying transaction achieves the same outcome: the token can no longer
// be paid, so that is what PayMux does, and it is why this takes an order id
// rather than a token.
func (a *Adapter) CancelCheckoutSession(ctx context.Context, orderID string) error {
	_, err := a.ExpireTransaction(ctx, orderID)
	return err
}

// Capabilities reports what this account can do (PRD §85).
//
// Refunds and cancellation are always attemptable; whether a specific payment
// channel permits them is decided by Midtrans per transaction. Subscriptions
// are reported from the account's stored capability flags, because recurring
// billing requires activation that PayMux cannot infer.
func (a *Adapter) Capabilities() gateway.Capabilities {
	caps := gateway.Capabilities{
		Checkout:      true,
		Refund:        true,
		PartialRefund: true,
		Cancel:        true,
		Expire:        true,
		Subscriptions: a.account.Capabilities.Subscriptions,
		// Reported from the account rather than hard-coded: the adapter can
		// always disburse, but an account without Iris credentials cannot.
		Disbursement: a.account.CanDisburse(),
	}
	return caps
}

// asGatewayError is errors.As specialised to *gateway.Error.
func asGatewayError(err error, target **gateway.Error) bool {
	return errors.As(err, target)
}

// SetBaseURLs overrides the Snap and Core API endpoints this adapter talks to.
//
// It exists for tests and for deployments that route Midtrans traffic through
// an egress proxy. Ordinary use should leave the endpoints alone: they are
// derived from the account's environment, which is what keeps sandbox and
// production traffic apart.
func (a *Adapter) SetBaseURLs(snapURL, coreURL string) {
	if snapURL != "" {
		a.client.SnapURL = snapURL
	}
	if coreURL != "" {
		a.client.CoreURL = coreURL
	}
}
