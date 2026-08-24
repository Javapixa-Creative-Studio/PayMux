package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/auth"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/httpx"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/payout"
)

// handleAdminListPayouts lists payouts across every application.
func (s *Server) handleAdminListPayouts(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	list, err := s.payouts.List(r.Context(), payout.Filter{
		ApplicationID: query.Get("application_id"),
		Status:        strings.ToUpper(query.Get("status")),
		BeneficiaryID: query.Get("beneficiary_id"),
		Search:        strings.TrimSpace(query.Get("search")),
	}, pageFromRequest(r))
	if err != nil {
		fail(w, r, err, payoutMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, list)
}

// payoutDetailResponse is a payout with the trail of who did what to it.
type payoutDetailResponse struct {
	Payout      *payout.Payout       `json:"payout"`
	Transitions []*payout.Transition `json:"transitions"`
}

// handleAdminGetPayout reads a payout and its history.
//
// The history is not optional here. For a payment an operator mostly wants the
// current state; for a payout the question is usually who released it, and the
// answer only exists in the trail.
func (s *Server) handleAdminGetPayout(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "payoutID")
	p, err := s.payouts.Get(r.Context(), "", id)
	if err != nil {
		fail(w, r, err, payoutMissing)
		return
	}
	transitions, err := s.payouts.Transitions(r.Context(), id)
	if err != nil {
		fail(w, r, err, payoutMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, payoutDetailResponse{Payout: p, Transitions: transitions})
}

// handleApprovePayout releases a payout for submission.
func (s *Server) handleApprovePayout(w http.ResponseWriter, r *http.Request) {
	admin := auth.AdminFromContext(r.Context())
	id := chi.URLParam(r, "payoutID")

	p, err := s.payouts.Approve(r.Context(), id, admin.ID)
	if err != nil {
		fail(w, r, err, payoutMissing)
		return
	}
	// Approving a payout is the moment a person becomes answerable for it, so
	// the audit entry records the amount and destination rather than just the
	// identifier.
	s.audit(r, "payout.approved", "payout", id, map[string]any{
		"amount":              p.Amount,
		"currency":            p.Currency,
		"beneficiary_account": p.BeneficiaryAccount,
		"beneficiary_bank":    p.BeneficiaryBank,
	})
	httpx.JSON(w, r, http.StatusOK, p)
}

type rejectPayoutRequest struct {
	Reason string `json:"reason"`
}

// handleRejectPayout refuses a payout. Nothing is sent to the gateway.
func (s *Server) handleRejectPayout(w http.ResponseWriter, r *http.Request) {
	admin := auth.AdminFromContext(r.Context())
	id := chi.URLParam(r, "payoutID")

	var req rejectPayoutRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		fail(w, r, err, payoutMissing)
		return
	}

	p, err := s.payouts.Reject(r.Context(), id, admin.ID, strings.TrimSpace(req.Reason))
	if err != nil {
		fail(w, r, err, payoutMissing)
		return
	}
	s.audit(r, "payout.rejected", "payout", id, map[string]any{
		"amount": p.Amount,
		"reason": p.RejectReason,
	})
	httpx.JSON(w, r, http.StatusOK, p)
}

// handleSyncPayout asks the gateway what happened to a payout.
func (s *Server) handleSyncPayout(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "payoutID")
	p, err := s.payouts.Get(r.Context(), "", id)
	if err != nil {
		fail(w, r, err, payoutMissing)
		return
	}
	updated, err := s.payouts.Reconcile(r.Context(), p)
	if err != nil {
		fail(w, r, err, payoutMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, updated)
}

// ---------------------------------------------------------------------------
// Per-application permissions
// ---------------------------------------------------------------------------

type payoutLimitsRequest struct {
	Enabled          *bool  `json:"enabled"`
	RequiresApproval *bool  `json:"requires_approval"`
	MaxAmount        *int64 `json:"max_amount"`
	DailyLimit       *int64 `json:"daily_limit"`
	// ClearMaxAmount and ClearDailyLimit remove a ceiling. Omitting a limit
	// leaves it alone; removing one has to be asked for in words, because a
	// null that means "unlimited" is too easy to send by accident.
	ClearMaxAmount  bool `json:"clear_max_amount"`
	ClearDailyLimit bool `json:"clear_daily_limit"`
}

