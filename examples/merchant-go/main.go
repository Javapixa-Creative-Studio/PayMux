package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

// This example is a miniature storefront. It does the two things every
// application integrating with PayMux has to do:
//
//	POST /checkout          opens a payment and hands the browser a token
//	POST /webhooks/paymux   accepts the signed event and fulfils the order
//
// Run it with:
//
//	PAYMUX_URL=http://localhost:8080 \
//	PAYMUX_API_KEY=pmx_test_... \
//	PAYMUX_WEBHOOK_SECRET=whsec_... \
//	go run ./examples/merchant-go

func main() {
	baseURL := envOr("PAYMUX_URL", "http://localhost:8080")
	apiKey := os.Getenv("PAYMUX_API_KEY")
	secret := os.Getenv("PAYMUX_WEBHOOK_SECRET")
	addr := envOr("MERCHANT_ADDR", ":9911")

	if apiKey == "" || secret == "" {
		log.Fatal("set PAYMUX_API_KEY and PAYMUX_WEBHOOK_SECRET; " +
			"both are shown once, when you create them in the PayMux dashboard")
	}

	shop := &shop{
		paymux: NewClient(baseURL, apiKey),
		secret: secret,
		orders: make(map[string]string),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /checkout", shop.checkout)
	mux.HandleFunc("POST /webhooks/paymux", shop.receive)

	log.Printf("example merchant listening on %s, using PayMux at %s", addr, baseURL)
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

type shop struct {
	paymux *Client
	secret string

	// orders stands in for your database: order reference to fulfilment
	// state. A real application would use its own storage.
	mu     sync.Mutex
	orders map[string]string
}

type checkoutRequest struct {
	OrderID string `json:"order_id"`
	Amount  int64  `json:"amount"`
	Email   string `json:"email"`
}

// checkout opens a payment and returns what the browser needs.
func (s *shop) checkout(w http.ResponseWriter, r *http.Request) {
	var req checkoutRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	payment, err := s.paymux.CreatePayment(ctx, CreatePaymentRequest{
		ApplicationOrderID: req.OrderID,
		Amount:             req.Amount,
		Currency:           "IDR",
		Customer:           &Customer{Email: req.Email},
		Items: []Item{
			// Item prices must sum to the amount, or the gateway rejects it.
			{ID: "SKU-1", Name: "Example product", Price: req.Amount, Quantity: 1},
		},
		Metadata: map[string]any{"source": "example-merchant"},
	}, "order-"+req.OrderID) // derived from the order, so a retry matches

	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			log.Printf("paymux refused the payment: %v", apiErr)
			http.Error(w, apiErr.Message, http.StatusBadGateway)
			return
		}
		log.Printf("could not reach paymux: %v", err)
		http.Error(w, "payment could not be started", http.StatusBadGateway)
		return
	}

	s.mu.Lock()
	s.orders[req.OrderID] = "awaiting payment"
	s.mu.Unlock()

	// The browser needs only the token or the redirect URL. Never send the
	// gateway's server key anywhere near a browser — PayMux holds it for you.
	writeJSON(w, http.StatusOK, map[string]any{
		"payment_id":   payment.ID,
		"snap_token":   payment.SnapToken,
		"redirect_url": payment.RedirectURL,
	})
}

// receive handles a delivery from PayMux.
func (s *shop) receive(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "could not read body", http.StatusBadRequest)
		return
	}

	event, err := VerifyWebhook(s.secret, r.Header, body)
	if err != nil {
		// Answering 401 tells PayMux the delivery was refused. It will retry,
		// which is what you want if the cause was a secret you had not yet
		// rotated into place.
		log.Printf("rejected a delivery: %v", err)
		http.Error(w, "signature could not be verified", http.StatusUnauthorized)
		return
	}

	// Deliveries are at-least-once: PayMux retries until you answer 2xx, so
	// the same event can arrive twice. Key your fulfilment on event.ID.
	s.mu.Lock()
	previous := s.orders[event.ApplicationOrderID]
	switch event.Type {
	case "payment.paid":
		s.orders[event.ApplicationOrderID] = "paid"
	case "payment.expired", "payment.canceled", "payment.failed":
		s.orders[event.ApplicationOrderID] = "closed unpaid"
	case "payment.refunded", "payment.partially_refunded":
		s.orders[event.ApplicationOrderID] = "refunded"
	}
	current := s.orders[event.ApplicationOrderID]
	s.mu.Unlock()

	log.Printf("%s: order %s %s -> %s (%s %d)",
		event.Type, event.ApplicationOrderID, previous, current, event.Currency, event.Amount)

	// Answer promptly. PayMux does not wait for your fulfilment work, and a
	// slow handler only makes it retry.
	writeJSON(w, http.StatusOK, map[string]any{"received": event.ID})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
