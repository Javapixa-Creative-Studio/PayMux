package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/auth"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/delivery"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/httpx"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/payment"
)

// HeaderIdempotencyKey carries a caller's idempotency key (PRD §63).
const HeaderIdempotencyKey = "Idempotency-Key"

type createPaymentRequest struct {
	ApplicationOrderID    string           `json:"application_order_id"`
	Amount                int64            `json:"amount"`
	Currency              string           `json:"currency"`
	Customer              *customerRequest `json:"customer"`
	Items                 []itemRequest    `json:"items"`
	EnabledPaymentMethods []string         `json:"enabled_payment_methods"`
	ExpiresAt             *time.Time       `json:"expires_at"`
	CallbackURL           string           `json:"callback_url"`
	CustomFields          []string         `json:"custom_fields"`
	Metadata              map[string]any   `json:"metadata"`
	Gateway               string           `json:"gateway"`
	GatewayOptions        gatewayOptions   `json:"gateway_options"`
}

// gatewayOptions keeps gateway-specific parameters namespaced by gateway, so
// a request written for one gateway cannot be misread by another (PRD §18).
type gatewayOptions struct {
	Midtrans json.RawMessage `json:"midtrans,omitempty"`
}

type customerRequest struct {
	FirstName string          `json:"first_name"`
	LastName  string          `json:"last_name"`
	Email     string          `json:"email"`
	Phone     string          `json:"phone"`
	Billing   *addressRequest `json:"billing_address"`
	Shipping  *addressRequest `json:"shipping_address"`
}

type addressRequest struct {
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	Phone       string `json:"phone"`
	Address     string `json:"address"`
	City        string `json:"city"`
	PostalCode  string `json:"postal_code"`
	CountryCode string `json:"country_code"`
}

type itemRequest struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Price    int64  `json:"price"`
	Quantity int    `json:"quantity"`
	Category string `json:"category"`
	Merchant string `json:"merchant_name"`
	URL      string `json:"url"`
}

// handleCreatePayment opens a payment for the authenticated application.
//
// The application is resolved from the API key, never from the request body:
// a caller cannot create a payment that belongs to somebody else (PRD §91
// rule 13).
func (s *Server) handleCreatePayment(w http.ResponseWriter, r *http.Request) {
	app := auth.ApplicationFromContext(r.Context())
	apiKey := auth.APIKeyFromContext(r.Context())

	var req createPaymentRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if req.Gateway != "" && req.Gateway != "midtrans" {
		httpx.Fail(w, r, httpx.NewError(http.StatusBadRequest, httpx.CodeUnsupportedGateway,
			"This PayMux instance supports the midtrans gateway.").
			WithField("gateway", "is not supported"))
		return
	}

	idempotencyKey := r.Header.Get(HeaderIdempotencyKey)
	if len(idempotencyKey) > 255 {
		httpx.Fail(w, r, httpx.ErrInvalidRequest("Idempotency-Key must be at most 255 characters."))
		return
	}

	// With a key present, the first caller does the work and later callers
	// replay its result rather than opening a second gateway transaction.
	if idempotencyKey != "" {
		requestHash, err := payment.HashRequest(req)
		if err != nil {
			httpx.Fail(w, r, httpx.ErrInternal(err))
			return
		}
		_, err = s.idempotency.Claim(r.Context(), app.ID, "POST /api/v1/payments", idempotencyKey, requestHash)
		if err != nil {
			if record := payment.Replay(err); record != nil {
				replayStored(w, r, record)
				return
			}
			fail(w, r, err, paymentMissing)
			return
		}
	}

	created, err := s.payments.Create(r.Context(), payment.CreateInput{
		Application:           app,
		KeyMode:               apiKey.Mode,
		ApplicationOrderID:    req.ApplicationOrderID,
		Amount:                req.Amount,
		Currency:              req.Currency,
		Customer:              toDomainCustomer(req.Customer),
		Items:                 toDomainItems(req.Items),
		EnabledPaymentMethods: req.EnabledPaymentMethods,
		ExpiresAt:             req.ExpiresAt,
		CallbackURL:           req.CallbackURL,
		CustomFields:          req.CustomFields,
		Metadata:              req.Metadata,
		GatewayOptions:        req.GatewayOptions.Midtrans,
	})
	if err != nil {
		// The work failed, so the claim is released and the caller may retry
		// with the same key.
		if idempotencyKey != "" {
			if releaseErr := s.idempotency.Release(r.Context(), app.ID, "POST /api/v1/payments", idempotencyKey); releaseErr != nil {
				s.logger.Error("could not release an idempotency claim",
					"application_id", app.ID, "error", releaseErr)
			}
		}
		fail(w, r, err, paymentMissing)
		return
	}

	response := renderPayment(created)
	if idempotencyKey != "" {
		if err := s.idempotency.Complete(r.Context(), app.ID, "POST /api/v1/payments",
			idempotencyKey, created.ID, http.StatusCreated, response); err != nil {
			s.logger.Error("could not record an idempotent response",
				"payment_id", created.ID, "error", err)
		}
	}
	httpx.JSON(w, r, http.StatusCreated, response)
}

