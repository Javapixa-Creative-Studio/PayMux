package api

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/auth"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/httpx"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/subscription"
)

type subscriptionResponse struct {
	ID                    string         `json:"id"`
	Object                string         `json:"object"`
	ApplicationID         string         `json:"application_id"`
	Gateway               string         `json:"gateway"`
	GatewaySubscriptionID string         `json:"gateway_subscription_id,omitempty"`
	Name                  string         `json:"name"`
	Amount                int64          `json:"amount"`
	Currency              string         `json:"currency"`
	Status                string         `json:"status"`
	GatewayStatus         string         `json:"gateway_status,omitempty"`
	IntervalUnit          string         `json:"interval_unit"`
	IntervalCount         int            `json:"interval_count"`
	MaxInterval           *int           `json:"max_interval,omitempty"`
	StartTime             *time.Time     `json:"start_time,omitempty"`
	PaymentType           string         `json:"payment_type,omitempty"`
	Metadata              map[string]any `json:"metadata"`
	CreatedAt             time.Time      `json:"created_at"`
	UpdatedAt             time.Time      `json:"updated_at"`
}

func renderSubscription(s *subscription.Subscription) subscriptionResponse {
	metadata := s.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	// The payment token is deliberately absent: it is a credential that can
	// charge the customer, and an application already holds its own copy.
	return subscriptionResponse{
		ID:                    s.ID,
		Object:                "subscription",
		ApplicationID:         s.ApplicationID,
		Gateway:               s.Gateway,
		GatewaySubscriptionID: s.GatewaySubscriptionID,
		Name:                  s.Name,
		Amount:                s.Amount,
		Currency:              s.Currency,
		Status:                string(s.Status),
		GatewayStatus:         s.GatewayStatus,
		IntervalUnit:          s.IntervalUnit,
		IntervalCount:         s.IntervalCount,
		MaxInterval:           s.MaxInterval,
		StartTime:             s.StartTime,
		PaymentType:           s.PaymentType,
		Metadata:              metadata,
		CreatedAt:             s.CreatedAt,
		UpdatedAt:             s.UpdatedAt,
	}
}

type createSubscriptionRequest struct {
	Name           string           `json:"name"`
	Amount         int64            `json:"amount"`
	Currency       string           `json:"currency"`
	PaymentType    string           `json:"payment_type"`
	PaymentToken   string           `json:"payment_token"`
	IntervalUnit   string           `json:"interval_unit"`
	IntervalCount  int              `json:"interval_count"`
	MaxInterval    int              `json:"max_interval"`
	StartTime      *time.Time       `json:"start_time"`
	Customer       *customerRequest `json:"customer"`
	Metadata       map[string]any   `json:"metadata"`
	GatewayOptions gatewayOptions   `json:"gateway_options"`
}

func (s *Server) handleCreateSubscription(w http.ResponseWriter, r *http.Request) {
	app := auth.ApplicationFromContext(r.Context())

	var req createSubscriptionRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	created, err := s.subscriptions.Create(r.Context(), subscription.CreateInput{
		Application:   app,
		Name:          req.Name,
		Amount:        req.Amount,
		Currency:      req.Currency,
		PaymentType:   req.PaymentType,
		PaymentToken:  req.PaymentToken,
		IntervalUnit:  req.IntervalUnit,
		IntervalCount: req.IntervalCount,
		MaxInterval:   req.MaxInterval,
		StartTime:     req.StartTime,
		Customer:      toGatewayCustomer(req.Customer),
		Metadata:      req.Metadata,
		Options:       req.GatewayOptions.Midtrans,
	})
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, renderSubscription(created))
}

func (s *Server) handleGetSubscription(w http.ResponseWriter, r *http.Request) {
	sub, err := s.subscriptions.Get(r.Context(), applicationScope(r), chi.URLParam(r, "subscriptionID"))
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, renderSubscription(sub))
}

