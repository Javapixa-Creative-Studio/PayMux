// Package main is a worked example of integrating an application with PayMux.
//
// It is deliberately small and dependency-free: everything an application
// genuinely needs is one HTTP call to create a payment, and one signature
// check on the way back. Copy the two functions in this file into your own
// service and you have a complete integration.
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Client talks to a PayMux deployment on behalf of one application.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// NewClient builds a client. The API key identifies your application, so it
// belongs on your server and never in a browser.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// CreatePaymentRequest is what your application asks PayMux to open.
type CreatePaymentRequest struct {
	ApplicationOrderID string         `json:"application_order_id"`
	Amount             int64          `json:"amount"`
	Currency           string         `json:"currency,omitempty"`
	Customer           *Customer      `json:"customer,omitempty"`
	Items              []Item         `json:"items,omitempty"`
	CallbackURL        string         `json:"callback_url,omitempty"`
	Metadata           map[string]any `json:"metadata,omitempty"`
}

// Customer is the payer.
type Customer struct {
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Email     string `json:"email,omitempty"`
	Phone     string `json:"phone,omitempty"`
}

// Item is a line on the order. If you send items, they must sum to Amount.
type Item struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name"`
	Price    int64  `json:"price"`
	Quantity int    `json:"quantity"`
}

// Payment is PayMux's answer.
type Payment struct {
	ID                 string `json:"id"`
	ApplicationOrderID string `json:"application_order_id"`
	GatewayOrderID     string `json:"gateway_order_id"`
	Amount             int64  `json:"amount"`
	Currency           string `json:"currency"`
	Status             string `json:"status"`
	SnapToken          string `json:"snap_token"`
	RedirectURL        string `json:"redirect_url"`
}

// APIError is a failure PayMux reported. The request id is worth logging: it
// appears in PayMux's own logs for the same request.
type APIError struct {
	Status    int
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
	Fields    []struct {
		Field   string `json:"field"`
		Message string `json:"message"`
	} `json:"fields"`
}

// Error implements error.
func (e *APIError) Error() string {
	return fmt.Sprintf("paymux: %s: %s (request %s)", e.Code, e.Message, e.RequestID)
}

// CreatePayment opens a payment and returns the checkout details.
//
// idempotencyKey makes the call safe to retry: sending the same key returns
// the original payment rather than opening a second one at the gateway. Derive
// it from your own order, not from a random value, or a retry after a timeout
// will not match.
func (c *Client) CreatePayment(ctx context.Context, req CreatePaymentRequest, idempotencyKey string) (*Payment, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("paymux: encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/v1/payments", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("paymux: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		httpReq.Header.Set("Idempotency-Key", idempotencyKey)
	}

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("paymux: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("paymux: read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var envelope struct {
			Error APIError `json:"error"`
		}
		_ = json.Unmarshal(raw, &envelope)
		envelope.Error.Status = resp.StatusCode
		return nil, &envelope.Error
	}

	var payment Payment
	if err := json.Unmarshal(raw, &payment); err != nil {
		return nil, fmt.Errorf("paymux: decode response: %w", err)
	}
	return &payment, nil
}

// ---------------------------------------------------------------------------
// Verifying an inbound event
// ---------------------------------------------------------------------------

// Headers PayMux sets on every delivery.
const (
	HeaderEventID    = "PayMux-Event-Id"
	HeaderDeliveryID = "PayMux-Delivery-Id"
	HeaderTimestamp  = "PayMux-Timestamp"
	HeaderSignature  = "PayMux-Signature"
	HeaderEventType  = "PayMux-Event-Type"
)

// Errors VerifyWebhook can report.
var (
	ErrBadSignature = errors.New("paymux: signature does not match")
	ErrStale        = errors.New("paymux: delivery timestamp is outside the tolerance")
)

// Tolerance is how far a delivery's timestamp may be from your clock.
//
// This is what stops someone replaying a delivery they captured earlier: the
// signature stays valid forever, so the timestamp is what bounds it.
const Tolerance = 5 * time.Minute

// Event is the body PayMux delivers.
type Event struct {
	ID                 string         `json:"id"`
	Type               string         `json:"type"`
	Gateway            string         `json:"gateway"`
	ApplicationID      string         `json:"application_id"`
	PaymentID          string         `json:"payment_id"`
	ApplicationOrderID string         `json:"application_order_id"`
	GatewayOrderID     string         `json:"gateway_order_id"`
	Status             string         `json:"status"`
	GatewayStatus      string         `json:"gateway_status"`
	Amount             int64          `json:"amount"`
	Currency           string         `json:"currency"`
	Metadata           map[string]any `json:"metadata"`
	CreatedAt          time.Time      `json:"created_at"`
	GatewayData        map[string]any `json:"gateway_data"`
}

// VerifyWebhook authenticates a delivery and decodes it.
//
// Pass the *raw* body, exactly as received. Re-encoding it first — even
// through a JSON round trip that looks lossless — changes the bytes the
// signature covers and verification will fail.
func VerifyWebhook(secret string, header http.Header, body []byte) (*Event, error) {
	timestamp, err := strconv.ParseInt(header.Get(HeaderTimestamp), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("paymux: unreadable timestamp: %w", err)
	}
	if skew := time.Since(time.Unix(timestamp, 0)); skew > Tolerance || skew < -Tolerance {
		return nil, ErrStale
	}

	expected := Sign(secret, timestamp, header.Get(HeaderDeliveryID), body)
	if !hmac.Equal([]byte(expected), []byte(header.Get(HeaderSignature))) {
		return nil, ErrBadSignature
	}

	var event Event
	if err := json.Unmarshal(body, &event); err != nil {
		return nil, fmt.Errorf("paymux: decode event: %w", err)
	}
	return &event, nil
}

// Sign reproduces PayMux's signature over a delivery.
//
// The signed string is "<timestamp>.<delivery id>.<body>". Including the
// delivery id means a captured body cannot be replayed under a different
// delivery.
func Sign(secret string, timestamp int64, deliveryID string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.%s.", timestamp, deliveryID)
	mac.Write(body)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}
