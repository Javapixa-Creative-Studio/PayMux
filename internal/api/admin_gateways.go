package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/crypto"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/httpx"
)

type createGatewayAccountRequest struct {
	Gateway     string `json:"gateway"`
	Name        string `json:"name"`
	Environment string `json:"environment"`
	MerchantID  string `json:"merchant_id"`
	ClientKey   string `json:"client_key"`
	ServerKey   string `json:"server_key"`
	Enabled     *bool  `json:"enabled"`
	IsDefault   *bool  `json:"is_default"`
}

func (s *Server) handleCreateGatewayAccount(w http.ResponseWriter, r *http.Request) {
	var req createGatewayAccountRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if req.Gateway == "" {
		req.Gateway = "midtrans"
	}
	if !s.gateways.Supports(req.Gateway) {
		httpx.Fail(w, r, httpx.NewError(http.StatusBadRequest, httpx.CodeUnsupportedGateway,
			"No adapter is available for gateway "+req.Gateway+".").
			WithField("gateway", "is not supported"))
		return
	}
	env := gateway.Environment(req.Environment)
	if !env.Valid() {
		httpx.Fail(w, r, httpx.ErrValidation("The request contains invalid values.").
			WithField("environment", `must be "sandbox" or "production"`))
		return
	}
	if req.ServerKey == "" {
		httpx.Fail(w, r, httpx.ErrValidation("The request contains invalid values.").
			WithField("server_key", "must not be empty"))
		return
	}
	if req.Name == "" {
		httpx.Fail(w, r, httpx.ErrValidation("The request contains invalid values.").
			WithField("name", "must not be empty"))
		return
	}

	acc := &gateway.Account{
		Gateway:     req.Gateway,
		Name:        req.Name,
		Environment: env,
		MerchantID:  req.MerchantID,
		ClientKey:   req.ClientKey,
		ServerKey:   crypto.Secret(req.ServerKey),
		Enabled:     boolOr(req.Enabled, true),
		IsDefault:   boolOr(req.IsDefault, false),
	}
	if err := s.gatewayAccounts.Create(r.Context(), acc); err != nil {
		if isNameConflict(err) {
			httpx.Fail(w, r, httpx.ErrConflict(httpx.CodeConflict,
				"A gateway account with that name already exists."))
			return
		}
		fail(w, r, err, genericMissing)
		return
	}
	s.audit(r, "gateway_account.created", "gateway_account", acc.ID, map[string]any{
		"gateway":     acc.Gateway,
		"environment": string(acc.Environment),
	})
	httpx.JSON(w, r, http.StatusCreated, renderGatewayAccount(acc))
}

func (s *Server) handleListGatewayAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.gatewayAccounts.List(r.Context())
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, renderList(accounts, false, len(accounts), renderGatewayAccount))
}

func (s *Server) handleGetGatewayAccount(w http.ResponseWriter, r *http.Request) {
	acc, err := s.gatewayAccounts.Get(r.Context(), chi.URLParam(r, "accountID"))
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, renderGatewayAccount(acc))
}

type updateGatewayAccountRequest struct {
	Name        *string `json:"name"`
	MerchantID  *string `json:"merchant_id"`
	ClientKey   *string `json:"client_key"`
	ServerKey   *string `json:"server_key"`
	Environment *string `json:"environment"`
	Enabled     *bool   `json:"enabled"`
	IsDefault   *bool   `json:"is_default"`
}

func (s *Server) handleUpdateGatewayAccount(w http.ResponseWriter, r *http.Request) {
	var req updateGatewayAccountRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	update := gateway.AccountUpdate{
		Name:       req.Name,
		MerchantID: req.MerchantID,
		ClientKey:  req.ClientKey,
		Enabled:    req.Enabled,
		IsDefault:  req.IsDefault,
	}
	if req.ServerKey != nil {
		if *req.ServerKey == "" {
			httpx.Fail(w, r, httpx.ErrValidation("The request contains invalid values.").
				WithField("server_key", "must not be empty"))
			return
		}
		update.ServerKey = crypto.Secret(*req.ServerKey)
	}
	if req.Environment != nil {
		env := gateway.Environment(*req.Environment)
		if !env.Valid() {
			httpx.Fail(w, r, httpx.ErrValidation("The request contains invalid values.").
				WithField("environment", `must be "sandbox" or "production"`))
			return
		}
		update.Environment = &env
	}

	acc, err := s.gatewayAccounts.Update(r.Context(), chi.URLParam(r, "accountID"), update)
	if err != nil {
		if isNameConflict(err) {
			httpx.Fail(w, r, httpx.ErrConflict(httpx.CodeConflict,
				"A gateway account with that name already exists."))
			return
		}
		fail(w, r, err, genericMissing)
		return
	}
	s.audit(r, "gateway_account.updated", "gateway_account", acc.ID, nil)
	httpx.JSON(w, r, http.StatusOK, renderGatewayAccount(acc))
}

func (s *Server) handleDeleteGatewayAccount(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "accountID")
	if err := s.gatewayAccounts.Delete(r.Context(), id); err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	s.audit(r, "gateway_account.deleted", "gateway_account", id, nil)
	httpx.NoContent(w)
}

type gatewayListEntry struct {
	Name string `json:"name"`
}

// handleListGateways reports which gateway adapters this build supports.
func (s *Server) handleListGateways(w http.ResponseWriter, r *http.Request) {
	names := s.gateways.Names()
	entries := make([]gatewayListEntry, 0, len(names))
	for _, name := range names {
		entries = append(entries, gatewayListEntry{Name: name})
	}
	httpx.JSON(w, r, http.StatusOK, listResponse[gatewayListEntry]{
		Data: entries, HasMore: false, Limit: len(entries),
	})
}

func boolOr(v *bool, def bool) bool {
	if v == nil {
		return def
	}
	return *v
}

func isNameConflict(err error) bool {
	return storageIsUnique(err, gateway.ConstraintNameUnique)
}
