package api

import (
	"errors"
	"net/http"

	"github.com/anggapixa/paymux/internal/application"
	"github.com/anggapixa/paymux/internal/auth"
	"github.com/anggapixa/paymux/internal/gateway"
	"github.com/anggapixa/paymux/internal/httpx"
	"github.com/anggapixa/paymux/internal/payment"
	"github.com/anggapixa/paymux/internal/storage"
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
)

// translate converts a domain error into PayMux's public error contract.
//
// Everything unrecognised becomes an opaque internal error, so a new error
// from a lower layer can never leak its text to a client by default (PRD §64).
func translate(err error, missing notFound) error {
	if err == nil {
		return nil
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
