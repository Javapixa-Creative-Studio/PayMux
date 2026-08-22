package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ProbeResult reports whether a configured account can actually reach its
// gateway (PRD §58).
type ProbeResult struct {
	OK           bool
	Message      string
	Capabilities Capabilities
	CheckedAt    time.Time
}

// probeOrderID is an order that will never exist. Asking for its status is the
// cheapest call that still proves the credentials work.
const probeOrderID = "pmx-connection-probe"

// Probe checks an account's credentials against the live gateway.
//
// It works by asking for a transaction that cannot exist. The gateway's answer
// separates the two cases an operator cares about: "no such transaction" means
// the credentials were accepted, while a rejection means they were not. A
// call that created something would be a poor probe — it would leave litter in
// the merchant's account every time someone pressed the button.
func Probe(ctx context.Context, g Gateway) ProbeResult {
	result := ProbeResult{CheckedAt: time.Now().UTC(), Capabilities: CapabilitiesFor(g)}

	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	_, err := g.GetTransaction(probeCtx, probeOrderID)

	switch {
	case err == nil:
		// Improbable, but it still means the gateway answered us.
		result.OK = true
		result.Message = "Credentials accepted."

	case errors.Is(err, ErrTransactionNotFound):
		// The expected answer: the gateway understood the request, which it
		// could only do with working credentials.
		result.OK = true
		result.Message = "Credentials accepted."

	default:
		result.Message = describeProbeFailure(err)
	}
	return result
}

// describeProbeFailure turns a failed probe into something an operator can act
// on, rather than a transport error they have to interpret.
func describeProbeFailure(err error) string {
	var gwErr *Error
	if !errors.As(err, &gwErr) {
		return "Could not reach the gateway: " + err.Error()
	}

	switch {
	case gwErr.StatusCode == http.StatusUnauthorized, gwErr.Code == "401":
		return "The gateway rejected these credentials. Check the server key, " +
			"and that it belongs to the environment selected here."
	case gwErr.StatusCode == http.StatusForbidden, gwErr.Code == "403":
		return "These credentials are not permitted to perform this operation."
	case gwErr.Retryable:
		return "The gateway is not responding: " + gwErr.Message
	default:
		return fmt.Sprintf("The gateway refused the request: %s", gwErr.Message)
	}
}
