package gateway

import "testing"

func TestStatusTransitionsMoveForwardOnly(t *testing.T) {
	cases := []struct {
		from, to Status
		want     bool
	}{
		{StatusPending, StatusPaid, true},
		{StatusPending, StatusAuthorized, true},
		{StatusPending, StatusExpired, true},
		{StatusPending, StatusCanceled, true},
		{StatusPending, StatusFailed, true},
		{StatusAuthorized, StatusPaid, true},

		// A delayed notification must never drag a payment backwards (PRD §40).
		{StatusPaid, StatusPending, false},
		{StatusPaid, StatusAuthorized, false},
		{StatusPaid, StatusExpired, false},
		{StatusPaid, StatusCanceled, false},
		{StatusPaid, StatusFailed, false},
		{StatusAuthorized, StatusPending, false},
		{StatusExpired, StatusPending, false},
		{StatusCanceled, StatusPaid, false},
		{StatusFailed, StatusPaid, false},

		// Refunds are the one progression out of a settled payment.
		{StatusPaid, StatusRefunded, true},
		{StatusPaid, StatusPartiallyRefunded, true},
		{StatusPartiallyRefunded, StatusRefunded, true},
		{StatusRefunded, StatusPaid, false},

		// A repeated notification for the same state is not a transition.
		{StatusPaid, StatusPaid, false},
		{StatusPending, StatusPending, false},
	}
	for _, tc := range cases {
		if got := tc.from.CanTransitionTo(tc.to); got != tc.want {
			t.Errorf("%s -> %s = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestCanTransitionRejectsUnknownStatuses(t *testing.T) {
	if Status("MYSTERY").CanTransitionTo(StatusPaid) {
		t.Error("unknown source status was allowed to transition")
	}
	if StatusPending.CanTransitionTo(Status("MYSTERY")) {
		t.Error("transition to an unknown status was allowed")
	}
}

func TestTerminalAndSettled(t *testing.T) {
	terminal := map[Status]bool{
		StatusPending: false, StatusAuthorized: false, StatusPaid: false,
		StatusPartiallyRefunded: false,
		StatusFailed:            true, StatusCanceled: true, StatusExpired: true, StatusRefunded: true,
	}
	for s, want := range terminal {
		if got := s.Terminal(); got != want {
			t.Errorf("%s.Terminal() = %v, want %v", s, got, want)
		}
	}
	settled := map[Status]bool{
		StatusPaid: true, StatusRefunded: true, StatusPartiallyRefunded: true,
		StatusPending: false, StatusAuthorized: false, StatusFailed: false,
		StatusCanceled: false, StatusExpired: false,
	}
	for s, want := range settled {
		if got := s.Settled(); got != want {
			t.Errorf("%s.Settled() = %v, want %v", s, got, want)
		}
	}
}

func TestParseStatus(t *testing.T) {
	got, err := ParseStatus("PAID")
	if err != nil || got != StatusPaid {
		t.Fatalf("ParseStatus(PAID) = %q, %v", got, err)
	}
	if _, err := ParseStatus("paid"); err == nil {
		t.Error("ParseStatus accepted a lowercase status")
	}
	if _, err := ParseStatus("SETTLEMENT"); err == nil {
		t.Error("ParseStatus accepted a gateway-specific status")
	}
}

func TestEnvironmentValidity(t *testing.T) {
	if !Sandbox.Valid() || !Production.Valid() {
		t.Error("known environments reported invalid")
	}
	if Environment("staging").Valid() {
		t.Error("unknown environment reported valid")
	}
}

func TestGatewayErrorMessage(t *testing.T) {
	err := &Error{Gateway: "midtrans", Code: "401", Message: "invalid credentials"}
	if got := err.Error(); got != "midtrans: 401: invalid credentials" {
		t.Errorf("Error() = %q", got)
	}
	if IsRetryable(err) {
		t.Error("non-retryable error reported as retryable")
	}
	if !IsRetryable(&Error{Gateway: "midtrans", Retryable: true}) {
		t.Error("retryable error reported as non-retryable")
	}
}
