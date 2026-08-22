package api

import (
	"net/http"
	"time"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/delivery"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/event"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/notification"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/payment"
)

// Filters are read from the query string. An unrecognised value is left unset
// rather than rejected: a filter is a narrowing hint, and failing a listing
// over a stray parameter helps nobody.

func paymentFilterFromRequest(r *http.Request) payment.Filter {
	q := r.URL.Query()
	filter := payment.Filter{
		ApplicationID:        q.Get("application_id"),
		Gateway:              q.Get("gateway"),
		ApplicationOrderID:   q.Get("application_order_id"),
		GatewayOrderID:       q.Get("gateway_order_id"),
		GatewayTransactionID: q.Get("gateway_transaction_id"),
		CreatedFrom:          parseTimeParam(q.Get("created_from")),
		CreatedTo:            parseTimeParam(q.Get("created_to")),
	}
	if status := gateway.Status(q.Get("status")); status.Valid() {
		filter.Status = status
	}
	return filter
}

func eventFilterFromRequest(r *http.Request) event.Filter {
	q := r.URL.Query()
	filter := event.Filter{
		ApplicationID: q.Get("application_id"),
		PaymentID:     q.Get("payment_id"),
		CreatedFrom:   parseTimeParam(q.Get("created_from")),
		CreatedTo:     parseTimeParam(q.Get("created_to")),
	}
	if t := event.Type(q.Get("type")); t.Valid() {
		filter.Type = t
	}
	return filter
}

func deliveryFilterFromRequest(r *http.Request) delivery.Filter {
	q := r.URL.Query()
	filter := delivery.Filter{
		ApplicationID: q.Get("application_id"),
		EventID:       q.Get("event_id"),
		DestinationID: q.Get("destination_id"),
	}
	switch state := delivery.State(q.Get("state")); state {
	case delivery.StatePending, delivery.StateDelivering, delivery.StateSucceeded,
		delivery.StateFailed, delivery.StateDead, delivery.StateCanceled:
		filter.State = state
	}
	return filter
}

func notificationFilterFromRequest(r *http.Request) notification.Filter {
	q := r.URL.Query()
	filter := notification.Filter{
		ApplicationID: q.Get("application_id"),
		PaymentID:     q.Get("payment_id"),
		Gateway:       q.Get("gateway"),
	}
	switch routing := notification.Routing(q.Get("routing_status")); routing {
	case notification.RoutingRouted, notification.RoutingDuplicate, notification.RoutingUnrouted,
		notification.RoutingRejected, notification.RoutingIgnored:
		filter.Routing = routing
	}
	return filter
}

func parseTimeParam(value string) *time.Time {
	if value == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			utc := parsed.UTC()
			return &utc
		}
	}
	return nil
}