func (s *Server) handleListSubscriptions(w http.ResponseWriter, r *http.Request) {
	scope := applicationScope(r)
	if scope == "" {
		// An administrator may narrow to one application explicitly.
		scope = r.URL.Query().Get("application_id")
	}
	list, err := s.subscriptions.List(r.Context(), scope, pageFromRequest(r))
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, renderList(list.Items, list.HasMore, list.Limit, renderSubscription))
}

type updateSubscriptionRequest struct {
	Name           *string        `json:"name"`
	Amount         *int64         `json:"amount"`
	IntervalUnit   *string        `json:"interval_unit"`
	IntervalCount  *int           `json:"interval_count"`
	PaymentToken   *string        `json:"payment_token"`
	GatewayOptions gatewayOptions `json:"gateway_options"`
}

func (s *Server) handleUpdateSubscription(w http.ResponseWriter, r *http.Request) {
	var req updateSubscriptionRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	updated, err := s.subscriptions.Update(r.Context(), applicationScope(r), chi.URLParam(r, "subscriptionID"),
		subscription.UpdateInput{
			Name:          req.Name,
			Amount:        req.Amount,
			IntervalUnit:  req.IntervalUnit,
			IntervalCount: req.IntervalCount,
			PaymentToken:  req.PaymentToken,
			Options:       req.GatewayOptions.Midtrans,
		})
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, renderSubscription(updated))
}

func (s *Server) handleEnableSubscription(w http.ResponseWriter, r *http.Request) {
	s.subscriptionAction(w, r, s.subscriptions.Enable)
}

func (s *Server) handleDisableSubscription(w http.ResponseWriter, r *http.Request) {
	s.subscriptionAction(w, r, s.subscriptions.Disable)
}

func (s *Server) handleCancelSubscription(w http.ResponseWriter, r *http.Request) {
	s.subscriptionAction(w, r, s.subscriptions.Cancel)
}

func (s *Server) handleSyncSubscription(w http.ResponseWriter, r *http.Request) {
	s.subscriptionAction(w, r, s.subscriptions.Sync)
}

type subscriptionOperation func(ctx context.Context, applicationID, id string) (*subscription.Subscription, error)

func (s *Server) subscriptionAction(w http.ResponseWriter, r *http.Request, op subscriptionOperation) {
	updated, err := op(r.Context(), applicationScope(r), chi.URLParam(r, "subscriptionID"))
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, renderSubscription(updated))
}

// applicationScope returns the application a request is limited to, or "" for
// an administrator, who sees every application.
func applicationScope(r *http.Request) string {
	if app := auth.ApplicationFromContext(r.Context()); app != nil {
		return app.ID
	}
	return ""
}

// toGatewayCustomer converts an API customer into the gateway's shape.
func toGatewayCustomer(c *customerRequest) gateway.Customer {
	if c == nil {
		return gateway.Customer{}
	}
	out := gateway.Customer{
		FirstName: c.FirstName,
		LastName:  c.LastName,
		Email:     c.Email,
		Phone:     c.Phone,
	}
	if c.Billing != nil {
		out.Billing = toGatewayAddress(c.Billing)
	}
	if c.Shipping != nil {
		out.Shipping = toGatewayAddress(c.Shipping)
	}
	return out
}

func toGatewayAddress(a *addressRequest) *gateway.Address {
	return &gateway.Address{
		FirstName:   a.FirstName,
		LastName:    a.LastName,
		Phone:       a.Phone,
		Address:     a.Address,
		City:        a.City,
		PostalCode:  a.PostalCode,
		CountryCode: a.CountryCode,
	}
}

// capabilitiesResponse reports what a gateway account can do (PRD §85).
type capabilitiesResponse struct {
	Gateway      string               `json:"gateway"`
	Environment  string               `json:"environment"`
	Capabilities gateway.Capabilities `json:"capabilities"`
}

// handleCapabilities lets an application discover which operations are
// available before it tries them.
func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	account, err := s.gatewayAccounts.Default(r.Context(), "midtrans")
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	adapter, err := s.gateways.For(account)
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, capabilitiesResponse{
		Gateway:      account.Gateway,
		Environment:  string(account.Environment),
		Capabilities: gateway.CapabilitiesFor(adapter),
	})
}
