package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/httpx"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/notification"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/payment"
)

// The administrator views span every application, so these handlers pass no
// application filter unless the operator asked for one.

func (s *Server) handleAdminListPayments(w http.ResponseWriter, r *http.Request) {
	list, err := s.payments.List(r.Context(), paymentFilterFromRequest(r), pageFromRequest(r))
	if err != nil {
		fail(w, r, err, paymentMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, renderList(list.Items, list.HasMore, list.Limit, renderPayment))
}

// paymentDetailResponse is the payment detail view the dashboard renders
// (PRD §55). It gathers everything about one payment in a single request so
// an operator investigating a problem does not have to stitch it together.
type paymentDetailResponse struct {
	Payment       paymentResponse        `json:"payment"`
	Refunds       []refundResponse       `json:"refunds"`
	Events        []eventResponse        `json:"events"`
	Deliveries    []deliveryResponse     `json:"deliveries"`
	GatewayEvents []gatewayEventResponse `json:"gateway_events"`
}

func (s *Server) handleAdminGetPayment(w http.ResponseWriter, r *http.Request) {
	paymentID := chi.URLParam(r, "paymentID")

	p, err := s.payments.GetAny(r.Context(), paymentID)
	if err != nil {
		fail(w, r, err, paymentMissing)
		return
	}

	detail := paymentDetailResponse{Payment: renderPayment(p)}

	refunds, err := s.payments.ListRefunds(r.Context(), "", paymentID)
	if err != nil {
		fail(w, r, err, paymentMissing)
		return
	}
	for _, refund := range refunds {
		detail.Refunds = append(detail.Refunds, renderRefund(refund))
	}

	events, err := s.events.ListForPayment(r.Context(), paymentID)
	if err != nil {
		fail(w, r, err, paymentMissing)
		return
	}
	for _, e := range events {
		detail.Events = append(detail.Events, renderEvent(e))
	}

	deliveries, err := s.deliveries.List(r.Context(),
		deliveryFilterFromRequest(r), pageFromRequest(r))
	if err != nil {
		fail(w, r, err, paymentMissing)
		return
	}
	for _, d := range deliveries.Items {
		// Only this payment's deliveries belong in its detail view.
		for _, e := range events {
			if d.EventID == e.ID {
				detail.Deliveries = append(detail.Deliveries, renderDelivery(d))
				break
			}
		}
	}

	gatewayEvents, err := s.notificationRepo.List(r.Context(),
		notificationFilterForPayment(paymentID), pageFromRequest(r))
	if err != nil {
		fail(w, r, err, paymentMissing)
		return
	}
	for _, e := range gatewayEvents.Items {
		detail.GatewayEvents = append(detail.GatewayEvents, renderGatewayEvent(e))
	}

	httpx.JSON(w, r, http.StatusOK, detail)
}

// handleAdminListRefunds lists refunds across every payment (PRD §51).
func (s *Server) handleAdminListRefunds(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := payment.RefundFilter{
		ApplicationID: q.Get("application_id"),
		PaymentID:     q.Get("payment_id"),
	}
	switch status := gateway.RefundStatus(q.Get("status")); status {
	case gateway.RefundPending, gateway.RefundSucceeded, gateway.RefundFailed:
		filter.Status = status
	}

	list, err := s.payments.ListAllRefunds(r.Context(), filter, pageFromRequest(r))
	if err != nil {
		fail(w, r, err, refundMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, renderList(list.Items, list.HasMore, list.Limit, renderRefund))
}

func (s *Server) handleAdminListEvents(w http.ResponseWriter, r *http.Request) {
	list, err := s.events.List(r.Context(), eventFilterFromRequest(r), pageFromRequest(r))
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, renderList(list.Items, list.HasMore, list.Limit, renderEvent))
}

func (s *Server) handleAdminGetEvent(w http.ResponseWriter, r *http.Request) {
	e, err := s.events.Get(r.Context(), chi.URLParam(r, "eventID"))
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, renderEvent(e))
}

func (s *Server) handleAdminListDeliveries(w http.ResponseWriter, r *http.Request) {
	list, err := s.deliveries.List(r.Context(), deliveryFilterFromRequest(r), pageFromRequest(r))
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, renderList(list.Items, list.HasMore, list.Limit, renderDelivery))
}

