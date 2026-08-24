package midtrans

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/crypto"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/money"
)

// Midtrans's disbursement product, formerly branded Iris, lives on its own
// host and speaks its own dialect: separate base URLs, separate credentials,
// and monetary values as decimal strings rather than integers.
const (
	irisSandboxURL    = "https://app.sandbox.midtrans.com/iris"
	irisProductionURL = "https://app.midtrans.com/iris"
)

// notesPattern keeps only what a beneficiary's bank will accept on a
// statement line. Midtrans restricts this to ASCII alphanumerics and spaces
// for bank destinations, and rejects the whole payout otherwise, so PayMux
// sanitises rather than fails, since a note is descriptive and losing a comma
// is never worth refusing to pay somebody.
var notesPattern = regexp.MustCompile(`[^a-zA-Z0-9 ]+`)

// maxNotesLength is Midtrans's limit for the statement note.
const maxNotesLength = 100

// Disburser implements gateway.DisbursementGateway for Midtrans.
//
// It is a separate type from Adapter, holding separate credentials, because
// Midtrans issues separate creator and approver keys and PayMux has no reason
// to let the payment path reach either of them. A leak of the payment server
// key does not become the ability to move money out.
type Disburser struct {
	BaseURL     string
	CreatorKey  crypto.Secret
	ApproverKey crypto.Secret
	HTTPClient  *http.Client
	Metrics     RequestRecorder
	Environment gateway.Environment
}

var (
	_ gateway.DisbursementGateway = (*Disburser)(nil)
	_ gateway.AccountValidator    = (*Disburser)(nil)
	_ gateway.BankLister          = (*Disburser)(nil)
	_ gateway.BalanceReporter     = (*Disburser)(nil)
)

