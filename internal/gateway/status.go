// Package gateway defines the adapter contract every payment gateway
// implements, plus the normalized vocabulary PayMux speaks internally.
//
// Gateway-specific request and response shapes never leave an adapter: an
// adapter translates them into the types declared here, so the payment domain
// works the same way whichever gateway is behind it (PRD §11, §91 rule 6).
package gateway

import "fmt"

// Status is PayMux's normalized payment status (PRD §26). It is deliberately
// smaller than any single gateway's status set, and the gateway's own value is
// always retained alongside it.
type Status string

const (
	StatusPending           Status = "PENDING"
	StatusAuthorized        Status = "AUTHORIZED"
	StatusPaid              Status = "PAID"
	StatusFailed            Status = "FAILED"
	StatusCanceled          Status = "CANCELED"
	StatusExpired           Status = "EXPIRED"
	StatusRefunded          Status = "REFUNDED"
	StatusPartiallyRefunded Status = "PARTIALLY_REFUNDED"
)

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	_, ok := statusRanks[s]
	return ok
}

// String implements fmt.Stringer.
func (s Status) String() string { return string(s) }

// Terminal reports whether no further transition is expected from s.
//
// A refunded payment is terminal for the purposes of the payment lifecycle;
// further refund activity is tracked on the refund entity itself.
func (s Status) Terminal() bool {
	switch s {
	case StatusFailed, StatusCanceled, StatusExpired, StatusRefunded:
		return true
	default:
		return false
	}
}

// Settled reports whether money has been captured for the payment.
func (s Status) Settled() bool {
	switch s {
	case StatusPaid, StatusRefunded, StatusPartiallyRefunded:
		return true
	default:
		return false
	}
}

// statusRanks orders statuses by how far through the lifecycle they are.
//
// Rank exists to stop a delayed or reordered notification from downgrading a
// payment: a "pending" that arrives after a "settlement" must not revert a
// paid payment (PRD §40). Ranks are compared, never subtracted, so the exact
// spacing carries no meaning.
var statusRanks = map[Status]int{
	StatusPending:           10,
	StatusAuthorized:        20,
	StatusFailed:            30,
	StatusCanceled:          30,
	StatusExpired:           30,
	StatusPaid:              40,
	StatusPartiallyRefunded: 50,
	StatusRefunded:          60,
}

// Rank returns the lifecycle rank of s.
func (s Status) Rank() int { return statusRanks[s] }

// CanTransitionTo reports whether a payment in status s may move to next.
//
// The rule is: a payment never moves backwards, and never leaves a terminal
// status: except that a settled payment may still become refunded, which is
// the one legitimate transition out of PAID.
func (s Status) CanTransitionTo(next Status) bool {
	if !s.Valid() || !next.Valid() {
		return false
	}
	if s == next {
		return false // no-op transitions are not state changes
	}
	// Refunds are the only progression allowed out of a settled payment.
	if s.Settled() {
		return next == StatusPartiallyRefunded || next == StatusRefunded
	}
	if s.Terminal() {
		return false
	}
	return next.Rank() > s.Rank()
}

// ParseStatus converts a string to a Status, rejecting unknown values.
func ParseStatus(v string) (Status, error) {
	s := Status(v)
	if !s.Valid() {
		return "", fmt.Errorf("gateway: unknown payment status %q", v)
	}
	return s, nil
}

// RefundStatus is the normalized state of a refund.
type RefundStatus string

const (
	RefundPending   RefundStatus = "PENDING"
	RefundSucceeded RefundStatus = "SUCCEEDED"
	RefundFailed    RefundStatus = "FAILED"
)

// SubscriptionStatus is the normalized state of a subscription.
type SubscriptionStatus string

const (
	SubscriptionActive   SubscriptionStatus = "ACTIVE"
	SubscriptionInactive SubscriptionStatus = "INACTIVE"
	SubscriptionCanceled SubscriptionStatus = "CANCELED"
)

// AllStatuses lists every normalized status, ordered by lifecycle rank.
func AllStatuses() []Status {
	return []Status{
		StatusPending, StatusAuthorized, StatusFailed, StatusCanceled,
		StatusExpired, StatusPaid, StatusPartiallyRefunded, StatusRefunded,
	}
}

// PredecessorsOf returns every status a payment may legitimately be in for a
// move to next to be allowed.
//
// It exists so the transition rule can be enforced inside a single SQL
// statement: with concurrent notifications for one payment, only the database
// can decide which update wins, and it can only do that if the permitted
// starting states are handed to it (PRD §40).
func PredecessorsOf(next Status) []string {
	var out []string
	for _, s := range AllStatuses() {
		if s.CanTransitionTo(next) {
			out = append(out, string(s))
		}
	}
	return out
}
