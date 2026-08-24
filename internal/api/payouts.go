package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/application"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/auth"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/httpx"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/payout"
)

type createPayoutRequest struct {
	ApplicationPayoutID string          `json:"application_payout_id"`
	BeneficiaryID       string          `json:"beneficiary_id"`
	BeneficiaryAlias    string          `json:"beneficiary_alias"`
	Amount              int64           `json:"amount"`
	Currency            string          `json:"currency"`
	Notes               string          `json:"notes"`
	Metadata            json.RawMessage `json:"metadata"`
}

// handleCreatePayout records a payout an application wants to make.
//
// It returns 202 rather than 201 when approval is required, because the
// request has been accepted and nothing has moved yet. A 201 would suggest the
// transfer exists at the gateway, which is exactly the misreading that matters
// here.
func (s *Server) handleCreatePayout(w http.ResponseWriter, r *http.Request) {
	app := auth.ApplicationFromContext(r.Context())
	apiKey := auth.APIKeyFromContext(r.Context())

	var req createPayoutRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		fail(w, r, err, payoutMissing)
		return
	}

	account, err := s.payoutAccount(r.Context(), app)
	if err != nil {
		fail(w, r, err, payoutMissing)
		return
	}
	if !account.CanDisburse() {
		httpx.Fail(w, r, httpx.NewError(http.StatusPreconditionFailed, "disbursement_not_configured",
			"This gateway account has no disbursement credentials. Add them in Gateways before paying out."))
		return
	}
	if apiKey != nil && apiKey.Mode != account.ExpectedKeyMode() {
		httpx.Fail(w, r, httpx.NewError(http.StatusForbidden, httpx.CodeForbidden,
			"This API key's mode does not match the configured gateway environment."))
		return
	}

	currency := req.Currency
	if currency == "" {
		currency = "IDR"
	}

	p, err := s.payouts.Request(r.Context(), payout.RequestInput{
		ApplicationID:       app.ID,
		GatewayAccountID:    account.ID,
		Gateway:             account.Gateway,
		ApplicationPayoutID: strings.TrimSpace(req.ApplicationPayoutID),
		BeneficiaryID:       req.BeneficiaryID,
		BeneficiaryAlias:    req.BeneficiaryAlias,
		Amount:              req.Amount,
		Currency:            currency,
		Notes:               req.Notes,
		Metadata:            req.Metadata,
	})
	if err != nil {
		fail(w, r, err, payoutMissing)
		return
	}

	status := http.StatusAccepted
	if !p.NeedsApproval() {
		status = http.StatusCreated
	}
	httpx.JSON(w, r, status, p)
}

