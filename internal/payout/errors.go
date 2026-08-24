package payout

import "errors"

// Domain errors. Handlers map these onto the public error contract.
var (
	// ErrPayoutsDisabled reports an application that has not been permitted to
	// pay out. This is the default state, and it is deliberately the first
	// check: everything else about a payout is irrelevant if the application
	// was never allowed to make one.
	ErrPayoutsDisabled = errors.New("payout: this application is not permitted to disburse")

	// ErrExceedsMaxAmount reports a single payout above the per-payout ceiling.
	ErrExceedsMaxAmount = errors.New("payout: amount exceeds this application's per-payout limit")

	// ErrExceedsDailyLimit reports a payout that would take the application
	// past its rolling daily total.
	ErrExceedsDailyLimit = errors.New("payout: amount exceeds this application's daily limit")

	// ErrDuplicatePayoutID reports a second payout for a reference the
	// application has already used. It is how a retried request becomes the
	// same payout rather than a second transfer.
	ErrDuplicatePayoutID = errors.New("payout: a payout with this id already exists")

	// ErrBeneficiaryNotFound reports an unknown or unowned destination.
	ErrBeneficiaryNotFound = errors.New("payout: beneficiary not found")

	// ErrBeneficiaryDisabled reports a destination somebody has turned off.
	ErrBeneficiaryDisabled = errors.New("payout: this beneficiary is disabled")

	// ErrDuplicateAlias reports a beneficiary alias already in use.
	ErrDuplicateAlias = errors.New("payout: a beneficiary with this alias already exists")

	// ErrPayoutNotFound reports an unknown or unowned payout.
	ErrPayoutNotFound = errors.New("payout: payout not found")

	// ErrStaleTransition reports a state change that would have moved a payout
	// backwards, or that lost a race. Expected under concurrent reconciliation.
	ErrStaleTransition = errors.New("payout: state change is stale and was not applied")

	// ErrNotPending reports an approve or reject on a payout that is no longer
	// waiting for one. Once released, a payout cannot be un-released.
	ErrNotPending = errors.New("payout: this payout is no longer awaiting approval")

	// ErrSelfApproval reports an approver who is also the requester. The
	// database refuses this too; the domain checks first so the caller gets a
	// sentence rather than a constraint violation.
	ErrSelfApproval = errors.New("payout: a payout cannot be approved by the person who requested it")

	// ErrDisbursementNotConfigured reports a gateway account with no
	// disbursement credentials.
	ErrDisbursementNotConfigured = errors.New("payout: this gateway account has no disbursement credentials")

	// ErrNotSupported reports a gateway that cannot disburse at all.
	ErrNotSupported = errors.New("payout: this gateway does not support disbursement")

	// ErrStranded reports an unresolved payout whose idempotency window has
	// closed. PayMux will not guess, and will not retry: retrying now would be
	// a new request rather than a question about the old one, and could send
	// the money a second time. Someone has to reconcile against the gateway.
	ErrStranded = errors.New("payout: outcome is unknown and can no longer be resolved automatically")
)
