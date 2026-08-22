package delivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/anggapixa/paymux/internal/crypto"
	"github.com/anggapixa/paymux/internal/netsafe"
)

// Outbound signature headers (PRD §44).
const (
	HeaderEventID    = "PayMux-Event-Id"
	HeaderDeliveryID = "PayMux-Delivery-Id"
	HeaderTimestamp  = "PayMux-Timestamp"
	HeaderSignature  = "PayMux-Signature"
	HeaderEventType  = "PayMux-Event-Type"
	HeaderAttempt    = "PayMux-Attempt"
)

// maxResponseSnippet is how much of a destination's response body is kept for
// debugging. Applications sometimes return large error pages, and PayMux has
// no reason to store them.
const maxResponseSnippet = 2000

// Sender performs the HTTP call for one delivery.
type Sender struct {
	client    *http.Client
	userAgent string
}

// NewSender builds a Sender whose transport refuses restricted addresses.
//
// The SSRF guard is installed in the dialer rather than checked beforehand:
// the check then runs against the address the connection actually reaches, so
// a hostname that changes its answer between validation and delivery cannot
// slip past it (PRD §73).
func NewSender(guard *netsafe.Guard, timeout time.Duration, userAgent string) *Sender {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   guard.DialControl(),
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: 1 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   4,
		ForceAttemptHTTP2:     true,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		// Redirects are not followed: a destination that redirects could send
		// PayMux — and the signed event with it — somewhere unvetted.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	if userAgent == "" {
		userAgent = "PayMux-Webhook/1.0"
	}
	return &Sender{client: client, userAgent: userAgent}
}

// Request is everything needed to make one delivery attempt.
type Request struct {
	DeliveryID    string
	EventID       string
	EventType     string
	URL           string
	Body          []byte
	Secret        crypto.Secret
	AttemptNumber int
}

// Result is the outcome of an attempt.
type Result struct {
	StatusCode   int
	Duration     time.Duration
	ResponseBody string
	Err          error
}

// Delivered reports whether the destination accepted the event.
func (r Result) Delivered() bool { return r.Err == nil && Succeeded(r.StatusCode) }

// Retryable reports whether another attempt is worthwhile.
func (r Result) Retryable() bool {
	if r.Err != nil {
		// A blocked address will be blocked again; anything else is likely
		// transient.
		return !errors.Is(r.Err, netsafe.ErrBlockedAddress)
	}
	return ShouldRetry(r.StatusCode)
}

// Send performs one delivery attempt.
func (s *Sender) Send(ctx context.Context, req Request) Result {
	timestamp := time.Now().Unix()
	signature := crypto.SignWebhook(req.Secret, timestamp, req.DeliveryID, req.Body)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		return Result{Err: fmt.Errorf("delivery: build request: %w", err)}
	}
	httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")
	httpReq.Header.Set("User-Agent", s.userAgent)
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set(HeaderEventID, req.EventID)
	httpReq.Header.Set(HeaderDeliveryID, req.DeliveryID)
	httpReq.Header.Set(HeaderEventType, req.EventType)
	httpReq.Header.Set(HeaderTimestamp, strconv.FormatInt(timestamp, 10))
	httpReq.Header.Set(HeaderSignature, signature)
	httpReq.Header.Set(HeaderAttempt, strconv.Itoa(req.AttemptNumber))

	start := time.Now()
	resp, err := s.client.Do(httpReq)
	if err != nil {
		return Result{Duration: time.Since(start), Err: err}
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseSnippet))
		_ = resp.Body.Close()
	}()

	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseSnippet))
	return Result{
		StatusCode:   resp.StatusCode,
		Duration:     time.Since(start),
		ResponseBody: string(snippet),
	}
}
