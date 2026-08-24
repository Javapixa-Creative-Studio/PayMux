package midtrans

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
)

func testDisburser(t *testing.T, handler http.HandlerFunc) *Disburser {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	d, err := NewDisburser(gateway.Sandbox, "creator-key", "approver-key", server.Client())
	if err != nil {
		t.Fatalf("NewDisburser: %v", err)
	}
	d.BaseURL = server.URL
	return d
}

func createRequest() gateway.CreatePayoutRequest {
	return gateway.CreatePayoutRequest{
		IdempotencyKey:     "po_01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Amount:             125000,
		Currency:           "IDR",
		BeneficiaryName:    "Jon Snow",
		BeneficiaryAccount: "1172993826",
		BeneficiaryBank:    "BNI",
		Notes:              "Payout April 17",
	}
}

func TestCreatePayoutSendsTheIdempotencyKey(t *testing.T) {
	// Without this header a retried timeout becomes a second transfer, so it
	// is the single most important thing the adapter sends.
	var got string
	d := testDisburser(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Idempotency-Key")
		_, _ = io.WriteString(w, `{"payouts":[{"status":"queued","reference_no":"ref-1"}]}`)
	})

	if _, err := d.CreatePayout(context.Background(), createRequest()); err != nil {
		t.Fatalf("CreatePayout: %v", err)
	}
	if got != "po_01ARZ3NDEKTSV4RRFFQ69G5FAV" {
		t.Fatalf("X-Idempotency-Key = %q, want the request's key", got)
	}
}

func TestCreatePayoutRefusesWithoutAnIdempotencyKey(t *testing.T) {
	// Better to fail before the call than to make an unrepeatable one.
	d := testDisburser(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the gateway must not be called at all")
	})
	req := createRequest()
	req.IdempotencyKey = ""
	if _, err := d.CreatePayout(context.Background(), req); err == nil {
		t.Fatal("expected a refusal when no idempotency key is supplied")
	}
}

func TestCreatePayoutUsesTheCreatorKey(t *testing.T) {
	var auth string
	d := testDisburser(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"payouts":[{"status":"queued","reference_no":"ref-1"}]}`)
	})
	if _, err := d.CreatePayout(context.Background(), createRequest()); err != nil {
		t.Fatalf("CreatePayout: %v", err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("creator-key:"))
	if auth != want {
		t.Fatalf("creation used %q, want the creator credential", auth)
	}
}

func TestApproveUsesTheApproverKey(t *testing.T) {
	// The separation is the whole point: whoever can request a payout must
	// not be able to release it.
	var auth string
	d := testDisburser(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	if err := d.ApprovePayout(context.Background(), gateway.ApprovePayoutRequest{ReferenceNos: []string{"ref-1"}}); err != nil {
		t.Fatalf("ApprovePayout: %v", err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("approver-key:"))
	if auth != want {
		t.Fatalf("approval used %q, want the approver credential", auth)
	}
}

func TestApproveWithoutAnApproverKeyIsUnsupported(t *testing.T) {
	d := testDisburser(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the gateway must not be called")
	})
	d.ApproverKey = ""
	err := d.ApprovePayout(context.Background(), gateway.ApprovePayoutRequest{ReferenceNos: []string{"ref-1"}})
	if !errors.Is(err, gateway.ErrNotSupported) {
		t.Fatalf("err = %v, want ErrNotSupported", err)
	}
}

// --- The ambiguous cases -----------------------------------------------------
//
// Each of these is a failure where Midtrans may still have executed the
// transfer. Reporting them as ordinary errors would invite a caller to retry
// with a fresh key and send the money twice.

func TestTimeoutOnCreateIsAnUnknownOutcome(t *testing.T) {
	d := testDisburser(t, func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Skip("server does not support hijacking")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack: %v", err)
		}
		_ = conn.Close() // drop the connection mid-request
	})

	_, err := d.CreatePayout(context.Background(), createRequest())
	if !errors.Is(err, gateway.ErrOutcomeUnknown) {
		t.Fatalf("err = %v, want ErrOutcomeUnknown", err)
	}
}

func TestServerErrorOnCreateIsAnUnknownOutcome(t *testing.T) {
	d := testDisburser(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	_, err := d.CreatePayout(context.Background(), createRequest())
	if !errors.Is(err, gateway.ErrOutcomeUnknown) {
		t.Fatalf("err = %v, want ErrOutcomeUnknown", err)
	}
}

func TestUnreadableCreateResponseIsAnUnknownOutcome(t *testing.T) {
	// A 201 PayMux cannot parse still means Midtrans accepted the payout.
	d := testDisburser(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"payouts": [ truncated`)
	})
	_, err := d.CreatePayout(context.Background(), createRequest())
	if !errors.Is(err, gateway.ErrOutcomeUnknown) {
		t.Fatalf("err = %v, want ErrOutcomeUnknown", err)
	}
}

