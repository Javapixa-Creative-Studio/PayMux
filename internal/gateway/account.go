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
