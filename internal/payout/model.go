// Package payout is PayMux's money-out domain: beneficiaries, payouts, and
// the rules that decide whether a transfer may happen at all.
//
// It is deliberately not part of package payment. The two look similar on
// paper — an amount, a gateway, a status that advances — but they answer
// opposite questions. A payment asks whether money arrived, and a mistake
// costs a reconciliation. A payout asks whether money should leave, and a
// mistake costs the money. Sharing a package would invite sharing the
// looser rules.
package payout

import (
	"encoding/json"
	"time"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
)

// Beneficiary is a destination an application may pay out to.
type Beneficiary struct {
	ID            string          `json:"id"`
	Object        string          `json:"object"`
	ApplicationID string          `json:"application_id"`
	Alias         string          `json:"alias"`
	Name          string          `json:"name"`
	Account       string          `json:"account"`
	Bank          string          `json:"bank"`
	Email         string          `json:"email,omitempty"`
	VerifiedAt    *time.Time      `json:"verified_at,omitempty"`
	VerifiedName  string          `json:"verified_name,omitempty"`
	DisabledAt    *time.Time      `json:"disabled_at,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// Usable reports whether money may be sent to this beneficiary.
func (b *Beneficiary) Usable() bool { return b != nil && b.DisabledAt == nil }

// Verified reports whether the gateway has confirmed the account exists and
// the holder's name matches what was recorded.
func (b *Beneficiary) Verified() bool { return b != nil && b.VerifiedAt != nil }

// Payout is one transfer out of the merchant balance.
type Payout struct {
	ID                  string `json:"id"`
	Object              string `json:"object"`
	ApplicationID       string `json:"application_id"`
	GatewayAccountID    string `json:"-"`
	Gateway             string `json:"gateway"`
	ApplicationPayoutID string `json:"application_payout_id"`

	// IdempotencyKey is what PayMux sends to the gateway and never
	// regenerates. It is not exposed: it is a safety mechanism, not data an
	// application has any use for.
	IdempotencyKey string `json:"-"`

	// ReferenceNo is nil while PayMux does not know whether the gateway
	// accepted the payout.
	ReferenceNo *string `json:"reference_no,omitempty"`

	BeneficiaryID *string `json:"beneficiary_id,omitempty"`
	// A copy of the destination as it was when the payout was requested.
	BeneficiaryName    string `json:"beneficiary_name"`
	BeneficiaryAccount string `json:"beneficiary_account"`
	BeneficiaryBank    string `json:"beneficiary_bank"`
	BeneficiaryEmail   string `json:"beneficiary_email,omitempty"`

	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	Notes    string `json:"notes,omitempty"`

	Status        gateway.PayoutStatus `json:"status"`
	StatusRank    int                  `json:"-"`
	GatewayStatus string               `json:"gateway_status,omitempty"`
	FailureCode   string               `json:"failure_code,omitempty"`
	FailureReason string               `json:"failure_reason,omitempty"`

	RequestedBy  *string    `json:"requested_by,omitempty"`
	ApprovedBy   *string    `json:"approved_by,omitempty"`
	ApprovedAt   *time.Time `json:"approved_at,omitempty"`
	RejectedBy   *string    `json:"rejected_by,omitempty"`
	RejectedAt   *time.Time `json:"rejected_at,omitempty"`
	RejectReason string     `json:"reject_reason,omitempty"`

	SubmittedAt          *time.Time `json:"submitted_at,omitempty"`
	CompletedAt          *time.Time `json:"completed_at,omitempty"`
	FailedAt             *time.Time `json:"failed_at,omitempty"`
	LastSyncedAt         *time.Time `json:"last_synced_at,omitempty"`
	IdempotencyExpiresAt *time.Time `json:"-"`

	GatewayData json.RawMessage `json:"-"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// NeedsApproval reports whether the payout is waiting on a human.
func (p *Payout) NeedsApproval() bool {
	return p != nil && p.Status == gateway.PayoutRequested
}

// Submittable reports whether the payout is released and not yet handed over.
func (p *Payout) Submittable() bool {
	return p != nil && p.Status == gateway.PayoutApproved
}

// Recoverable reports whether an unresolved payout can still be settled by
// asking the gateway again with the original idempotency key.
//
// Midtrans discards idempotency keys after 24 hours. Past that point a retry
// would be a new request rather than a question about the old one, so PayMux
// stops retrying and the payout becomes a person's problem.
func (p *Payout) Recoverable(now time.Time) bool {
	if p == nil || p.Status != gateway.PayoutUnresolved {
		return false
	}
	if p.IdempotencyExpiresAt == nil {
		return false
	}
	return now.Before(*p.IdempotencyExpiresAt)
}

// Stranded reports an unresolved payout that can no longer be settled
// automatically. Somebody has to look at the gateway's own records.
func (p *Payout) Stranded(now time.Time) bool {
	return p != nil && p.Status == gateway.PayoutUnresolved && !p.Recoverable(now)
}

// Transition is one recorded change of state, kept for the audit trail.
type Transition struct {
	ID          string               `json:"id"`
	PayoutID    string               `json:"payout_id"`
	FromStatus  gateway.PayoutStatus `json:"from_status,omitempty"`
	ToStatus    gateway.PayoutStatus `json:"to_status"`
	ActorKind   string               `json:"actor_kind"`
	ActorID     string               `json:"actor_id,omitempty"`
	Reason      string               `json:"reason,omitempty"`
	GatewayData json.RawMessage      `json:"-"`
	CreatedAt   time.Time            `json:"created_at"`
}

// Actor kinds, recorded on every transition so the trail says who as well as
// what.
const (
	ActorApplication = "application"
	ActorAdmin       = "admin"
	ActorGateway     = "gateway"
	ActorWorker      = "worker"
)

// Limits are an application's payout permissions.
//
// Zero value means disabled, which is what an application that nobody has
// configured should be.
type Limits struct {
	Enabled          bool
	RequiresApproval bool
	// MaxAmount caps a single payout. nil means no ceiling, which is only
	// reachable by someone explicitly clearing it.
	MaxAmount *int64
	// DailyLimit caps the total sent in a rolling day.
	DailyLimit *int64
}

// Filter narrows a payout listing.
type Filter struct {
	ApplicationID string
	Status        string
	BeneficiaryID string
	Search        string
}
