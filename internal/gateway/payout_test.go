package gateway_test

import (
	"slices"
	"testing"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
)

// The payout state machine is the last thing between a bug and somebody else's
// money, so these assert the edges that must not exist as carefully as the
// ones that must.

func TestPayoutCannotMoveBackwards(t *testing.T) {
	backwards := []struct{ from, to gateway.PayoutStatus }{
		{gateway.PayoutSubmitted, gateway.PayoutApproved},
		{gateway.PayoutSubmitted, gateway.PayoutRequested},
		{gateway.PayoutCompleted, gateway.PayoutSubmitted},
		{gateway.PayoutApproved, gateway.PayoutRequested},
	}
	for _, c := range backwards {
		if c.from.CanTransitionTo(c.to) {
			t.Errorf("%s -> %s should not be allowed", c.from, c.to)
		}
	}
}

func TestSubmittedPayoutCannotBeRejected(t *testing.T) {
	// Rejection is a decision about a request. Once the gateway has the money
	// in flight there is no request left to refuse, and pretending otherwise
	// would leave PayMux reporting REJECTED for a transfer that completed.
	for _, from := range []gateway.PayoutStatus{
		gateway.PayoutSubmitted,
		gateway.PayoutUnresolved,
		gateway.PayoutCompleted,
		gateway.PayoutFailed,
	} {
		if from.CanTransitionTo(gateway.PayoutRejected) {
			t.Errorf("%s -> REJECTED should not be allowed", from)
		}
	}
}

func TestApprovedPayoutCannotBeRejected(t *testing.T) {
	// The approval already happened. Reversing it is a different operation
	// with a different name, not a second opinion on the same one.
	if gateway.PayoutApproved.CanTransitionTo(gateway.PayoutRejected) {
		t.Fatal("an approved payout must not be rejectable")
	}
}

func TestTerminalPayoutStatusesAreFinal(t *testing.T) {
	for _, s := range []gateway.PayoutStatus{
		gateway.PayoutCompleted,
		gateway.PayoutFailed,
		gateway.PayoutRejected,
	} {
		if !s.Terminal() {
			t.Errorf("%s should be terminal", s)
		}
		for _, next := range allPayoutStatuses() {
			if s.CanTransitionTo(next) {
				t.Errorf("%s should not transition to %s", s, next)
			}
		}
	}
}

func TestUnresolvedIsNotTerminal(t *testing.T) {
	// "We don't know" has to be a state the system can leave, or a single
	// dropped connection would strand a payout forever.
	if gateway.PayoutUnresolved.Terminal() {
		t.Fatal("UNRESOLVED must not be terminal")
	}
	for _, next := range []gateway.PayoutStatus{
		gateway.PayoutSubmitted,
		gateway.PayoutCompleted,
		gateway.PayoutFailed,
	} {
		if !gateway.PayoutUnresolved.CanTransitionTo(next) {
			t.Errorf("UNRESOLVED should be able to resolve to %s", next)
		}
	}
}

func TestSubmittedCountsAsSettled(t *testing.T) {
	// Once the gateway has it PayMux cannot recall it, so anything reasoning
	// about how much has left the balance must count it as gone.
	if !gateway.PayoutSubmitted.Settled() {
		t.Fatal("SUBMITTED must count as settled")
	}
	for _, s := range []gateway.PayoutStatus{
		gateway.PayoutRequested,
		gateway.PayoutApproved,
		gateway.PayoutRejected,
		gateway.PayoutFailed,
	} {
		if s.Settled() {
			t.Errorf("%s must not count as settled", s)
		}
	}
}

func TestInFlightIsExactlyWhatNeedsPolling(t *testing.T) {
	want := []gateway.PayoutStatus{
		gateway.PayoutApproved,
		gateway.PayoutSubmitted,
		gateway.PayoutUnresolved,
	}
	for _, s := range allPayoutStatuses() {
		expected := slices.Contains(want, s)
		if s.InFlight() != expected {
			t.Errorf("%s InFlight() = %v, want %v", s, s.InFlight(), expected)
		}
	}
}

func TestPayoutPredecessorsMatchTheTransitionTable(t *testing.T) {
	// The repository puts PayoutPredecessorsOf straight into a WHERE clause,
	// so if it and CanTransitionTo ever disagree the database would allow a
	// transition the domain forbids.
	for _, to := range allPayoutStatuses() {
		got := gateway.PayoutPredecessorsOf(to)
		for _, from := range allPayoutStatuses() {
			listed := slices.Contains(got, string(from))
			if listed != from.CanTransitionTo(to) {
				t.Errorf("%s -> %s: predecessors say %v, CanTransitionTo says %v",
					from, to, listed, from.CanTransitionTo(to))
			}
		}
	}
}

func TestNoStatusTransitionsToItself(t *testing.T) {
	for _, s := range allPayoutStatuses() {
		if s.CanTransitionTo(s) {
			t.Errorf("%s should not transition to itself", s)
		}
	}
}

func TestUnknownPayoutStatusIsRejected(t *testing.T) {
	if _, err := gateway.ParsePayoutStatus("SENT"); err == nil {
		t.Fatal("an unknown status must not parse")
	}
	if gateway.PayoutStatus("SENT").Valid() {
		t.Fatal("an unknown status must not be valid")
	}
	// An unknown status must not look terminal, or a typo would silently
	// freeze a payout.
	if gateway.PayoutStatus("SENT").Terminal() {
		t.Fatal("an unknown status must not report as terminal")
	}
}

func allPayoutStatuses() []gateway.PayoutStatus {
	return []gateway.PayoutStatus{
		gateway.PayoutRequested,
		gateway.PayoutApproved,
		gateway.PayoutSubmitted,
		gateway.PayoutUnresolved,
		gateway.PayoutCompleted,
		gateway.PayoutFailed,
		gateway.PayoutRejected,
	}
}