// NewDisburser builds a disburser for an account's environment.
//
// The creator key is required; the approver key is not, because a merchant may
// legitimately approve from the Midtrans dashboard rather than through PayMux.
func NewDisburser(env gateway.Environment, creator, approver crypto.Secret, httpClient *http.Client) (*Disburser, error) {
	if !env.Valid() {
		return nil, errors.New("midtrans: disbursement environment must be sandbox or production")
	}
	if creator == "" {
		return nil, errors.New("midtrans: disbursement needs a creator API key")
	}
	base := irisProductionURL
	if env == gateway.Sandbox {
		base = irisSandboxURL
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Disburser{
		BaseURL:     base,
		CreatorKey:  creator,
		ApproverKey: approver,
		HTTPClient:  httpClient,
		Environment: env,
	}, nil
}

// CanApprove reports whether this disburser holds an approver credential.
func (d *Disburser) CanApprove() bool { return d.ApproverKey != "" }

// ---------------------------------------------------------------------------
// Wire types
// ---------------------------------------------------------------------------

type irisPayoutRequest struct {
	BeneficiaryName    string `json:"beneficiary_name"`
	BeneficiaryAccount string `json:"beneficiary_account"`
	BeneficiaryBank    string `json:"beneficiary_bank"`
	BeneficiaryEmail   string `json:"beneficiary_email,omitempty"`
	Amount             string `json:"amount"`
	Notes              string `json:"notes,omitempty"`
}

type irisCreateRequest struct {
	Payouts []irisPayoutRequest `json:"payouts"`
}

type irisCreateResponse struct {
	Payouts []struct {
		Status      string `json:"status"`
		ReferenceNo string `json:"reference_no"`
	} `json:"payouts"`
	ErrorMessage string `json:"error_message"`
	Errors       any    `json:"errors"`
}

type irisPayoutDetails struct {
	Amount             string `json:"amount"`
	BeneficiaryName    string `json:"beneficiary_name"`
	BeneficiaryAccount string `json:"beneficiary_account"`
	Bank               string `json:"bank"`
	ReferenceNo        string `json:"reference_no"`
	Notes              string `json:"notes"`
	Status             string `json:"status"`
	CreatedBy          string `json:"created_by"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
	ErrorDetails       *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error_details"`
}

type irisApproveRequest struct {
	ReferenceNos []string `json:"reference_nos"`
	OTP          string   `json:"otp,omitempty"`
}

type irisRejectRequest struct {
	ReferenceNos []string `json:"reference_nos"`
	RejectReason string   `json:"reject_reason"`
}

type irisAccountResponse struct {
	AccountName string `json:"account_name"`
	AccountNo   string `json:"account_no"`
}

type irisBank struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type irisBanksResponse struct {
	BeneficiaryBanks []irisBank `json:"beneficiary_banks"`
}

// ---------------------------------------------------------------------------
// Operations
// ---------------------------------------------------------------------------

// CreatePayout submits one payout.
//
// PayMux sends payouts one at a time even though the endpoint accepts a batch.
// A batch shares one idempotency key and one failure, which would make a
// partial failure impossible to attribute, and attribution is the whole point
// when the subject is somebody's money.
func (d *Disburser) CreatePayout(ctx context.Context, req gateway.CreatePayoutRequest) (*gateway.PayoutResult, error) {
	if req.IdempotencyKey == "" {
		return nil, errors.New("midtrans: refusing to create a payout without an idempotency key")
	}
	amount, err := money.Format(req.Amount, req.Currency)
	if err != nil {
		return nil, fmt.Errorf("midtrans: payout amount is not usable: %w", err)
	}

	body := irisCreateRequest{Payouts: []irisPayoutRequest{{
		BeneficiaryName:    req.BeneficiaryName,
		BeneficiaryAccount: req.BeneficiaryAccount,
		BeneficiaryBank:    strings.ToLower(req.BeneficiaryBank),
		BeneficiaryEmail:   req.BeneficiaryEmail,
		Amount:             amount,
		Notes:              sanitiseNotes(req.Notes),
	}}}

	var out irisCreateResponse
	err = d.do(ctx, http.MethodPost, "/api/v1/payouts", d.CreatorKey, req.IdempotencyKey, body, &out)
	if err != nil {
		return nil, err
	}
	if len(out.Payouts) == 0 {
		// A 2xx with no payout in it means the gateway accepted something and
		// told PayMux nothing about it. That is indistinguishable from a
		// timeout as far as safety goes, so it is treated the same way.
		return nil, fmt.Errorf("%w: the gateway returned no payout", gateway.ErrOutcomeUnknown)
	}

	created := out.Payouts[0]
	status, err := mapIrisStatus(created.Status)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(created)
	return &gateway.PayoutResult{
		ReferenceNo:   created.ReferenceNo,
		Status:        status,
		GatewayStatus: created.Status,
		Amount:        req.Amount,
		UpdatedAt:     time.Now().UTC(),
		Raw:           raw,
	}, nil
}

// GetPayout reads a payout's authoritative state.
func (d *Disburser) GetPayout(ctx context.Context, referenceNo string) (*gateway.PayoutResult, error) {
	if referenceNo == "" {
		return nil, errors.New("midtrans: no payout reference supplied")
	}
	var out irisPayoutDetails
	err := d.do(ctx, http.MethodGet, "/api/v1/payouts/"+referenceNo, d.CreatorKey, "", nil, &out)
	if err != nil {
		var gwErr *gateway.Error
		if errors.As(err, &gwErr) && gwErr.StatusCode == http.StatusNotFound {
			return nil, gateway.ErrPayoutNotFound
		}
		return nil, err
	}

	status, err := mapIrisStatus(out.Status)
	if err != nil {
		return nil, err
	}
	result := &gateway.PayoutResult{
		ReferenceNo:   out.ReferenceNo,
		Status:        status,
		GatewayStatus: out.Status,
		UpdatedAt:     parseIrisTime(out.UpdatedAt),
	}
	if out.ErrorDetails != nil {
		result.FailureCode = out.ErrorDetails.Code
		result.FailureReason = out.ErrorDetails.Message
	}
	if amount, err := money.Parse(out.Amount, "IDR"); err == nil {
		result.Amount = amount
	}
	result.Raw, _ = json.Marshal(out)
	return result, nil
}

// ApprovePayout releases payouts the gateway is holding.
func (d *Disburser) ApprovePayout(ctx context.Context, req gateway.ApprovePayoutRequest) error {
	if len(req.ReferenceNos) == 0 {
		return errors.New("midtrans: no payout references to approve")
	}
	if d.ApproverKey == "" {
		return fmt.Errorf("%w: this account has no approver key", gateway.ErrNotSupported)
	}
	// The approval is keyed on the references themselves, so a retry of the
	// same approval is the same request rather than a second release.
	key := "approve-" + strings.Join(req.ReferenceNos, "-")
	body := irisApproveRequest{ReferenceNos: req.ReferenceNos, OTP: req.OTP}
	return d.do(ctx, http.MethodPost, "/api/v1/payouts/approve", d.ApproverKey, key, body, nil)
}

// RejectPayout refuses payouts the gateway is holding.
func (d *Disburser) RejectPayout(ctx context.Context, req gateway.RejectPayoutRequest) error {
	if len(req.ReferenceNos) == 0 {
		return errors.New("midtrans: no payout references to reject")
	}
	if d.ApproverKey == "" {
		return fmt.Errorf("%w: this account has no approver key", gateway.ErrNotSupported)
	}
	reason := req.Reason
	if reason == "" {
		reason = "Rejected in PayMux"
	}
	key := "reject-" + strings.Join(req.ReferenceNos, "-")
	body := irisRejectRequest{ReferenceNos: req.ReferenceNos, RejectReason: reason}
	return d.do(ctx, http.MethodPost, "/api/v1/payouts/reject", d.ApproverKey, key, body, nil)
}

// ValidateAccount asks the bank who owns an account, so a caller can compare
// the answer against the name they hold before sending money to it.
func (d *Disburser) ValidateAccount(ctx context.Context, account, bank string) (*gateway.AccountValidation, error) {
	if account == "" || bank == "" {
		return nil, errors.New("midtrans: account and bank are both required")
	}
	path := fmt.Sprintf("/api/v1/account_validation?bank=%s&account=%s",
		urlValue(strings.ToLower(bank)), urlValue(account))

	var out irisAccountResponse
	if err := d.do(ctx, http.MethodGet, path, d.CreatorKey, "", nil, &out); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(out)
	return &gateway.AccountValidation{
		AccountName:   out.AccountName,
		AccountNumber: out.AccountNo,
		Raw:           raw,
	}, nil
}

// ListBanks enumerates the destinations this gateway can pay out to.
func (d *Disburser) ListBanks(ctx context.Context) ([]gateway.Bank, error) {
	var out irisBanksResponse
	if err := d.do(ctx, http.MethodGet, "/api/v1/beneficiary_banks", d.CreatorKey, "", nil, &out); err != nil {
		return nil, err
	}
	banks := make([]gateway.Bank, 0, len(out.BeneficiaryBanks))
	for _, b := range out.BeneficiaryBanks {
		banks = append(banks, gateway.Bank{Code: b.Code, Name: b.Name})
	}
	return banks, nil
}

// GetBalance reports what is available to pay out.
//
// The endpoint is confirmed to exist: an authenticated probe answers 401
// where an unknown path answers 404, but Midtrans does not currently document
// its response, so the shape here is inferred from the legacy Iris API. The
// raw body is kept and an unreadable amount is reported as an error rather
// than silently becoming zero: a balance that reads as nothing when it is not
// would stop payouts for no reason, and one that reads as something when it is
// nothing is worse.
func (d *Disburser) GetBalance(ctx context.Context) (*gateway.Balance, error) {
	var out struct {
		Balance string `json:"balance"`
	}
	raw, err := d.doRaw(ctx, http.MethodGet, "/api/v1/balance", d.CreatorKey, "", nil, &out)
	if err != nil {
		return nil, err
	}
	if out.Balance == "" {
		return nil, fmt.Errorf("midtrans: the balance response had no balance in it: %s",
			truncate(string(raw), 200))
	}
	amount, err := money.Parse(out.Balance, "IDR")
	if err != nil {
		return nil, fmt.Errorf("midtrans: balance %q is not a usable amount: %w", out.Balance, err)
	}
	return &gateway.Balance{Amount: amount, Currency: "IDR", Raw: raw}, nil
}

// ---------------------------------------------------------------------------
// Transport
// ---------------------------------------------------------------------------

// do performs one Iris request.
//
// It differs from the payment client in one way that matters: any failure
// where the request might still have been executed is reported as
// ErrOutcomeUnknown rather than as an ordinary error. Getting that
// distinction wrong is how a retry becomes a second transfer.
func (d *Disburser) do(ctx context.Context, method, path string, key crypto.Secret, idempotencyKey string, body, out any) error {
	_, err := d.doRaw(ctx, method, path, key, idempotencyKey, body, out)
	return err
}

// doRaw is do, additionally handing back the response body. Callers that need
// to keep what the gateway actually said use this; everything else uses do.
func (d *Disburser) doRaw(ctx context.Context, method, path string, key crypto.Secret, idempotencyKey string, body, out any) (raw []byte, err error) {
	if d.Metrics != nil {
		start := time.Now()
		operation := "iris." + strings.TrimPrefix(strings.SplitN(strings.TrimPrefix(path, "/api/v1/"), "?", 2)[0], "/")
		defer func() { d.Metrics.RecordGatewayRequest(Name, operation, time.Since(start), err) }()
	}

	var payload *strings.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("midtrans: encode payout request: %w", err)
		}
		payload = strings.NewReader(string(encoded))
	}

	var req *http.Request
	if payload != nil {
		req, err = http.NewRequestWithContext(ctx, method, d.BaseURL+path, payload)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, d.BaseURL+path, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("midtrans: build payout request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(key.Reveal()+":")))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set("X-Idempotency-Key", truncate(idempotencyKey, 100))
	}

	mutating := method == http.MethodPost

	resp, err := d.HTTPClient.Do(req)
	if err != nil {
		if mutating {
			// The request left PayMux and no answer came back. Midtrans may
			// have executed it. Only the idempotency key can settle this, and
			// only a caller who kept it can ask.
			return nil, fmt.Errorf("%w: %s did not answer: %w", gateway.ErrOutcomeUnknown, path, err)
		}
		return nil, &gateway.Error{Gateway: Name, Message: "could not reach the disbursement gateway", Retryable: true, Err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	raw, readErr := readLimited(resp.Body)
	if readErr != nil {
		if mutating {
			return nil, fmt.Errorf("%w: could not read the response to %s: %w", gateway.ErrOutcomeUnknown, path, readErr)
		}
		return nil, &gateway.Error{Gateway: Name, StatusCode: resp.StatusCode, Message: "could not read the gateway response", Retryable: true, Err: readErr}
	}

	if resp.StatusCode == http.StatusConflict {
		// Midtrans rejects a key reused with different content. That is a
		// caller bug, and retrying will not fix it.
		return nil, fmt.Errorf("%w: %s", gateway.ErrIdempotencyConflict, irisMessage(raw))
	}

	if resp.StatusCode >= 500 {
		if mutating {
			// A 5xx after the request was accepted for processing is exactly
			// the ambiguous case: the gateway may have done the work before
			// falling over.
			return nil, fmt.Errorf("%w: %s returned HTTP %d", gateway.ErrOutcomeUnknown, path, resp.StatusCode)
		}
		return nil, &gateway.Error{Gateway: Name, StatusCode: resp.StatusCode, Message: irisMessage(raw), Retryable: true}
	}

	if resp.StatusCode >= 400 {
		// A 4xx is a definite refusal: the gateway understood and declined, so
		// nothing was executed and the caller may safely correct and retry.
		message := irisMessage(raw)
		if strings.Contains(strings.ToLower(message), "insufficient") {
			return nil, fmt.Errorf("%w: %s", gateway.ErrInsufficientBalance, message)
		}
		return nil, &gateway.Error{Gateway: Name, StatusCode: resp.StatusCode, Message: message}
	}

	if out == nil {
		return raw, nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		if mutating {
			return nil, fmt.Errorf("%w: the response to %s was unreadable: %w", gateway.ErrOutcomeUnknown, path, err)
		}
		return nil, &gateway.Error{Gateway: Name, StatusCode: resp.StatusCode, Message: "the gateway returned an unreadable response", Err: err}
	}
	return raw, nil
}

// mapIrisStatus translates Midtrans's payout vocabulary into PayMux's.
//
// Midtrans reports four states. "processed" means the request reached the bank
// but the beneficiary has not been credited yet, which is PayMux's SUBMITTED
// rather than COMPLETED: treating it as completed would tell an application
// money had arrived when it had not.
func mapIrisStatus(status string) (gateway.PayoutStatus, error) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "processed", "pending", "approved":
		return gateway.PayoutSubmitted, nil
	case "completed", "success", "succeeded":
		return gateway.PayoutCompleted, nil
	case "failed":
		return gateway.PayoutFailed, nil
	case "rejected":
		return gateway.PayoutRejected, nil
	default:
		// An unmapped status is never guessed at. The caller records it
		// verbatim and leaves the payout where it is, which is the same rule
		// the payment mapper follows.
		return "", fmt.Errorf("midtrans: unmapped payout status %q", status)
	}
}

// sanitiseNotes trims a note to what a bank statement will carry.
func sanitiseNotes(notes string) string {
	cleaned := strings.TrimSpace(notesPattern.ReplaceAllString(notes, " "))
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	return truncate(cleaned, maxNotesLength)
}

func parseIrisTime(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// irisMessage pulls the human-readable part out of an Iris error body.
func irisMessage(raw []byte) string {
	var envelope struct {
		ErrorMessage string   `json:"error_message"`
		Errors       []string `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil {
		if envelope.ErrorMessage != "" {
			return envelope.ErrorMessage
		}
		if len(envelope.Errors) > 0 {
			return strings.Join(envelope.Errors, "; ")
		}
	}
	if len(raw) == 0 {
		return "the disbursement gateway rejected the request"
	}
	return truncate(string(raw), 300)
}

func urlValue(v string) string {
	return strings.NewReplacer(" ", "%20", "&", "%26", "?", "%3F", "#", "%23").Replace(v)
}

// readLimited reads a response body, bounded so a misbehaving upstream cannot
// exhaust memory.
func readLimited(r io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, maxResponseBytes))
}