// handleGetPayoutLimits reports what an application is permitted to disburse.
func (s *Server) handleGetPayoutLimits(w http.ResponseWriter, r *http.Request) {
	limits, err := s.payouts.Repo().LimitsFor(r.Context(), chi.URLParam(r, "applicationID"))
	if err != nil {
		fail(w, r, err, payoutMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, payoutLimitsBody(limits))
}

// handleSetPayoutLimits changes what an application is permitted to disburse.
func (s *Server) handleSetPayoutLimits(w http.ResponseWriter, r *http.Request) {
	applicationID := chi.URLParam(r, "applicationID")

	var req payoutLimitsRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		fail(w, r, err, payoutMissing)
		return
	}

	current, err := s.payouts.Repo().LimitsFor(r.Context(), applicationID)
	if err != nil {
		fail(w, r, err, payoutMissing)
		return
	}

	next := current
	if req.Enabled != nil {
		next.Enabled = *req.Enabled
	}
	if req.RequiresApproval != nil {
		next.RequiresApproval = *req.RequiresApproval
	}
	if req.MaxAmount != nil {
		next.MaxAmount = req.MaxAmount
	}
	if req.ClearMaxAmount {
		next.MaxAmount = nil
	}
	if req.DailyLimit != nil {
		next.DailyLimit = req.DailyLimit
	}
	if req.ClearDailyLimit {
		next.DailyLimit = nil
	}

	if err := validateLimits(next); err != nil {
		fail(w, r, err, payoutMissing)
		return
	}
	if err := s.payouts.Repo().SetLimits(r.Context(), applicationID, next); err != nil {
		fail(w, r, err, payoutMissing)
		return
	}

	s.audit(r, "payout.limits.updated", "application", applicationID, map[string]any{
		"enabled":           next.Enabled,
		"requires_approval": next.RequiresApproval,
		"max_amount":        next.MaxAmount,
		"daily_limit":       next.DailyLimit,
	})
	httpx.JSON(w, r, http.StatusOK, payoutLimitsBody(next))
}

// validateLimits refuses a combination that would leave money unguarded.
func validateLimits(l payout.Limits) error {
	if !l.Enabled {
		return nil
	}
	if l.MaxAmount != nil && *l.MaxAmount <= 0 {
		return httpx.ErrInvalidRequest("max_amount must be positive.").
			WithField("max_amount", "must be greater than zero")
	}
	if l.DailyLimit != nil && *l.DailyLimit <= 0 {
		return httpx.ErrInvalidRequest("daily_limit must be positive.").
			WithField("daily_limit", "must be greater than zero")
	}
	if l.MaxAmount != nil && l.DailyLimit != nil && *l.MaxAmount > *l.DailyLimit {
		return httpx.ErrInvalidRequest(
			"max_amount cannot exceed daily_limit, or the daily limit would never apply.").
			WithField("max_amount", "must not exceed daily_limit")
	}
	// Turning off approval with no ceiling of any kind means an API key alone
	// can move an unbounded amount. That may be what someone wants, but it
	// should not be reachable by omission.
	if !l.RequiresApproval && l.MaxAmount == nil && l.DailyLimit == nil {
		return httpx.ErrInvalidRequest(
			"An application that disburses without approval needs a limit. "+
				"Set max_amount or daily_limit, or keep requires_approval on.").
			WithField("requires_approval", "needs a limit when turned off")
	}
	return nil
}

// payoutLimitsBody renders limits with nulls that read as "no ceiling".
func payoutLimitsBody(l payout.Limits) map[string]any {
	return map[string]any{
		"enabled":           l.Enabled,
		"requires_approval": l.RequiresApproval,
		"max_amount":        l.MaxAmount,
		"daily_limit":       l.DailyLimit,
	}
}

// handleGatewayBalance reports what an account can pay out.
//
// Read on request rather than polled: it costs a gateway call, it is only
// meaningful at the moment somebody looks, and nothing in PayMux acts on it.
func (s *Server) handleGatewayBalance(w http.ResponseWriter, r *http.Request) {
	balance, err := s.payouts.Balance(r.Context(), chi.URLParam(r, "accountID"))
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{
		"amount":   balance.Amount,
		"currency": balance.Currency,
	})
}

// handleAdminVerifyBeneficiary checks a destination from the dashboard.
func (s *Server) handleAdminVerifyBeneficiary(w http.ResponseWriter, r *http.Request) {
	applicationID := chi.URLParam(r, "applicationID")

	app, err := s.applications.Get(r.Context(), applicationID)
	if err != nil {
		fail(w, r, err, applicationMissing)
		return
	}
	account, err := s.payoutAccount(r.Context(), app)
	if err != nil {
		fail(w, r, err, beneficiaryMissing)
		return
	}

	b, err := s.payouts.VerifyBeneficiary(r.Context(), applicationID,
		chi.URLParam(r, "beneficiaryID"), account.ID)
	if err != nil {
		fail(w, r, err, beneficiaryMissing)
		return
	}
	s.audit(r, "beneficiary.verified", "beneficiary", b.ID, map[string]any{
		"account":      b.Account,
		"bank":         b.Bank,
		"name_on_file": b.Name,
		"name_at_bank": b.VerifiedName,
	})
	httpx.JSON(w, r, http.StatusOK, b)
}

// handleAdminListBeneficiaries lists one application's payout destinations.
func (s *Server) handleAdminListBeneficiaries(w http.ResponseWriter, r *http.Request) {
	list, err := s.payouts.Repo().ListBeneficiaries(r.Context(),
		chi.URLParam(r, "applicationID"), pageFromRequest(r))
	if err != nil {
		fail(w, r, err, payoutMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, list)
}
