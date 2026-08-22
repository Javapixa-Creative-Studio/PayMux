package api

import (
	"io"
	"net/http"

	"github.com/anggapixa/paymux/internal/gateway"
	"github.com/anggapixa/paymux/internal/httpx"
	"github.com/anggapixa/paymux/internal/logging"
	"github.com/anggapixa/paymux/internal/notification"
)

// maxNotificationBytes caps an inbound gateway notification. Midtrans payloads
// are a few kilobytes; this leaves ample room while bounding the endpoint.
const maxNotificationBytes = 256 << 10

// handleMidtransNotification receives Midtrans's payment notifications.
//
// This is the URL configured as the merchant's Payment Notification URL, and
// it is deliberately outside the application API's authentication: it is
// authenticated by Midtrans's own signature instead (PRD §77).
//
// PayMux acknowledges as soon as the notification is durably recorded and the
// payment updated. Delivery to the owning application happens asynchronously,
// because making Midtrans wait for a third party's webhook would turn that
// application's downtime into failed notifications (PRD §37).
func (s *Server) handleMidtransNotification(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxNotificationBytes))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrInvalidRequest("Could not read the notification body."))
		return
	}

	outcome, err := s.notifications.Process(r.Context(), "midtrans", gateway.WebhookRequest{
		Headers: r.Header,
		Body:    body,
	})
	if err != nil {
		// PayMux failed, not the sender. A 5xx asks Midtrans to redeliver,
		// which is exactly what should happen.
		logging.FromContext(r.Context()).Error("could not process a gateway notification", "error", err)
		httpx.Fail(w, r, httpx.ErrInternal(err))
		return
	}

	switch outcome.Routing {
	case notification.RoutingRejected:
		// Verification failed. Answering 401 tells a genuine gateway that its
		// credentials do not match this instance, and tells a forger nothing
		// about whether the order exists.
		httpx.Fail(w, r, httpx.ErrUnauthorized("The notification could not be verified."))
		return

	case notification.RoutingUnrouted:
		// The notification is authentic but matches no payment PayMux created.
		// It is stored for an operator to inspect, and acknowledged: asking
		// the gateway to redeliver would not make the payment appear.
		httpx.JSON(w, r, http.StatusAccepted, map[string]any{
			"received": true,
			"routed":   false,
			"reason":   outcome.Reason,
		})
		return
	}

	httpx.JSON(w, r, http.StatusOK, map[string]any{
		"received": true,
		"routed":   outcome.Routing == notification.RoutingRouted,
		"status":   string(outcome.Routing),
	})
}