func TestEmptyCreateResponseIsAnUnknownOutcome(t *testing.T) {
	d := testDisburser(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"payouts":[]}`)
	})
	_, err := d.CreatePayout(context.Background(), createRequest())
	if !errors.Is(err, gateway.ErrOutcomeUnknown) {
		t.Fatalf("err = %v, want ErrOutcomeUnknown", err)
	}
}

func TestRejectedRequestIsNotAnUnknownOutcome(t *testing.T) {
	// A 4xx is a definite refusal. Calling it unknown would strand payouts
	// that plainly never happened, which is its own kind of wrong.
	d := testDisburser(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error_message":"beneficiary account is invalid"}`)
	})
	_, err := d.CreatePayout(context.Background(), createRequest())
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, gateway.ErrOutcomeUnknown) {
		t.Fatalf("a 4xx must not report an unknown outcome: %v", err)
	}
	if !strings.Contains(err.Error(), "beneficiary account is invalid") {
		t.Fatalf("err = %v, want the gateway's own reason", err)
	}
}

func TestIdempotencyConflictIsReportedAsSuch(t *testing.T) {
	d := testDisburser(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error_message":"idempotency-key is not unique"}`)
	})
	_, err := d.CreatePayout(context.Background(), createRequest())
	if !errors.Is(err, gateway.ErrIdempotencyConflict) {
		t.Fatalf("err = %v, want ErrIdempotencyConflict", err)
	}
}