// handleGetPayout reads one of the calling application's payouts.
func (s *Server) handleGetPayout(w http.ResponseWriter, r *http.Request) {
	app := auth.ApplicationFromContext(r.Context())
	p, err := s.payouts.Get(r.Context(), app.ID, chi.URLParam(r, "payoutID"))
	if err != nil {
		fail(w, r, err, payoutMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, p)
}

// handleListPayouts lists the calling application's payouts.
func (s *Server) handleListPayouts(w http.ResponseWriter, r *http.Request) {
	app := auth.ApplicationFromContext(r.Context())
	list, err := s.payouts.List(r.Context(), payout.Filter{
		ApplicationID: app.ID,
		Status:        strings.ToUpper(r.URL.Query().Get("status")),
		BeneficiaryID: r.URL.Query().Get("beneficiary_id"),
	}, pageFromRequest(r))
	if err != nil {
		fail(w, r, err, payoutMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, list)
}

// ---------------------------------------------------------------------------
// Beneficiaries
// ---------------------------------------------------------------------------

type beneficiaryRequest struct {
	Alias    string          `json:"alias"`
	Name     string          `json:"name"`
	Account  string          `json:"account"`
	Bank     string          `json:"bank"`
	Email    string          `json:"email"`
	Metadata json.RawMessage `json:"metadata"`
}

type updateBeneficiaryRequest struct {
	Name     *string `json:"name"`
	Account  *string `json:"account"`
	Bank     *string `json:"bank"`
	Email    *string `json:"email"`
	Disabled *bool   `json:"disabled"`
}

func (s *Server) handleCreateBeneficiary(w http.ResponseWriter, r *http.Request) {
	app := auth.ApplicationFromContext(r.Context())

	var req beneficiaryRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		fail(w, r, err, beneficiaryMissing)
		return
	}
	if err := validateBeneficiary(req); err != nil {
		fail(w, r, err, beneficiaryMissing)
		return
	}

	b := &payout.Beneficiary{
		ApplicationID: app.ID,
		Alias:         strings.TrimSpace(req.Alias),
		Name:          strings.TrimSpace(req.Name),
		Account:       strings.TrimSpace(req.Account),
		Bank:          strings.ToLower(strings.TrimSpace(req.Bank)),
		Email:         strings.TrimSpace(req.Email),
		Metadata:      req.Metadata,
	}
	if err := s.payouts.Repo().CreateBeneficiary(r.Context(), b); err != nil {
		fail(w, r, err, beneficiaryMissing)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, b)
}

func (s *Server) handleListBeneficiaries(w http.ResponseWriter, r *http.Request) {
	app := auth.ApplicationFromContext(r.Context())
	list, err := s.payouts.Repo().ListBeneficiaries(r.Context(), app.ID, pageFromRequest(r))
	if err != nil {
		fail(w, r, err, beneficiaryMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, list)
}

func (s *Server) handleGetBeneficiary(w http.ResponseWriter, r *http.Request) {
	app := auth.ApplicationFromContext(r.Context())
	b, err := s.payouts.Repo().GetBeneficiary(r.Context(), app.ID, chi.URLParam(r, "beneficiaryID"))
	if err != nil {
		fail(w, r, err, beneficiaryMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, b)
}

func (s *Server) handleUpdateBeneficiary(w http.ResponseWriter, r *http.Request) {
	app := auth.ApplicationFromContext(r.Context())

	var req updateBeneficiaryRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		fail(w, r, err, beneficiaryMissing)
		return
	}
	b, err := s.payouts.Repo().UpdateBeneficiary(r.Context(), app.ID,
		chi.URLParam(r, "beneficiaryID"), payout.BeneficiaryUpdate{
			Name: req.Name, Account: req.Account, Bank: req.Bank,
			Email: req.Email, Disabled: req.Disabled,
		})
	if err != nil {
		fail(w, r, err, beneficiaryMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, b)
}

func (s *Server) handleDeleteBeneficiary(w http.ResponseWriter, r *http.Request) {
	app := auth.ApplicationFromContext(r.Context())
	if err := s.payouts.Repo().DeleteBeneficiary(r.Context(), app.ID, chi.URLParam(r, "beneficiaryID")); err != nil {
		fail(w, r, err, beneficiaryMissing)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validateBeneficiary(req beneficiaryRequest) error {
	var fields []struct{ name, problem string }
	if strings.TrimSpace(req.Alias) == "" {
		fields = append(fields, struct{ name, problem string }{"alias", "is required"})
	}
	if strings.TrimSpace(req.Name) == "" {
		fields = append(fields, struct{ name, problem string }{"name", "is required"})
	}
	if strings.TrimSpace(req.Account) == "" {
		fields = append(fields, struct{ name, problem string }{"account", "is required"})
	}
	if strings.TrimSpace(req.Bank) == "" {
		fields = append(fields, struct{ name, problem string }{"bank", "is required"})
	}
	if len(fields) == 0 {
		return nil
	}
	err := httpx.ErrInvalidRequest("A beneficiary needs an alias, a name, an account and a bank.")
	for _, f := range fields {
		err = err.WithField(f.name, f.problem)
	}
	return err
}

// payoutAccount resolves which gateway account an application disburses
// through. It mirrors the payment path: an explicit assignment wins, and
// otherwise the default account for the only gateway V1 supports.
func (s *Server) payoutAccount(ctx context.Context, app *application.Application) (*gateway.Account, error) {
	if app.GatewayAccountID != "" {
		account, err := s.gatewayAccounts.Get(ctx, app.GatewayAccountID)
		if err != nil {
			return nil, httpx.NewError(http.StatusPreconditionFailed, "gateway_not_configured",
				"This application has no usable gateway account.")
		}
		if !account.Usable() {
			return nil, httpx.NewError(http.StatusPreconditionFailed, "gateway_not_configured",
				"This application's gateway account is disabled.")
		}
		return account, nil
	}
	account, err := s.gatewayAccounts.Default(ctx, "midtrans")
	if err != nil || !account.Usable() {
		return nil, httpx.NewError(http.StatusPreconditionFailed, "gateway_not_configured",
			"No gateway account is configured.")
	}
	return account, nil
}
