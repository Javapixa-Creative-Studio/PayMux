package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// PayoutStatus is PayMux's normalized state for money leaving the merchant
// balance (PRD §94).
//
// It is deliberately larger than the gateway's own set. Midtrans reports four
// states, all of them about what the bank is doing; PayMux has to record two
// things the gateway has no opinion on: whether a human released the payout,
// and whether PayMux knows the outcome at all.
type PayoutStatus string

const (
	// PayoutRequested is recorded before anything is sent anywhere. A payout
	// in this state has moved no money and can still be refused.
	PayoutRequested PayoutStatus = "REQUESTED"

	// PayoutApproved means a human released it and it is queued for
	// submission. Still no money moved: the gateway has not been called.
	PayoutApproved PayoutStatus = "APPROVED"

	// PayoutSubmitted means the gateway accepted it and returned a reference.
	// Money is now in flight and PayMux can no longer stop it.
	PayoutSubmitted PayoutStatus = "SUBMITTED"

	// PayoutUnresolved means PayMux sent the request and does not know what
	// happened to it: the connection failed at the one moment where failure
	// is ambiguous. The money may or may not be moving.
	//
	// This is the state that justifies the whole idempotency apparatus. It is
	// not terminal: a retry with the original key, or a later poll, resolves
	// it. It exists so that "we don't know" is a state the system can hold and
	// report, rather than a guess it has to make.
	PayoutUnresolved PayoutStatus = "UNRESOLVED"

	// PayoutCompleted means the beneficiary received the money.
	PayoutCompleted PayoutStatus = "COMPLETED"

	// PayoutFailed means the transfer did not happen and the funds stayed put.
	PayoutFailed PayoutStatus = "FAILED"

	// PayoutRejected means an approver refused it. Distinct from FAILED
	// because nothing was ever attempted, and the reason is a person's.
	PayoutRejected PayoutStatus = "REJECTED"
)

// payoutSuccessors is the complete set of allowed transitions.
//
// Payments derive this from a rank comparison, which is right for them: their
// states form a line and the only question is whether a notification arrived
// late. Payouts get an explicit allow-list instead. The difference is that a
// wrong answer here moves real money out of a real account, so every edge
// someone might later want has to be written down and argued for rather than
// falling out of an inequality.
var payoutSuccessors = map[PayoutStatus][]PayoutStatus{
	// A request can be released, refused, or fail validation before it is
	// ever sent: an over-limit or disabled-beneficiary request never reaches
	// the gateway.
	PayoutRequested: {PayoutApproved, PayoutRejected, PayoutFailed},

	// Once released it is either handed over, or fails trying. It cannot be
	// rejected any more: rejection is a decision about a request, and this
	// request has already been decided.
	PayoutApproved: {PayoutSubmitted, PayoutFailed, PayoutUnresolved},

	// In the gateway's hands. PayMux only observes from here.
	PayoutSubmitted: {PayoutCompleted, PayoutFailed},

	// Reconciliation resolves an unknown outcome in one of two directions. It
	// can also turn out to have been submitted after all, once a retry with
	// the original idempotency key returns the original reference.
	PayoutUnresolved: {PayoutSubmitted, PayoutCompleted, PayoutFailed},

	PayoutCompleted: nil,
	PayoutFailed:    nil,
	PayoutRejected:  nil,
}

var payoutRanks = map[PayoutStatus]int{
	PayoutRequested:  10,
	PayoutApproved:   20,
	PayoutSubmitted:  30,
	PayoutUnresolved: 35,
	PayoutCompleted:  40,
	PayoutFailed:     40,
	PayoutRejected:   40,
}

// Valid reports whether s is a known payout status.
func (s PayoutStatus) Valid() bool {
	_, ok := payoutRanks[s]
	return ok
}

// String implements fmt.Stringer.
func (s PayoutStatus) String() string { return string(s) }

// Rank orders statuses by how far through the lifecycle they are.
func (s PayoutStatus) Rank() int { return payoutRanks[s] }

// Terminal reports whether no further transition is expected from s.
func (s PayoutStatus) Terminal() bool {
	return len(payoutSuccessors[s]) == 0 && s.Valid()
}

// Settled reports whether money has irreversibly left the balance.
//
// SUBMITTED counts: once the gateway has it, PayMux cannot recall it, and
// anything that reasons about exposure has to treat it as spent.
func (s PayoutStatus) Settled() bool {
	switch s {
	case PayoutSubmitted, PayoutCompleted:
		return true
	default:
		return false
	}
}

// InFlight reports whether the outcome is still unknown and worth polling.
func (s PayoutStatus) InFlight() bool {
	switch s {
	case PayoutApproved, PayoutSubmitted, PayoutUnresolved:
		return true
	default:
		return false
	}
}

// CanTransitionTo reports whether a payout in status s may move to next.
func (s PayoutStatus) CanTransitionTo(next PayoutStatus) bool {
	if !s.Valid() || !next.Valid() || s == next {
		return false
	}
	for _, allowed := range payoutSuccessors[s] {
		if allowed == next {
			return true
		}
	}
	return false
}

// PayoutPredecessorsOf lists the statuses a payout may be in and still move to
// next.
//
// The repository puts this straight into the UPDATE's WHERE clause, so the
// state machine is enforced by the same statement that performs the write and
// two concurrent workers cannot both advance the same payout.
func PayoutPredecessorsOf(next PayoutStatus) []string {
	var out []string
	for from, successors := range payoutSuccessors {
		for _, allowed := range successors {
			if allowed == next {
				out = append(out, string(from))
			}
		}
	}
	return out
}