func TestReadFailureOnANonMutatingCallIsOrdinary(t *testing.T) {
	// A GET that fails changed nothing, so it is safe to retry and must not
	// be dressed up as an unknown outcome.
	d := testDisburser(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	_, err := d.GetPayout(context.Background(), "ref-1")
	if errors.Is(err, gateway.ErrOutcomeUnknown) {
		t.Fatalf("a failed read must not report an unknown outcome: %v", err)
	}
}

// --- Status mapping ----------------------------------------------------------

func TestProcessedIsNotYetCompleted(t *testing.T) {
	// Midtrans's "processed" means the bank has the instruction, not that the
	// beneficiary has the money. Mapping it to COMPLETED would tell an
	// application funds had landed when they had not.
	got, err := mapIrisStatus("processed")
	if err != nil {
		t.Fatalf("mapIrisStatus: %v", err)
	}
	if got != gateway.PayoutSubmitted {
		t.Fatalf("processed mapped to %s, want SUBMITTED", got)
	}
}

func TestIrisStatusMapping(t *testing.T) {
	cases := map[string]gateway.PayoutStatus{
		"queued":    gateway.PayoutSubmitted,
		"processed": gateway.PayoutSubmitted,
		"completed": gateway.PayoutCompleted,
		"failed":    gateway.PayoutFailed,
		"rejected":  gateway.PayoutRejected,
		"COMPLETED": gateway.PayoutCompleted,
	}
	for in, want := range cases {
		got, err := mapIrisStatus(in)
		if err != nil {
			t.Errorf("mapIrisStatus(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("mapIrisStatus(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestUnmappedStatusIsAnErrorRatherThanAGuess(t *testing.T) {
	if _, err := mapIrisStatus("reversing"); err == nil {
		t.Fatal("an unmapped payout status must be reported, not guessed at")
	}
}

func TestFailedPayoutCarriesTheGatewaysReason(t *testing.T) {
	d := testDisburser(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{
			"amount": "338745.00",
			"reference_no": "83hgf882",
			"status": "failed",
			"updated_at": "2025-12-01T06:00:23.677256Z",
			"error_details": {"message": "beneficiary account is blocked", "code": "012"}
		}`)
	})
	result, err := d.GetPayout(context.Background(), "83hgf882")
	if err != nil {
		t.Fatalf("GetPayout: %v", err)
	}
	if result.Status != gateway.PayoutFailed {
		t.Fatalf("status = %s, want FAILED", result.Status)
	}
	if result.FailureCode != "012" || result.FailureReason != "beneficiary account is blocked" {
		t.Fatalf("failure = %q/%q, want the gateway's own code and message",
			result.FailureCode, result.FailureReason)
	}
	if result.Amount != 338745 {
		t.Fatalf("amount = %d, want 338745", result.Amount)
	}
}

func TestMissingPayoutIsReportedAsNotFound(t *testing.T) {
	d := testDisburser(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	_, err := d.GetPayout(context.Background(), "nope")
	if !errors.Is(err, gateway.ErrPayoutNotFound) {
		t.Fatalf("err = %v, want ErrPayoutNotFound", err)
	}
}

// --- Payload shaping ---------------------------------------------------------

func TestAmountIsSentAsADecimalString(t *testing.T) {
	// Midtrans wants a monetary string. PayMux holds minor units as integers
	// and must never reach for a float to bridge the two.
	var body string
	d := testDisburser(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		_, _ = io.WriteString(w, `{"payouts":[{"status":"queued","reference_no":"ref-1"}]}`)
	})
	if _, err := d.CreatePayout(context.Background(), createRequest()); err != nil {
		t.Fatalf("CreatePayout: %v", err)
	}
	if !strings.Contains(body, `"amount":"125000"`) {
		t.Fatalf("body = %s, want amount as the string 125000", body)
	}
}

func TestNotesAreSanitisedRatherThanRejected(t *testing.T) {
	// A note is descriptive. Losing a comma is never worth refusing to pay
	// somebody, but sending one Midtrans rejects would do exactly that.
	got := sanitiseNotes("Invoice #123 — April/May, 2026!")
	if strings.ContainsAny(got, "#—/,!") {
		t.Fatalf("sanitiseNotes left disallowed characters: %q", got)
	}
	if !strings.Contains(got, "Invoice") || !strings.Contains(got, "123") {
		t.Fatalf("sanitiseNotes discarded the meaning: %q", got)
	}
	if strings.Contains(got, "  ") {
		t.Fatalf("sanitiseNotes left doubled spaces: %q", got)
	}
}

func TestNotesAreTruncatedToTheGatewayLimit(t *testing.T) {
	got := sanitiseNotes(strings.Repeat("a", 250))
	if len(got) != maxNotesLength {
		t.Fatalf("len = %d, want %d", len(got), maxNotesLength)
	}
}

func TestSandboxAndProductionAreDifferentHosts(t *testing.T) {
	sandbox, err := NewDisburser(gateway.Sandbox, "k", "", nil)
	if err != nil {
		t.Fatalf("sandbox: %v", err)
	}
	production, err := NewDisburser(gateway.Production, "k", "", nil)
	if err != nil {
		t.Fatalf("production: %v", err)
	}
	if sandbox.BaseURL == production.BaseURL {
		t.Fatal("sandbox and production must not share a base URL")
	}
	if !strings.Contains(sandbox.BaseURL, "sandbox") {
		t.Fatalf("sandbox base URL = %q", sandbox.BaseURL)
	}
}

func TestDisburserNeedsACreatorKey(t *testing.T) {
	if _, err := NewDisburser(gateway.Sandbox, "", "approver", nil); err == nil {
		t.Fatal("a disburser without a creator key must not be constructed")
	}
}
