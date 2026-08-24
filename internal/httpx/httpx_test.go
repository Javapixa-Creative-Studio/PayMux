package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func decodeErrorBody(t *testing.T, rr *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var body errorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not a JSON error: %v (%s)", err, rr.Body.String())
	}
	return body
}

func TestFailHidesInternalDetail(t *testing.T) {
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/payments", nil)
	dbErr := errors.New(`pq: relation "payments" does not exist`)

	Fail(rr, r, ErrInternal(dbErr))

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "payments") {
		t.Fatalf("internal error leaked to client: %s", rr.Body.String())
	}
	body := decodeErrorBody(t, rr)
	if body.Error.Code != CodeInternal {
		t.Errorf("code = %q", body.Error.Code)
	}
}

func TestFailWrapsUnknownErrorsAsInternal(t *testing.T) {
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	Fail(rr, r, errors.New("sql: no rows in result set"))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "sql") {
		t.Fatalf("raw error leaked: %s", rr.Body.String())
	}
}

func TestFailIncludesRequestIDAndFields(t *testing.T) {
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r = r.WithContext(WithRequestID(r.Context(), "req_01ARZ3NDEKTSV4RRFFQ69G5FAV"))

	Fail(rr, r, ErrValidation("Request is invalid.").WithField("amount", "must be greater than zero"))

	body := decodeErrorBody(t, rr)
	if body.Error.RequestID != "req_01ARZ3NDEKTSV4RRFFQ69G5FAV" {
		t.Errorf("request_id = %q", body.Error.RequestID)
	}
	if len(body.Error.Fields) != 1 || body.Error.Fields[0].Field != "amount" {
		t.Errorf("fields = %+v", body.Error.Fields)
	}
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d", rr.Code)
	}
}

func TestAsErrorUnwrapsWrappedAPIError(t *testing.T) {
	inner := ErrNotFound(CodePaymentNotFound, "Payment was not found.")
	wrapped := errors.Join(errors.New("context"), inner)
	got := AsError(wrapped)
	if got.Code != CodePaymentNotFound {
		t.Fatalf("AsError lost the wrapped API error: %+v", got)
	}
	if AsError(nil) != nil {
		t.Fatal("AsError(nil) should be nil")
	}
}

type sample struct {
	Amount int64  `json:"amount"`
	Note   string `json:"note"`
}

func decodeInto(t *testing.T, body, contentType string) error {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	var v sample
	return DecodeJSON(r, &v)
}

func TestDecodeJSON(t *testing.T) {
	if err := decodeInto(t, `{"amount":100,"note":"ok"}`, "application/json"); err != nil {
		t.Fatalf("valid body rejected: %v", err)
	}
	if err := decodeInto(t, `{"amount":100}`, "application/json; charset=utf-8"); err != nil {
		t.Fatalf("charset parameter rejected: %v", err)
	}
	if err := decodeInto(t, `{"amount":100}`, ""); err != nil {
		t.Fatalf("missing content type rejected: %v", err)
	}
}

func TestDecodeJSONRejectsBadInput(t *testing.T) {
	cases := []struct {
		name, body, contentType string
		wantStatus              int
	}{
		{"unknown field", `{"amount":1,"nope":true}`, "application/json", http.StatusBadRequest},
		{"wrong type", `{"amount":"lots"}`, "application/json", http.StatusBadRequest},
		{"malformed", `{"amount":`, "application/json", http.StatusBadRequest},
		{"empty", ``, "application/json", http.StatusBadRequest},
		{"trailing content", `{"amount":1}{"amount":2}`, "application/json", http.StatusBadRequest},
		{"wrong content type", `{"amount":1}`, "text/plain", http.StatusUnsupportedMediaType},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := decodeInto(t, tc.body, tc.contentType)
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := AsError(err).Status; got != tc.wantStatus {
				t.Fatalf("status = %d, want %d (%v)", got, tc.wantStatus, err)
			}
		})
	}
}

func TestRequestIDMiddlewareMintsAndEchoes(t *testing.T) {
	var seen string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFromContext(r.Context())
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if seen == "" || !strings.HasPrefix(seen, "req_") {
		t.Fatalf("no request id assigned: %q", seen)
	}
	if rr.Header().Get(HeaderRequestID) != seen {
		t.Errorf("response header = %q, want %q", rr.Header().Get(HeaderRequestID), seen)
	}
}

func TestRequestIDIgnoresUntrustedInboundValue(t *testing.T) {
	var seen string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFromContext(r.Context())
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(HeaderRequestID, "injected log line")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if seen == "injected log line" {
		t.Fatal("client-controlled request id was accepted verbatim")
	}

	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.Header.Set(HeaderRequestID, "req_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	h.ServeHTTP(httptest.NewRecorder(), r2)
	if seen != "req_01ARZ3NDEKTSV4RRFFQ69G5FAV" {
		t.Fatalf("well-formed upstream request id not honoured: %q", seen)
	}
}

func TestRecovererReturnsOpaque500(t *testing.T) {
	h := RequestID(Recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom: server key SB-Mid-server-abc")
	})))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "SB-Mid-server") {
		t.Fatalf("panic detail leaked: %s", rr.Body.String())
	}
}

