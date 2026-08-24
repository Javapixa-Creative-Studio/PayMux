package gateway

import (
	"time"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/crypto"
)

// Account is one configured gateway credential set.
//
// Credentials live here rather than on an application so several applications
// can share a single merchant account, and so a second gateway can be added
// without touching the application domain (PRD §59).
type Account struct {
	ID          string
	Gateway     string
	Name        string
	Environment Environment
	MerchantID  string
	ClientKey   string
	// ServerKey is sealed at rest and never returned through the API (PRD §58).
	ServerKey crypto.Secret

	// Disbursement credentials, sealed the same way and kept apart from the
	// payment key. Midtrans issues two so that whoever can request a payout
	// cannot also release it, and PayMux keeps that separation rather than
	// collapsing them into one field.
	DisbursementCreatorKey  crypto.Secret
	DisbursementApproverKey crypto.Secret

	Enabled   bool
	IsDefault bool

	Capabilities   Capabilities
	LastCheckedAt  *time.Time
	LastCheckOK    *bool
	LastCheckError string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Usable reports whether the account may be used to reach a gateway.
func (a *Account) Usable() bool {
	return a != nil && a.Enabled && a.ServerKey != "" && a.Environment.Valid()
}

// CanDisburse reports whether this account holds the credentials needed to pay
// money out. An adapter that implements disbursement is not enough: Midtrans
// gates payouts behind separate approval and separate keys, so what the code
// can do and what the account may do are different questions.
func (a *Account) CanDisburse() bool {
	return a != nil && a.Enabled && a.DisbursementCreatorKey != ""
}

// CanApprovePayouts reports whether payouts can be released through PayMux
// rather than only in the gateway's own dashboard.
func (a *Account) CanApprovePayouts() bool {
	return a != nil && a.DisbursementApproverKey != ""
}

// IsSandbox reports whether the account points at the gateway's sandbox.
func (a *Account) IsSandbox() bool { return a != nil && a.Environment == Sandbox }

// ExpectedKeyMode is the API-key mode applications should use against this
// account: sandbox accounts pair with test keys, production with live keys.
//
// PayMux checks this pairing on every payment so a test credential can never
// move real money, and a live credential never lands in the sandbox.
func (a *Account) ExpectedKeyMode() crypto.KeyMode {
	if a.IsSandbox() {
		return crypto.KeyModeTest
	}
	return crypto.KeyModeLive
}
