// Package httpx holds PayMux's shared HTTP plumbing: the public error
// contract, JSON encoding helpers and middleware.
package httpx

import (
	"errors"
	"fmt"
	"net/http"
)

// Code is a stable, machine-readable error identifier. Codes are part of the
// public API contract and must not be renamed casually.
type Code string

const (
	CodeInvalidRequest       Code = "INVALID_REQUEST"
	CodeValidationFailed     Code = "VALIDATION_FAILED"
	CodeUnauthorized         Code = "UNAUTHORIZED"
	CodeForbidden            Code = "FORBIDDEN"
	CodeNotFound             Code = "NOT_FOUND"
	CodePaymentNotFound      Code = "PAYMENT_NOT_FOUND"
	CodeApplicationNotFound  Code = "APPLICATION_NOT_FOUND"
	CodeRefundNotFound       Code = "REFUND_NOT_FOUND"
	CodeConflict             Code = "CONFLICT"
	CodeIdempotencyConflict  Code = "IDEMPOTENCY_KEY_REUSED"
	CodeDuplicateOrderID     Code = "DUPLICATE_ORDER_ID"
	CodeUnsupportedGateway   Code = "UNSUPPORTED_GATEWAY"
	CodeGatewayNotConfigured Code = "GATEWAY_NOT_CONFIGURED"
	CodeGatewayError         Code = "GATEWAY_ERROR"
	CodeInvalidState         Code = "INVALID_STATE"
	CodeNotSupported         Code = "NOT_SUPPORTED"
	CodeRateLimited          Code = "RATE_LIMITED"
	CodePayloadTooLarge      Code = "PAYLOAD_TOO_LARGE"
	CodeInternal             Code = "INTERNAL_ERROR"
)

// FieldError describes one invalid request field.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error is an API error that is safe to render to a client. Internal detail
// lives in Cause, which is logged but never serialised.
type Error struct {
	Status  int
	Code    Code
	Message string
	Fields  []FieldError
	Cause   error
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap exposes the internal cause to errors.Is/As without exposing it to
// clients.
func (e *Error) Unwrap() error { return e.Cause }

// WithCause attaches an internal cause for logging.
func (e *Error) WithCause(err error) *Error {
	clone := *e
	clone.Cause = err
	return &clone
}

// WithField adds a field-level validation detail.
func (e *Error) WithField(field, message string) *Error {
	clone := *e
	clone.Fields = append(append([]FieldError(nil), e.Fields...), FieldError{Field: field, Message: message})
	return &clone
}

// NewError builds an API error.
func NewError(status int, code Code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// ErrInvalidRequest reports a malformed request. It heads the set of
// constructors for the errors handlers raise most often.
func ErrInvalidRequest(message string) *Error {
	return NewError(http.StatusBadRequest, CodeInvalidRequest, message)
}

func ErrValidation(message string) *Error {
	return NewError(http.StatusUnprocessableEntity, CodeValidationFailed, message)
}

func ErrUnauthorized(message string) *Error {
	return NewError(http.StatusUnauthorized, CodeUnauthorized, message)
}

func ErrForbidden(message string) *Error {
	return NewError(http.StatusForbidden, CodeForbidden, message)
}

func ErrNotFound(code Code, message string) *Error {
	return NewError(http.StatusNotFound, code, message)
}

func ErrConflict(code Code, message string) *Error {
	return NewError(http.StatusConflict, code, message)
}

func ErrInternal(cause error) *Error {
	return &Error{
		Status:  http.StatusInternalServerError,
		Code:    CodeInternal,
		Message: "An internal error occurred.",
		Cause:   cause,
	}
}

// AsError extracts an *Error from err, converting anything unrecognised into
// an opaque internal error. This is the single place where a non-API error
// becomes a client-visible response, which is what keeps database and gateway
// internals from leaking (PRD §64).
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return ErrInternal(err)
}

// errorBody is the wire format for an error response.
type errorBody struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code      Code         `json:"code"`
	Message   string       `json:"message"`
	Fields    []FieldError `json:"fields,omitempty"`
	RequestID string       `json:"request_id,omitempty"`
}
