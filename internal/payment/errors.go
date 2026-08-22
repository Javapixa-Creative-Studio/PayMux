package payment

import "errors"

// Domain errors. Handlers map these onto the public error contract.
var (
	// ErrStaleTransition reports a state change that would have moved a
	// payment backwards, or that lost a race to a more advanced state. It is
	// an expected outcome of duplicate notifications, not a failure.
	ErrStaleTransition = errors.New("payment: state change is stale and was not applied")

	// ErrDuplicateOrderID reports a second payment for an order the
	// application has already created.
	ErrDuplicateOrderID = errors.New("payment: an order with this id already exists")

	// ErrIdempotencyConflict reports an idempotency key reused with a
	// different request body.
	ErrIdempotencyConflict = errors.New("payment: idempotency key was reused with a different request")

	// ErrIdempotencyInProgress reports a concurrent request carrying the same
	// idempotency key.
	ErrIdempotencyInProgress = errors.New("payment: a request with this idempotency key is still in progress")

	// ErrNotCancelable reports a cancel attempt on a payment past the point of
	// cancellation.
	ErrNotCancelable = errors.New("payment: this payment can no longer be canceled")

	// ErrNotRefundable reports a refund attempt on a payment that has not
	// settled or has nothing left to refund.
	ErrNotRefundable = errors.New("payment: this payment cannot be refunded")

	// ErrRefundExceedsBalance reports a refund larger than the remaining
	// refundable amount.
	ErrRefundExceedsBalance = errors.New("payment: refund amount exceeds the refundable balance")

	// ErrGatewayNotConfigured reports an application with no usable gateway
	// account.
	ErrGatewayNotConfigured = errors.New("payment: no gateway account is configured")

	// ErrKeyModeMismatch reports a test key used against a production gateway
	// account, or the reverse.
	ErrKeyModeMismatch = errors.New("payment: api key mode does not match the gateway environment")
)

// ValidationError reports a specific invalid field on a request.
type ValidationError struct {
	Field   string
	Message string
}

// Error implements error.
func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

func invalid(field, message string) error { return &ValidationError{Field: field, Message: message} }
