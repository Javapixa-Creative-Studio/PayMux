package api

import (
	"time"

	"github.com/anggapixa/paymux/internal/delivery"
	"github.com/anggapixa/paymux/internal/event"
	"github.com/anggapixa/paymux/internal/notification"
	"github.com/anggapixa/paymux/internal/payment"
)

type paymentResponse struct {
	ID                   string            `json:"id"`
	Object               string            `json:"object"`
	ApplicationID        string            `json:"application_id"`
	Gateway              string            `json:"gateway"`
	ApplicationOrderID   string            `json:"application_order_id"`
	GatewayOrderID       string            `json:"gateway_order_id"`
	GatewayTransactionID string            `json:"gateway_transaction_id,omitempty"`
	Amount               int64             `json:"amount"`
	Currency             string            `json:"currency"`
	Status               string            `json:"status"`
	GatewayStatus        string            `json:"gateway_status,omitempty"`
	FraudStatus          string            `json:"fraud_status,omitempty"`
	PaymentType          string            `json:"payment_type,omitempty"`
	PaymentMethod        string            `json:"payment_method,omitempty"`
	SnapToken            string            `json:"snap_token,omitempty"`
	RedirectURL          string            `json:"redirect_url,omitempty"`
	RefundedAmount       int64             `json:"refunded_amount"`
	RefundableAmount     int64             `json:"refundable_amount"`
	Metadata             map[string]any    `json:"metadata"`
	Customer             *customerResponse `json:"customer,omitempty"`
	Items                []itemResponse    `json:"items,omitempty"`
	ExpiresAt            *time.Time        `json:"expires_at"`
	PaidAt               *time.Time        `json:"paid_at,omitempty"`
	CanceledAt           *time.Time        `json:"canceled_at,omitempty"`
	ExpiredAt            *time.Time        `json:"expired_at,omitempty"`
	CreatedAt            time.Time         `json:"created_at"`
	UpdatedAt            time.Time         `json:"updated_at"`
}

type customerResponse struct {
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Email     string `json:"email,omitempty"`
	Phone     string `json:"phone,omitempty"`
}

type itemResponse struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name"`
	Price    int64  `json:"price"`
	Quantity int    `json:"quantity"`
	Category string `json:"category,omitempty"`
}

func renderPayment(p *payment.Payment) paymentResponse {
	metadata := p.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	out := paymentResponse{
		ID:                   p.ID,
		Object:               "payment",
		ApplicationID:        p.ApplicationID,
		Gateway:              p.Gateway,
		ApplicationOrderID:   p.ApplicationOrderID,
		GatewayOrderID:       p.GatewayOrderID,
		GatewayTransactionID: p.GatewayTransactionID,
		Amount:               p.Amount,
		Currency:             p.Currency,
		Status:               string(p.NormalizedStatus),
		GatewayStatus:        p.GatewayStatus,
		FraudStatus:          p.FraudStatus,
		PaymentType:          p.PaymentType,
		PaymentMethod:        p.PaymentMethod,
		SnapToken:            p.SnapToken,
		RedirectURL:          p.SnapRedirectURL,
		RefundedAmount:       p.RefundedAmount,
		RefundableAmount:     p.RefundableAmount(),
		Metadata:             metadata,
		ExpiresAt:            p.ExpiresAt,
		PaidAt:               p.PaidAt,
		CanceledAt:           p.CanceledAt,
		ExpiredAt:            p.ExpiredAt,
		CreatedAt:            p.CreatedAt,
		UpdatedAt:            p.UpdatedAt,
	}
	if p.Customer != nil {
		out.Customer = &customerResponse{
			FirstName: p.Customer.FirstName,
			LastName:  p.Customer.LastName,
			Email:     p.Customer.Email,
			Phone:     p.Customer.Phone,
		}
	}
	for _, item := range p.Items {
		out.Items = append(out.Items, itemResponse{
			ID:       item.SKU,
			Name:     item.Name,
			Price:    item.Price,
			Quantity: item.Quantity,
			Category: item.Category,
		})
	}
	return out
}

