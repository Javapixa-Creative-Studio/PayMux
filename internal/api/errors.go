package api

import (
	"errors"
	"net/http"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/application"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/auth"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/httpx"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/payment"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/payout"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/storage"
)

// notFound describes how a missing record should be reported for a resource.
type notFound struct {
	code    httpx.Code
	message string
}

// Standard "missing resource" responses, so the same resource always reports
// the same code no matter which handler looked it up.
var (
	applicationMissing = notFound{httpx.CodeApplicationNotFound, "Application was not found."}
	paymentMissing     = notFound{httpx.CodePaymentNotFound, "Payment was not found."}
	refundMissing      = notFound{httpx.CodeRefundNotFound, "Refund was not found."}
	genericMissing     = notFound{httpx.CodeNotFound, "The requested resource was not found."}
	payoutMissing      = notFound{"payout_not_found", "Payout was not found."}
	beneficiaryMissing = notFound{"beneficiary_not_found", "Beneficiary was not found."}
)

// translate converts a domain error into PayMux's public error contract.
//
// Everything unrecognised becomes an opaque internal error, so a new error
// from a lower layer can never leak its text to a client by default (PRD §64).
func translate(err error, missing notFound) error {
	if err == nil {
		return nil
	}

	// An error a handler built deliberately already says what it means, so it
	// travels unchanged rather than being re-derived or flattened to a 500.
	var typed *httpx.Error
	if errors.As(err, &typed) {
		return typed
	}

	var validation *application.ValidationError
	if errors.As(err, &validation) {
		return httpx.ErrValidation("The request contains invalid values.").
			WithField(validation.Field, validation.Message).
			WithCause(err)
	}
	var paymentValidation *payment.ValidationError
	if errors.As(err, &paymentValidation) {
		return httpx.ErrValidation("The request contains invalid values.").
			WithField(paymentValidation.Field, paymentValidation.Message).
			WithCause(err)
	}

	switch {
	case storage.IsNotFound(err):
		return httpx.ErrNotFound(missing.code, missing.message).WithCause(err)

	case errors.Is(err, application.ErrSlugTaken):
		return httpx.ErrConflict(httpx.CodeConflict, "That slug is already used by another application.").
			WithCause(err)

	case errors.Is(err, application.ErrDisabled):
		return httpx.ErrForbidden("This application is disabled.").WithCause(err)

	case errors.Is(err, application.ErrKeyNotUsable):
		return httpx.ErrUnauthorized("The API key is invalid, revoked or expired.").WithCause(err)

	case errors.Is(err, application.ErrDestinationIn):
		return httpx.ErrValidation("The webhook destination URL is not permitted.").
			WithField("url", destinationReason(err)).
			WithCause(err)

	case errors.Is(err, auth.ErrInvalidCredentials):
		return httpx.ErrUnauthorized("Email or password is incorrect.").WithCause(err)

	case errors.Is(err, auth.ErrSessionInvalid):
		return httpx.ErrUnauthorized("Your session has expired. Please sign in again.").WithCause(err)

	case errors.Is(err, auth.ErrEmailTaken):
		return httpx.ErrConflict(httpx.CodeConflict, "That email address is already registered.").WithCause(err)

	case errors.Is(err, auth.ErrWeakPassword):
		return httpx.ErrValidation("The password does not meet the minimum requirements.").
			WithField("password", err.Error()).
			WithCause(err)

	case errors.Is(err, payment.ErrDuplicateOrderID):
		return httpx.ErrConflict(httpx.CodeDuplicateOrderID,
			"A payment already exists for this application_order_id.").WithCause(err)

	case errors.Is(err, payment.ErrIdempotencyConflict):
		return httpx.ErrConflict(httpx.CodeIdempotencyConflict,
			"This idempotency key was already used for a different request.").WithCause(err)

	case errors.Is(err, payment.ErrIdempotencyInProgress):
		return httpx.ErrConflict(httpx.CodeConflict,
			"A request with this idempotency key is still in progress. Retry shortly.").WithCause(err)

	case errors.Is(err, payment.ErrStaleTransition):
		return httpx.ErrConflict(httpx.CodeConflict,
			"The payment has already moved past this state.").WithCause(err)

	case errors.Is(err, payment.ErrNotCancelable):
		return httpx.NewError(http.StatusUnprocessableEntity, httpx.CodeInvalidState,
			"This payment can no longer be canceled.").WithCause(err)

	// Payouts. Each of these is a refusal a caller can act on, and an opaque
	// 500 in their place would tell somebody trying to move money only that
	// something went wrong.
	case errors.Is(err, payout.ErrPayoutsDisabled):
		return httpx.ErrForbidden(
			"This application is not permitted to disburse. Turn payouts on for it first.").WithCause(err)

	case errors.Is(err, payout.ErrExceedsMaxAmount):
		return httpx.ErrValidation("The amount exceeds this application's per-payout limit.").
			WithField("amount", "exceeds the per-payout limit").WithCause(err)

	case errors.Is(err, payout.ErrExceedsDailyLimit):
		return httpx.ErrValidation("The amount exceeds this application's daily payout limit.").
			WithField("amount", "exceeds the daily limit").WithCause(err)

	case errors.Is(err, payout.ErrDuplicatePayoutID):
		return httpx.ErrConflict(httpx.CodeConflict,
			"A payout with this application_payout_id already exists.").WithCause(err)

	case errors.Is(err, payout.ErrBeneficiaryNotFound):
		return httpx.ErrNotFound("beneficiary_not_found", "No such beneficiary.").WithCause(err)

	case errors.Is(err, payout.ErrBeneficiaryDisabled):
		return httpx.NewError(http.StatusUnprocessableEntity, httpx.CodeInvalidState,
			"This beneficiary is disabled and cannot receive payouts.").WithCause(err)

	case errors.Is(err, payout.ErrDuplicateAlias):
		return httpx.ErrConflict(httpx.CodeConflict,
			"A beneficiary with this alias already exists.").WithCause(err)

	case errors.Is(err, payout.ErrPayoutNotFound):
		return httpx.ErrNotFound("payout_not_found", "No such payout.").WithCause(err)

	case errors.Is(err, payout.ErrNotPending):
		return httpx.NewError(http.StatusUnprocessableEntity, httpx.CodeInvalidState,
			"This payout is no longer awaiting approval.").WithCause(err)

	case errors.Is(err, payout.ErrSelfApproval):
		return httpx.ErrForbidden(
			"A payout cannot be approved by the person who requested it.").WithCause(err)

	case errors.Is(err, payout.ErrDisbursementNotConfigured), errors.Is(err, payout.ErrNotSupported):
		return httpx.NewError(http.StatusPreconditionFailed, "disbursement_not_configured",
			"This gateway account has no disbursement credentials.").WithCause(err)

	case errors.Is(err, payout.ErrStaleTransition):
		return httpx.ErrConflict(httpx.CodeConflict,
			"This payout changed while the request was in flight. Read it again.").WithCause(err)

	// An unknown outcome is reported as such rather than as a success or a
	// failure. The caller must not retry with a new reference: PayMux is
	// already holding the original idempotency key and will resolve it.
	case errors.Is(err, gateway.ErrOutcomeUnknown), errors.Is(err, payout.ErrStranded):
		return httpx.NewError(http.StatusAccepted, "outcome_unknown",
			"The payout was sent but its outcome is not yet known. "+
				"PayMux is resolving it; do not submit it again.").WithCause(err)

	case errors.Is(err, payment.ErrNotRefundable):
		return httpx.NewError(http.StatusUnprocessableEntity, httpx.CodeInvalidState,
			"This payment cannot be refunded.").WithCause(err)

	case errors.Is(err, payment.ErrRefundExceedsBalance):
		return httpx.ErrValidation("The refund amount exceeds the refundable balance.").
			WithField("amount", "exceeds the refundable balance").
			WithCause(err)

	case errors.Is(err, payment.ErrGatewayNotConfigured):
		return httpx.NewError(http.StatusServiceUnavailable, httpx.CodeGatewayNotConfigured,
			"No gateway account is configured for this application.").WithCause(err)

	case errors.Is(err, payment.ErrKeyModeMismatch):
		// A test key against a production account, or the reverse. Reported
		// plainly because the fix is to use the right credential.
		return httpx.ErrForbidden(
			"This API key's mode does not match the configured gateway environment.").WithCause(err)

	case errors.Is(err, gateway.ErrNotSupported):
		return httpx.NewError(http.StatusBadRequest, httpx.CodeNotSupported,
			"This gateway does not support that operation.").WithCause(err)

	case errors.Is(err, gateway.ErrTransactionNotFound):
		return httpx.ErrNotFound(httpx.CodePaymentNotFound,
			"The gateway does not have a transaction for this payment.").WithCause(err)
	}

	// A gateway rejected the request: report it as a gateway error, keeping
	// the gateway's own message, which adapters keep free of credentials.
	var gwErr *gateway.Error
	if errors.As(err, &gwErr) {
		status := http.StatusBadGateway
		if gwErr.StatusCode >= 400 && gwErr.StatusCode < 500 {
			// The gateway rejected what PayMux was asked to send, so this is
			// the caller's problem to fix, not a transient upstream fault.
			status = http.StatusUnprocessableEntity
		}
		return httpx.NewError(status, httpx.CodeGatewayError, gwErr.Message).WithCause(err)
	}

	return httpx.ErrInternal(err)
}

// destinationReason extracts the guard's explanation from a destination error
// without exposing the wrapped error chain.
func destinationReason(err error) string {
	msg := err.Error()
	const marker = ": "
	if i := indexAfterPrefix(msg, application.ErrDestinationIn.Error()+marker); i > 0 {
		return msg[i:]
	}
	return "is not permitted"
}

func indexAfterPrefix(s, prefix string) int {
	if len(s) > len(prefix) && s[:len(prefix)] == prefix {
		return len(prefix)
	}
	return -1
}

// fail translates and writes an error response.
func fail(w http.ResponseWriter, r *http.Request, err error, missing notFound) {
	httpx.Fail(w, r, translate(err, missing))
}
