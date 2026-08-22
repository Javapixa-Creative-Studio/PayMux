package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// stubGateway answers GetTransaction with whatever a test needs.
type stubGateway struct {
	Gateway
	err error
}

func (s stubGateway) GetTransaction(context.Context, string) (*Transaction, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &Transaction{OrderID: probeOrderID}, nil
}

func TestProbeTreatsNotFoundAsSuccess(t *testing.T) {
	// This is the whole idea of the probe: the gateway could only tell us the
	// transaction does not exist if it accepted our credentials first.
	result := Probe(context.Background(), stubGateway{err: ErrTransactionNotFound})

	if !result.OK {
		t.Fatalf("a not-found answer was treated as a failure: %s", result.Message)
	}
	if result.CheckedAt.IsZero() {
		t.Error("the probe did not record when it ran")
	}
}

func TestProbeReportsRejectedCredentials(t *testing.T) {
	result := Probe(context.Background(), stubGateway{err: &Error{
		Gateway:    "midtrans",
		StatusCode: http.StatusUnauthorized,
		Code:       "401",
		Message:    "Access denied due to unauthorized transaction",
	}})

	if result.OK {
		t.Fatal("rejected credentials were reported as working")
	}
	// The message has to tell an operator what to change, not just restate
	// the status code.
	if !strings.Contains(result.Message, "server key") {
		t.Errorf("message does not say what to check: %q", result.Message)
	}
	if !strings.Contains(result.Message, "environment") {
		t.Errorf("message does not mention the environment, the other common cause: %q", result.Message)
	}
}

func TestProbeReportsAnUnreachableGateway(t *testing.T) {
	result := Probe(context.Background(), stubGateway{err: &Error{
		Gateway:   "midtrans",
		Message:   "could not reach the payment gateway",
		Retryable: true,
	}})

	if result.OK {
		t.Fatal("an unreachable gateway was reported as working")
	}
	if !strings.Contains(result.Message, "not responding") {
		t.Errorf("message = %q, want it to distinguish unreachable from rejected", result.Message)
	}
}

func TestProbeReportsATransportFailurePlainly(t *testing.T) {
	result := Probe(context.Background(), stubGateway{err: errors.New("dial tcp: lookup failed")})

	if result.OK {
		t.Fatal("a transport failure was reported as working")
	}
	if !strings.Contains(result.Message, "Could not reach") {
		t.Errorf("message = %q", result.Message)
	}
}

func TestProbeReportsCapabilities(t *testing.T) {
	// The dashboard shows what an account can do; the probe is when that gets
	// refreshed, so it has to carry the answer.
	result := Probe(context.Background(), stubGateway{err: ErrTransactionNotFound})
	if !result.Capabilities.Cancel || !result.Capabilities.Expire {
		t.Errorf("capabilities were not reported: %+v", result.Capabilities)
	}
}
