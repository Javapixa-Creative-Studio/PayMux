package midtrans

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/crypto"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
)

// maxResponseBytes bounds how much of a gateway response PayMux will read, so
// a misbehaving upstream cannot exhaust memory.
const maxResponseBytes = 2 << 20 // 2 MiB

// RequestRecorder observes calls made to the gateway. The adapter depends on
// this narrow interface so it stays free of a metrics dependency.
type RequestRecorder interface {
	RecordGatewayRequest(gateway, operation string, duration time.Duration, err error)
}

// Client is the single place PayMux speaks HTTP to Midtrans (PRD §75).
//
// Nothing else in the codebase issues Midtrans requests: authentication,
// error decoding and payload limits are decided once, here.
type Client struct {
	SnapURL    string
	CoreURL    string
	ServerKey  crypto.Secret
	HTTPClient *http.Client

	// Metrics is optional; when nil, calls are simply not instrumented.
	Metrics RequestRecorder
}

// NewClient builds a Client for the given environment.
func NewClient(env gateway.Environment, serverKey crypto.Secret, httpClient *http.Client) *Client {
	snapURL, coreURL := snapProductionURL, coreProductionURL
	if env == gateway.Sandbox {
		snapURL, coreURL = snapSandboxURL, coreSandboxURL
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{SnapURL: snapURL, CoreURL: coreURL, ServerKey: serverKey, HTTPClient: httpClient}
}

// authorization builds the HTTP Basic credential Midtrans expects: the server
// key as the username with an empty password.
func (c *Client) authorization() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(c.ServerKey.Reveal()+":"))
}

// do performs a request and decodes the response into out.
//
// Midtrans reports application-level failures in a JSON body with its own
// status_code, sometimes alongside HTTP 200, so both the transport status and
// the body's status code are inspected.
func (c *Client) do(ctx context.Context, method, url string, body any, out any) (err error) {
	if c.Metrics != nil {
		start := time.Now()
		operation := operationName(method, url)
		defer func() { c.Metrics.RecordGatewayRequest(Name, operation, time.Since(start), err) }()
	}
	return c.exchange(ctx, method, url, body, out)
}

// exchange performs the request itself.
func (c *Client) exchange(ctx context.Context, method, url string, body any, out any) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("midtrans: encode request: %w", err)
		}
		payload = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, payload)
	if err != nil {
		return fmt.Errorf("midtrans: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", c.authorization())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		// Transport failures are worth retrying: the request may never have
		// reached Midtrans at all.
		return &gateway.Error{
			Gateway:   Name,
			Message:   "could not reach the payment gateway",
			Retryable: true,
			Err:       err,
		}
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
		_ = resp.Body.Close()
	}()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return &gateway.Error{
			Gateway:    Name,
			StatusCode: resp.StatusCode,
			Message:    "could not read the gateway response",
			Retryable:  true,
			Err:        err,
		}
	}

	if err := checkResponse(resp.StatusCode, raw); err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &gateway.Error{
			Gateway:    Name,
			StatusCode: resp.StatusCode,
			Message:    "the gateway returned an unreadable response",
			Err:        err,
		}
	}
	return nil
}

// checkResponse turns a Midtrans failure into a gateway.Error.
func checkResponse(httpStatus int, raw []byte) error {
	var envelope errorResponse
	// A body that does not parse is only a problem when the HTTP status is
	// also a failure; successful calls are decoded by the caller.
	_ = json.Unmarshal(raw, &envelope)

	code := envelope.StatusCode
	ok := httpStatus < 400 && (code == "" || strings.HasPrefix(code, "2"))
	if ok {
		return nil
	}

	message := envelope.StatusMessage
	if len(envelope.ErrorMessages) > 0 {
		message = strings.Join(envelope.ErrorMessages, "; ")
	} else if len(envelope.ValidationMessages) > 0 {
		message = strings.Join(envelope.ValidationMessages, "; ")
	}
	if message == "" {
		message = fmt.Sprintf("the gateway rejected the request (HTTP %d)", httpStatus)
	}

	status := httpStatus
	if status == 0 || status < 400 {
		// Midtrans signalled a failure in the body while returning 2xx: report
		// the body's own status code so callers see a failure.
		status = statusFromCode(code)
	}

	return &gateway.Error{
		Gateway:    Name,
		StatusCode: status,
		Code:       code,
		Message:    message,
		// Only server-side faults and rate limits are worth repeating; a
		// rejected request will be rejected again unchanged.
		Retryable: status >= 500 || status == http.StatusTooManyRequests,
	}
}

// statusFromCode maps a Midtrans status_code onto an HTTP status.
func statusFromCode(code string) int {
	switch {
	case code == "":
		return http.StatusBadGateway
	case strings.HasPrefix(code, "4"):
		var n int
		if _, err := fmt.Sscanf(code, "%d", &n); err == nil && n >= 400 && n < 600 {
			return n
		}
		return http.StatusBadRequest
	case strings.HasPrefix(code, "5"):
		return http.StatusBadGateway
	default:
		return http.StatusBadGateway
	}
}

// operationName labels a request by what it does rather than by its URL,
// which carries an order identifier and would explode the metric's
// cardinality.
func operationName(method, url string) string {
	switch {
	case strings.Contains(url, "/snap/v1/transactions"):
		return "snap.create"
	case strings.Contains(url, "/v1/subscriptions"):
		return "subscription." + strings.ToLower(method)
	case strings.HasSuffix(url, "/status"):
		return "transaction.status"
	case strings.HasSuffix(url, "/cancel"):
		return "transaction.cancel"
	case strings.HasSuffix(url, "/expire"):
		return "transaction.expire"
	case strings.HasSuffix(url, "/refund"):
		return "transaction.refund"
	default:
		return "other"
	}
}

// snapEndpoint builds a Snap URL.
func (c *Client) snapEndpoint(path string) string {
	return strings.TrimRight(c.SnapURL, "/") + path
}

// coreEndpoint builds a Core API URL.
func (c *Client) coreEndpoint(path string) string {
	return strings.TrimRight(c.CoreURL, "/") + path
}
