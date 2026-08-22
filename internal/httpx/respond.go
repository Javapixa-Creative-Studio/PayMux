package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/logging"
)

// JSON writes v as a JSON response with the given status.
func JSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	buf, err := json.Marshal(v)
	if err != nil {
		logging.FromContext(r.Context()).Error("failed to encode response", "error", err)
		writeRaw(w, http.StatusInternalServerError,
			[]byte(`{"error":{"code":"INTERNAL_ERROR","message":"An internal error occurred."}}`))
		return
	}
	writeRaw(w, status, buf)
}

// NoContent writes an empty 204 response.
func NoContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

// Fail renders err using PayMux's error contract, logging the internal cause.
//
// Client errors are logged at debug and server errors at error, so a noisy
// caller cannot flood the operator's logs.
func Fail(w http.ResponseWriter, r *http.Request, err error) {
	apiErr := AsError(err)
	requestID := RequestIDFromContext(r.Context())

	logger := logging.FromContext(r.Context()).With(
		"error_code", string(apiErr.Code),
		"status", apiErr.Status,
	)
	if apiErr.Status >= http.StatusInternalServerError {
		logger.Error("request failed", "error", apiErr.Error())
	} else {
		logger.Debug("request rejected", "error", apiErr.Error())
	}

	JSON(w, r, apiErr.Status, errorBody{Error: errorPayload{
		Code:      apiErr.Code,
		Message:   apiErr.Message,
		Fields:    apiErr.Fields,
		RequestID: requestID,
	}})
}

func writeRaw(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// DecodeJSON reads a JSON request body into v, rejecting unknown fields and
// trailing content so typos in a request surface immediately rather than
// being silently ignored.
func DecodeJSON(r *http.Request, v any) error {
	if ct := r.Header.Get("Content-Type"); ct != "" && !isJSONContentType(ct) {
		return NewError(http.StatusUnsupportedMediaType, CodeInvalidRequest,
			"Content-Type must be application/json.")
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return decodeError(err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidRequest("Request body must contain a single JSON object.")
	}
	return nil
}

func decodeError(err error) error {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return NewError(http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
			"Request body is too large.")
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		field := typeErr.Field
		if field == "" {
			field = "body"
		}
		return ErrInvalidRequest(fmt.Sprintf("Field %q must be of type %s.", field, typeErr.Type.String()))
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return ErrInvalidRequest(fmt.Sprintf("Request body contains malformed JSON at byte %d.", syntaxErr.Offset))
	}
	if errors.Is(err, io.EOF) {
		return ErrInvalidRequest("Request body must not be empty.")
	}
	// json.Decoder reports unknown fields only as a plain error string.
	return ErrInvalidRequest("Request body is not valid: " + sanitizeDecodeMessage(err))
}

// sanitizeDecodeMessage keeps decoder messages useful without echoing back
// arbitrary attacker-controlled content.
func sanitizeDecodeMessage(err error) string {
	const unknownPrefix = "json: unknown field "
	msg := err.Error()
	if len(msg) > len(unknownPrefix) && msg[:len(unknownPrefix)] == unknownPrefix {
		return "unknown field " + truncate(msg[len(unknownPrefix):], 64)
	}
	return "malformed request body"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func isJSONContentType(ct string) bool {
	for i := 0; i < len(ct); i++ {
		if ct[i] == ';' {
			ct = ct[:i]
			break
		}
	}
	switch trimSpace(ct) {
	case "application/json", "text/json", "application/vnd.api+json":
		return true
	}
	return false
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