func TestLimitBodyRejectsOversizedPayload(t *testing.T) {
	h := LimitBody(32)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v sample
		if err := DecodeJSON(r, &v); err != nil {
			Fail(w, r, err)
			return
		}
		JSON(w, r, http.StatusOK, v)
	}))
	body := `{"note":"` + strings.Repeat("x", 200) + `"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (%s)", rr.Code, rr.Body.String())
	}
}

func TestSecureHeaders(t *testing.T) {
	rr := httptest.NewRecorder()
	SecureHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := rr.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestCORSOnlyReflectsAllowedOrigins(t *testing.T) {
	h := CORS([]string{"https://dash.example.com"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Origin", "https://dash.example.com")
	h.ServeHTTP(rr, r)
	if rr.Header().Get("Access-Control-Allow-Origin") != "https://dash.example.com" {
		t.Error("allowed origin was not reflected")
	}

	rr = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Origin", "https://evil.example.com")
	h.ServeHTTP(rr, r)
	if rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("disallowed origin was reflected")
	}
}

func TestCORSPreflight(t *testing.T) {
	called := false
	h := CORS([]string{"https://dash.example.com"})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodOptions, "/", nil)
	r.Header.Set("Origin", "https://dash.example.com")
	h.ServeHTTP(rr, r)
	if rr.Code != http.StatusNoContent || called {
		t.Fatalf("preflight not short-circuited: status=%d called=%v", rr.Code, called)
	}
}

func TestRateLimiter(t *testing.T) {
	rl := NewRateLimiter(1, 2)
	for i := 0; i < 2; i++ {
		if !rl.Allow("1.2.3.4") {
			t.Fatalf("request %d denied within burst", i)
		}
	}
	if rl.Allow("1.2.3.4") {
		t.Fatal("burst was not enforced")
	}
	if !rl.Allow("5.6.7.8") {
		t.Fatal("limiter is not per-key")
	}
}

func TestRateLimiterMiddleware(t *testing.T) {
	rl := NewRateLimiter(0.0001, 1)
	h := rl.Middleware(ByClientIP)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		JSON(w, r, http.StatusOK, map[string]string{"ok": "yes"})
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:5000"

	first := httptest.NewRecorder()
	h.ServeHTTP(first, req)
	if first.Code != http.StatusOK {
		t.Fatalf("first request status = %d", first.Code)
	}
	second := httptest.NewRecorder()
	h.ServeHTTP(second, req)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", second.Code)
	}
	if decodeErrorBody(t, second).Error.Code != CodeRateLimited {
		t.Error("rate limit response used the wrong error code")
	}
}

func TestRateLimiterEvictsStaleBuckets(t *testing.T) {
	rl := NewRateLimiter(100, 100)
	now := time.Now()
	rl.now = func() time.Time { return now }
	for i := 0; i < 1100; i++ {
		rl.Allow(string(rune(i)) + "key")
	}
	before := len(rl.buckets)
	now = now.Add(time.Hour)
	rl.Allow("fresh")
	if len(rl.buckets) >= before {
		t.Fatalf("stale buckets not evicted: %d -> %d", before, len(rl.buckets))
	}
}

func TestClientIPIgnoresProxyHeadersByDefault(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.5:1234"
	r.Header.Set("X-Forwarded-For", "1.1.1.1")
	if got := ClientIP(r); got != "10.0.0.5" {
		t.Fatalf("ClientIP = %q, want 10.0.0.5", got)
	}

	TrustProxyHeaders = true
	defer func() { TrustProxyHeaders = false }()
	if got := ClientIP(r); got != "1.1.1.1" {
		t.Fatalf("ClientIP with proxy trust = %q, want 1.1.1.1", got)
	}
}

func TestJSONWritesContentType(t *testing.T) {
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	JSON(rr, r, http.StatusCreated, map[string]any{"id": "pay_1"})
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	if rr.Code != http.StatusCreated {
		t.Errorf("status = %d", rr.Code)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte(`"pay_1"`)) {
		t.Errorf("body = %s", rr.Body.String())
	}
}

// A wildcard origin combined with credentialed responses would let any site
// drive the admin API with an operator's session cookie. The browser forbids
// literal "*" with credentials, and reflecting the caller's origin instead is
// the same hole in disguise, so the wildcard must not be honoured at all.
func TestCORSRefusesWildcardBecauseResponsesCarryCredentials(t *testing.T) {
	h := CORS([]string{"*"})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Origin", "https://evil.example.com")
	h.ServeHTTP(rr, r)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("a wildcard configuration reflected %q alongside credentials", got)
	}
}

func TestCORSStillAllowsConfiguredOriginsWhenAWildcardIsPresent(t *testing.T) {
	// An operator who writes "*, https://dash.example.com" should still get
	// the origin they named, rather than losing access entirely.
	h := CORS([]string{"*", "https://dash.example.com"})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Origin", "https://dash.example.com")
	h.ServeHTTP(rr, r)

	if rr.Header().Get("Access-Control-Allow-Origin") != "https://dash.example.com" {
		t.Fatal("a named origin was lost because a wildcard was also configured")
	}
}

func TestProxyTrustIsConfigurable(t *testing.T) {
	// Without this being settable, PayMux behind a proxy sees every request as
	// coming from the proxy: per-client rate limits collapse into one bucket
	// and the audit trail records the wrong address.
	defer SetTrustProxyHeaders(false)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.5:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")

	SetTrustProxyHeaders(false)
	if got := ClientIP(r); got != "10.0.0.5" {
		t.Errorf("with proxy trust off, ClientIP = %q, want the direct peer", got)
	}

	SetTrustProxyHeaders(true)
	if got := ClientIP(r); got != "203.0.113.9" {
		t.Errorf("with proxy trust on, ClientIP = %q, want the original client", got)
	}
}