// ParsePayoutStatus converts a string to a PayoutStatus, rejecting unknowns.
func ParsePayoutStatus(v string) (PayoutStatus, error) {
	s := PayoutStatus(v)
	if !s.Valid() {
		return "", fmt.Errorf("gateway: unknown payout status %q", v)
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// The disbursement contract
// ---------------------------------------------------------------------------

// DisbursementGateway is implemented by adapters that can pay money out.
//
// It is a separate interface from Gateway for the same reason RefundGateway
// is: whether an account may disburse depends on merchant configuration and
// approval, not only on what the adapter can do. An adapter that implements
// this still reports Disbursement: false until the credentials exist.
type DisbursementGateway interface {
	// CreatePayout submits a payout for execution.
	//
	// It must send req.IdempotencyKey to the gateway. On any error where the
	// request may still have been received: a timeout, a dropped connection,
	// a 5xx: it must return an error wrapping ErrOutcomeUnknown rather than a
	// plain failure, because the difference decides whether PayMux may retry.
	CreatePayout(ctx context.Context, req CreatePayoutRequest) (*PayoutResult, error)

	// ApprovePayout releases payouts the gateway is holding. Called with the
	// approver credential, which is a different key from the creator's.
	ApprovePayout(ctx context.Context, req ApprovePayoutRequest) error

	// RejectPayout refuses payouts the gateway is holding.
	RejectPayout(ctx context.Context, req RejectPayoutRequest) error

	// GetPayout reads a payout's authoritative state.
	GetPayout(ctx context.Context, referenceNo string) (*PayoutResult, error)
}

// AccountValidator is implemented by adapters that can check a destination
// before money is sent to it. Separate because it is a courtesy, not a
// requirement: a gateway without it still disburses.
type AccountValidator interface {
	// ValidateAccount confirms an account exists and returns the name the
	// bank holds for it, so a caller can compare it against their own record.
	ValidateAccount(ctx context.Context, account, bank string) (*AccountValidation, error)
}

// BalanceReporter is implemented by adapters that can say how much is
// available to pay out.
//
// Separate from DisbursementGateway because the balance is a different
// question from a transfer, and a gateway that cannot answer it can still
// disburse: PayMux's own limits are what bound spending, not this.
type BalanceReporter interface {
	GetBalance(ctx context.Context) (*Balance, error)
}

// Balance is what a merchant has available to disburse.
type Balance struct {
	Amount   int64
	Currency string
	// Raw is kept so an operator can see exactly what the gateway said, which
	// matters when a number that decides whether payouts can proceed does not
	// look right.
	Raw json.RawMessage
}

// BankLister is implemented by adapters that can enumerate valid destinations.
type BankLister interface {
	ListBanks(ctx context.Context) ([]Bank, error)
}

// CreatePayoutRequest is one payout, as PayMux hands it to an adapter.
type CreatePayoutRequest struct {
	// IdempotencyKey must be stable across retries of the same logical payout.
	// It is the only thing standing between a retried timeout and a second
	// transfer.
	IdempotencyKey string

	Amount   int64
	Currency string

	BeneficiaryName    string
	BeneficiaryAccount string
	BeneficiaryBank    string
	BeneficiaryEmail   string

	// Notes rides along to the beneficiary's statement. Gateways restrict the
	// character set, so adapters sanitise rather than reject.
	Notes string
}

// PayoutResult is a payout's state as the gateway reports it.
type PayoutResult struct {
	ReferenceNo string
	Status      PayoutStatus
	// The gateway's own word, kept beside the normalized one so an operator
	// can always see what was actually said.
	GatewayStatus string
	Amount        int64
	FailureCode   string
	FailureReason string
	UpdatedAt     time.Time
	Raw           json.RawMessage
}

// ApprovePayoutRequest releases one or more payouts.
type ApprovePayoutRequest struct {
	ReferenceNos []string
	// OTP, when the merchant has transfer whitelisting turned on.
	OTP string
}

// RejectPayoutRequest refuses one or more payouts.
type RejectPayoutRequest struct {
	ReferenceNos []string
	Reason       string
}

// AccountValidation is a destination account as the bank describes it.
type AccountValidation struct {
	AccountName   string
	AccountNumber string
	Raw           json.RawMessage
}

// Bank is a destination a gateway can pay out to.
type Bank struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

var (
	// ErrOutcomeUnknown reports that a request may or may not have been
	// executed. It is the most important error in this package.
	//
	// Every other failure says "this did not happen", and the caller may
	// safely try again. This one says PayMux cannot tell, so retrying without
	// the original idempotency key could send the money twice.
	ErrOutcomeUnknown = errors.New("gateway: payout outcome is unknown")

	// ErrIdempotencyConflict reports that the key was reused with a different
	// request body. It means a caller changed the amount or destination and
	// kept the key, which is a bug worth surfacing rather than papering over.
	ErrIdempotencyConflict = errors.New("gateway: idempotency key was reused with different content")

	// ErrPayoutNotFound reports that the gateway has no such reference.
	ErrPayoutNotFound = errors.New("gateway: payout not found")

	// ErrInsufficientBalance reports that the merchant balance cannot cover it.
	ErrInsufficientBalance = errors.New("gateway: insufficient balance for payout")
)