type refundResponse struct {
	ID              string    `json:"id"`
	Object          string    `json:"object"`
	PaymentID       string    `json:"payment_id"`
	GatewayRefundID string    `json:"gateway_refund_id,omitempty"`
	Amount          int64     `json:"amount"`
	Currency        string    `json:"currency"`
	Reason          string    `json:"reason,omitempty"`
	Status          string    `json:"status"`
	GatewayStatus   string    `json:"gateway_status,omitempty"`
	FailureReason   string    `json:"failure_reason,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func renderRefund(r *payment.Refund) refundResponse {
	return refundResponse{
		ID:              r.ID,
		Object:          "refund",
		PaymentID:       r.PaymentID,
		GatewayRefundID: r.GatewayRefundID,
		Amount:          r.Amount,
		Currency:        r.Currency,
		Reason:          r.Reason,
		Status:          string(r.Status),
		GatewayStatus:   r.GatewayStatus,
		FailureReason:   r.FailureReason,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
	}
}

type eventResponse struct {
	ID             string        `json:"id"`
	Object         string        `json:"object"`
	Type           string        `json:"type"`
	Gateway        string        `json:"gateway"`
	ApplicationID  string        `json:"application_id"`
	PaymentID      string        `json:"payment_id,omitempty"`
	RefundID       string        `json:"refund_id,omitempty"`
	GatewayEventID string        `json:"gateway_event_id,omitempty"`
	Data           event.Payload `json:"data"`
	CreatedAt      time.Time     `json:"created_at"`
}

func renderEvent(e *event.Event) eventResponse {
	return eventResponse{
		ID:             e.ID,
		Object:         "event",
		Type:           string(e.Type),
		Gateway:        e.Gateway,
		ApplicationID:  e.ApplicationID,
		PaymentID:      e.PaymentID,
		RefundID:       e.RefundID,
		GatewayEventID: e.GatewayEventID,
		Data:           e.Payload,
		CreatedAt:      e.CreatedAt,
	}
}

type deliveryResponse struct {
	ID             string     `json:"id"`
	Object         string     `json:"object"`
	EventID        string     `json:"event_id"`
	ApplicationID  string     `json:"application_id"`
	DestinationID  string     `json:"destination_id"`
	URL            string     `json:"url"`
	State          string     `json:"state"`
	AttemptCount   int        `json:"attempt_count"`
	MaxAttempts    int        `json:"max_attempts"`
	NextAttemptAt  *time.Time `json:"next_attempt_at,omitempty"`
	LastAttemptAt  *time.Time `json:"last_attempt_at,omitempty"`
	LastStatusCode *int       `json:"last_status_code,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	LastDurationMS *int       `json:"last_duration_ms,omitempty"`
	SucceededAt    *time.Time `json:"succeeded_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

func renderDelivery(d *delivery.Delivery) deliveryResponse {
	out := deliveryResponse{
		ID:             d.ID,
		Object:         "delivery",
		EventID:        d.EventID,
		ApplicationID:  d.ApplicationID,
		DestinationID:  d.DestinationID,
		URL:            d.URL,
		State:          string(d.State),
		AttemptCount:   d.AttemptCount,
		MaxAttempts:    d.MaxAttempts,
		LastAttemptAt:  d.LastAttemptAt,
		LastStatusCode: d.LastStatusCode,
		LastError:      d.LastError,
		LastDurationMS: d.LastDurationMS,
		SucceededAt:    d.SucceededAt,
		CreatedAt:      d.CreatedAt,
	}
	// A next attempt time only means something while more attempts remain.
	if !d.State.Terminal() {
		next := d.NextAttemptAt
		out.NextAttemptAt = &next
	}
	return out
}

type attemptResponse struct {
	ID           string    `json:"id"`
	Number       int       `json:"attempt_number"`
	StatusCode   *int      `json:"status_code,omitempty"`
	Error        string    `json:"error,omitempty"`
	DurationMS   int       `json:"duration_ms"`
	ResponseBody string    `json:"response_body,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

func renderAttempt(a *delivery.Attempt) attemptResponse {
	return attemptResponse{
		ID:           a.ID,
		Number:       a.Number,
		StatusCode:   a.StatusCode,
		Error:        a.Error,
		DurationMS:   a.DurationMS,
		ResponseBody: a.ResponseBody,
		CreatedAt:    a.CreatedAt,
	}
}

type gatewayEventResponse struct {
	ID                   string         `json:"id"`
	Object               string         `json:"object"`
	Gateway              string         `json:"gateway"`
	ApplicationID        string         `json:"application_id,omitempty"`
	PaymentID            string         `json:"payment_id,omitempty"`
	GatewayOrderID       string         `json:"gateway_order_id,omitempty"`
	GatewayTransactionID string         `json:"gateway_transaction_id,omitempty"`
	GatewayStatus        string         `json:"gateway_status,omitempty"`
	FraudStatus          string         `json:"fraud_status,omitempty"`
	SignatureVerified    bool           `json:"signature_verified"`
	RoutingStatus        string         `json:"routing_status"`
	RoutingError         string         `json:"routing_error,omitempty"`
	Payload              map[string]any `json:"payload,omitempty"`
	ReceivedAt           time.Time      `json:"received_at"`
	ProcessedAt          *time.Time     `json:"processed_at,omitempty"`
}

func renderGatewayEvent(e *notification.GatewayEvent) gatewayEventResponse {
	return gatewayEventResponse{
		ID:                   e.ID,
		Object:               "gateway_event",
		Gateway:              e.Gateway,
		ApplicationID:        e.ApplicationID,
		PaymentID:            e.PaymentID,
		GatewayOrderID:       e.GatewayOrderID,
		GatewayTransactionID: e.GatewayTransactionID,
		GatewayStatus:        e.GatewayStatus,
		FraudStatus:          e.FraudStatus,
		SignatureVerified:    e.SignatureVerified,
		RoutingStatus:        string(e.Routing),
		RoutingError:         e.RoutingError,
		Payload:              e.Payload,
		ReceivedAt:           e.ReceivedAt,
		ProcessedAt:          e.ProcessedAt,
	}
}