type deliveryDetailResponse struct {
	Delivery deliveryResponse  `json:"delivery"`
	Attempts []attemptResponse `json:"attempts"`
}

func (s *Server) handleAdminGetDelivery(w http.ResponseWriter, r *http.Request) {
	deliveryID := chi.URLParam(r, "deliveryID")

	d, err := s.deliveries.Get(r.Context(), deliveryID)
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	attempts, err := s.deliveries.ListAttempts(r.Context(), deliveryID)
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}

	detail := deliveryDetailResponse{Delivery: renderDelivery(d)}
	for _, attempt := range attempts {
		detail.Attempts = append(detail.Attempts, renderAttempt(attempt))
	}
	httpx.JSON(w, r, http.StatusOK, detail)
}

func (s *Server) handleAdminListGatewayEvents(w http.ResponseWriter, r *http.Request) {
	list, err := s.notificationRepo.List(r.Context(), notificationFilterFromRequest(r), pageFromRequest(r))
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	httpx.JSON(w, r, http.StatusOK, renderList(list.Items, list.HasMore, list.Limit, renderGatewayEvent))
}

// Administrator payment actions mirror the application API, but without the
// ownership filter (PRD §55).

func (s *Server) handleAdminSyncPayment(w http.ResponseWriter, r *http.Request) {
	s.paymentAction(w, r, s.payments.Sync)
}

func (s *Server) handleAdminCancelPayment(w http.ResponseWriter, r *http.Request) {
	s.paymentAction(w, r, s.payments.Cancel)
}

func (s *Server) handleAdminExpirePayment(w http.ResponseWriter, r *http.Request) {
	s.paymentAction(w, r, s.payments.Expire)
}

// overviewResponse is the dashboard's operational summary (PRD §52).
type overviewResponse struct {
	Window     string             `json:"window"`
	Payments   paymentTotals      `json:"payments"`
	Deliveries deliveryTotals     `json:"deliveries"`
	Unrouted   int64              `json:"unrouted_notifications"`
	Currencies []currencySubtotal `json:"currency_totals"`
}

type paymentTotals struct {
	Created int64 `json:"created"`
	Paid    int64 `json:"paid"`
	Pending int64 `json:"pending"`
	Failed  int64 `json:"failed"`
}

type deliveryTotals struct {
	Pending   int64 `json:"pending"`
	Failed    int64 `json:"failed"`
	Succeeded int64 `json:"succeeded"`
	Dead      int64 `json:"dead"`
}

type currencySubtotal struct {
	Currency  string `json:"currency"`
	PaidTotal int64  `json:"paid_total"`
	Count     int64  `json:"count"`
}

func (s *Server) handleAdminOverview(w http.ResponseWriter, r *http.Request) {
	since := time.Now().Add(-24 * time.Hour)
	if window := r.URL.Query().Get("window"); window != "" {
		if d, err := time.ParseDuration(window); err == nil && d > 0 {
			since = time.Now().Add(-d)
		}
	}

	stats, err := s.paymentRepo.Stats(r.Context(), since)
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	deliveryStats, err := s.deliveries.Stats(r.Context(), since)
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}
	unrouted, err := s.notificationRepo.CountUnrouted(r.Context(), since)
	if err != nil {
		fail(w, r, err, genericMissing)
		return
	}

	response := overviewResponse{
		Window: time.Since(since).Round(time.Minute).String(),
		Payments: paymentTotals{
			Created: stats.Created,
			Paid:    stats.Paid,
			Pending: stats.Pending,
			Failed:  stats.Failed,
		},
		Deliveries: deliveryTotals{
			Pending:   deliveryStats.Pending,
			Failed:    deliveryStats.Failed,
			Succeeded: deliveryStats.Succeeded,
			Dead:      deliveryStats.Dead,
		},
		Unrouted: unrouted,
	}
	for _, total := range stats.Currencies {
		response.Currencies = append(response.Currencies, currencySubtotal{
			Currency:  total.Currency,
			PaidTotal: total.PaidTotal,
			Count:     total.Count,
		})
	}
	httpx.JSON(w, r, http.StatusOK, response)
}

// notificationFilterForPayment narrows gateway events to one payment.
func notificationFilterForPayment(paymentID string) notification.Filter {
	return notification.Filter{PaymentID: paymentID}
}