// replayStored returns the response a previous identical request produced.
func replayStored(w http.ResponseWriter, r *http.Request, record *payment.IdempotencyRecord) {
	status := record.ResponseStatus
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Idempotent-Replay", "true")
	w.WriteHeader(status)
	if len(record.ResponseBody) > 0 {
		_, _ = w.Write(record.ResponseBody)
	}
}

func (s *Server) handleGetPayment(w http.ResponseWriter, r *http.Request) {
	app := auth.ApplicationFromContext(r.Context())
	p, err := s.payments.Get(r.Context(), app.ID, chi.URLParam(r, "paymentID"))
	if err != nil {
		fail(w, r, err, paymentMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, renderPayment(p))
}

func (s *Server) handleListPayments(w http.ResponseWriter, r *http.Request) {
	app := auth.ApplicationFromContext(r.Context())
	filter := paymentFilterFromRequest(r)
	// An application only ever sees its own payments, whatever it asks for.
	filter.ApplicationID = app.ID

	list, err := s.payments.List(r.Context(), filter, pageFromRequest(r))
	if err != nil {
		fail(w, r, err, paymentMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, renderList(list.Items, list.HasMore, list.Limit, renderPayment))
}

func (s *Server) handleSyncPayment(w http.ResponseWriter, r *http.Request) {
	s.paymentAction(w, r, s.payments.Sync)
}

func (s *Server) handleCancelPayment(w http.ResponseWriter, r *http.Request) {
	s.paymentAction(w, r, s.payments.Cancel)
}

func (s *Server) handleExpirePayment(w http.ResponseWriter, r *http.Request) {
	s.paymentAction(w, r, s.payments.Expire)
}

func (s *Server) handleCancelSnapSession(w http.ResponseWriter, r *http.Request) {
	s.paymentAction(w, r, s.payments.CancelCheckoutSession)
}

// paymentOperation is an action that acts on one payment and returns it.
type paymentOperation func(ctx context.Context, applicationID, paymentID string) (*payment.Payment, error)

func (s *Server) paymentAction(w http.ResponseWriter, r *http.Request, op paymentOperation) {
	app := auth.ApplicationFromContext(r.Context())
	applicationID := ""
	if app != nil {
		applicationID = app.ID
	}
	updated, err := op(r.Context(), applicationID, chi.URLParam(r, "paymentID"))
	if err != nil {
		fail(w, r, err, paymentMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, renderPayment(updated))
}

type createRefundRequest struct {
	Amount int64  `json:"amount"`
	Reason string `json:"reason"`
}

func (s *Server) handleCreateRefund(w http.ResponseWriter, r *http.Request) {
	app := auth.ApplicationFromContext(r.Context())

	var req createRefundRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	applicationID := ""
	if app != nil {
		applicationID = app.ID
	}
	refund, _, err := s.payments.Refund(r.Context(), payment.RefundInput{
		ApplicationID: applicationID,
		PaymentID:     chi.URLParam(r, "paymentID"),
		Amount:        req.Amount,
		Reason:        req.Reason,
		// A caller's idempotency key doubles as the refund key, so a retried
		// refund request cannot refund twice.
		RefundKey: r.Header.Get(HeaderIdempotencyKey),
	})
	if err != nil {
		fail(w, r, err, paymentMissing)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, renderRefund(refund))
}

func (s *Server) handleListRefunds(w http.ResponseWriter, r *http.Request) {
	app := auth.ApplicationFromContext(r.Context())
	applicationID := ""
	if app != nil {
		applicationID = app.ID
	}
	refunds, err := s.payments.ListRefunds(r.Context(), applicationID, chi.URLParam(r, "paymentID"))
	if err != nil {
		fail(w, r, err, paymentMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, renderList(refunds, false, len(refunds), renderRefund))
}

func (s *Server) handleListApplicationEvents(w http.ResponseWriter, r *http.Request) {
	app := auth.ApplicationFromContext(r.Context())
	filter := eventFilterFromRequest(r)
	filter.ApplicationID = app.ID

	list, err := s.events.List(r.Context(), filter, pageFromRequest(r))
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, renderList(list.Items, list.HasMore, list.Limit, renderEvent))
}

func (s *Server) handleListApplicationDeliveries(w http.ResponseWriter, r *http.Request) {
	app := auth.ApplicationFromContext(r.Context())
	filter := deliveryFilterFromRequest(r)
	filter.ApplicationID = app.ID

	list, err := s.deliveries.List(r.Context(), filter, pageFromRequest(r))
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, renderList(list.Items, list.HasMore, list.Limit, renderDelivery))
}

// handleRetryDelivery re-queues a delivery (PRD §47).
func (s *Server) handleRetryDelivery(w http.ResponseWriter, r *http.Request) {
	app := auth.ApplicationFromContext(r.Context())
	deliveryID := chi.URLParam(r, "deliveryID")

	existing, err := s.deliveries.Get(r.Context(), deliveryID)
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	// An application may only retry its own deliveries; an administrator any.
	if app != nil && existing.ApplicationID != app.ID {
		httpx.Fail(w, r, httpx.ErrNotFound(httpx.CodeNotFound, "The requested resource was not found."))
		return
	}

	retried, err := s.deliveries.Retry(r.Context(), deliveryID)
	if err != nil {
		if errors.Is(err, delivery.ErrInFlight) {
			httpx.Fail(w, r, httpx.ErrConflict(httpx.CodeConflict,
				"This delivery is being attempted right now."))
			return
		}
		fail(w, r, err, genericMissing)
		return
	}
	s.audit(r, "delivery.retried", "delivery", retried.ID, nil)
	httpx.JSON(w, r, http.StatusOK, renderDelivery(retried))
}

func toDomainCustomer(c *customerRequest) *payment.Customer {
	if c == nil {
		return nil
	}
	out := &payment.Customer{
		FirstName: c.FirstName,
		LastName:  c.LastName,
		Email:     c.Email,
		Phone:     c.Phone,
	}
	if c.Billing != nil {
		out.Billing = toDomainAddress(c.Billing)
	}
	if c.Shipping != nil {
		out.Shipping = toDomainAddress(c.Shipping)
	}
	return out
}

func toDomainAddress(a *addressRequest) *payment.Address {
	return &payment.Address{
		FirstName:   a.FirstName,
		LastName:    a.LastName,
		Phone:       a.Phone,
		Address:     a.Address,
		City:        a.City,
		PostalCode:  a.PostalCode,
		CountryCode: a.CountryCode,
	}
}

func toDomainItems(items []itemRequest) []payment.Item {
	if len(items) == 0 {
		return nil
	}
	out := make([]payment.Item, 0, len(items))
	for _, item := range items {
		out = append(out, payment.Item{
			SKU:      item.ID,
			Name:     item.Name,
			Price:    item.Price,
			Quantity: item.Quantity,
			Category: item.Category,
			Merchant: item.Merchant,
			URL:      item.URL,
		})
	}
	return out
}
