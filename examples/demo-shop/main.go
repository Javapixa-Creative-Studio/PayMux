// Command demo-shop is a browsable storefront that pays through PayMux.
//
// It exists to make PayMux's premise visible rather than described: run two of
// these with different keys and they are two independent businesses, each with
// its own catalogue, orders and webhook secret, both collecting into one
// merchant account. Neither can see the other's payments.
//
// Everything a real integration does is here and nothing else is:
//
//	GET  /                  the catalogue
//	POST /buy               opens a payment, redirects the customer to pay
//	GET  /orders            what this shop knows about its own orders
//	POST /webhooks/paymux   the signed event, verified, that fulfils an order
//
// It keeps orders in memory on purpose. A database would be the largest thing
// in the file and would teach nothing about PayMux.
package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/Javapixa-Creative-Studio/PayMux/examples/paymuxgo"
)

func main() {
	shop := &shop{
		name:      envOr("SHOP_NAME", "Demo Shop"),
		tagline:   envOr("SHOP_TAGLINE", "A shop that pays through PayMux"),
		accent:    envOr("SHOP_ACCENT", "#3d4df5"),
		catalogue: catalogueFor(envOr("SHOP_CATALOGUE", "coffee")),
		paymux:    paymuxgo.NewClient(envOr("PAYMUX_URL", "http://localhost:8080"), os.Getenv("PAYMUX_API_KEY")),
		secret:    os.Getenv("PAYMUX_WEBHOOK_SECRET"),
		orders:    map[string]*order{},
		seen:      map[string]bool{},
	}
	if os.Getenv("PAYMUX_API_KEY") == "" || shop.secret == "" {
		log.Fatal("set PAYMUX_API_KEY and PAYMUX_WEBHOOK_SECRET; both are shown " +
			"once, when you create them in the PayMux dashboard")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", shop.storefront)
	mux.HandleFunc("POST /buy", shop.buy)
	mux.HandleFunc("GET /orders", shop.ordersPage)
	mux.HandleFunc("POST /webhooks/paymux", shop.receive)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})

	addr := envOr("SHOP_ADDR", ":9911")
	log.Printf("%s listening on %s", shop.name, addr)
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

type product struct {
	SKU   string
	Name  string
	Price int64 // minor units; rupiah for IDR
}

// order is what this shop knows about one purchase. The shop's own view is
// the point: PayMux holds the payment, the shop holds the order, and the
// webhook is the only thing that joins them.
type order struct {
	Reference string
	Product   string
	Amount    int64
	Status    string
	PaymentID string
	Placed    time.Time
	Settled   time.Time
}

type shop struct {
	name      string
	tagline   string
	accent    string
	catalogue []product
	paymux    *paymuxgo.Client
	secret    string

	mu     sync.Mutex
	orders map[string]*order
	// seen records event ids already handled. PayMux retries until it gets a
	// 2xx, so the same event can arrive twice; fulfilling twice would ship two
	// parcels for one payment.
	seen map[string]bool
}

func catalogueFor(kind string) []product {
	switch kind {
	case "books":
		return []product{
			{SKU: "BK-01", Name: "The Undersea Almanac", Price: 185000},
			{SKU: "BK-02", Name: "Field Notes on Rain", Price: 129000},
			{SKU: "BK-03", Name: "A History of Ledgers", Price: 240000},
		}
	default:
		return []product{
			{SKU: "CF-01", Name: "Gayo Highland, 250g", Price: 145000},
			{SKU: "CF-02", Name: "Toraja Dark Roast, 250g", Price: 132000},
			{SKU: "CF-03", Name: "Kintamani Honey, 250g", Price: 168000},
		}
	}
}

// buy opens a payment and sends the customer to the gateway.
//
// The order is recorded as pending *before* the redirect. If the customer pays
// and the browser never comes back, the webhook still has an order to settle,
// which is the case a checkout written happy-path-first always gets wrong.
func (s *shop) buy(w http.ResponseWriter, r *http.Request) {
	sku := r.FormValue("sku")
	item, ok := s.product(sku)
	if !ok {
		http.Error(w, "no such product", http.StatusBadRequest)
		return
	}

	reference := fmt.Sprintf("%s-%d", sku, time.Now().UnixNano()/1e6)

	s.mu.Lock()
	s.orders[reference] = &order{
		Reference: reference, Product: item.Name, Amount: item.Price,
		Status: "awaiting payment", Placed: time.Now(),
	}
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	payment, err := s.paymux.CreatePayment(ctx, paymuxgo.CreatePaymentRequest{
		ApplicationOrderID: reference,
		Amount:             item.Price,
		Currency:           "IDR",
		Customer:           &paymuxgo.Customer{Email: envOr("SHOP_BUYER_EMAIL", "buyer@example.test")},
		Items: []paymuxgo.Item{
			{ID: item.SKU, Name: item.Name, Price: item.Price, Quantity: 1},
		},
		// The reference is the idempotency key, so a customer who
		// double-clicks Buy gets the same payment rather than two.
	}, reference)
	if err != nil {
		s.setStatus(reference, "could not start payment: "+err.Error(), "")
		log.Printf("checkout failed for %s: %v", reference, err)
		http.Redirect(w, r, "/orders", http.StatusSeeOther)
		return
	}

	s.mu.Lock()
	s.orders[reference].PaymentID = payment.ID
	s.mu.Unlock()

	http.Redirect(w, r, payment.RedirectURL, http.StatusSeeOther)
}

// receive accepts a signed event and fulfils the order it names.
func (s *shop) receive(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "could not read body", http.StatusBadRequest)
		return
	}

	// The raw bytes, never a re-encoded object: the signature covers exactly
	// what PayMux sent.
	event, err := paymuxgo.VerifyWebhook(s.secret, r.Header, body)
	if err != nil {
		log.Printf("rejected a delivery: %v", err)
		http.Error(w, "signature does not match", http.StatusUnauthorized)
		return
	}

	s.mu.Lock()
	already := s.seen[event.ID]
	s.seen[event.ID] = true
	s.mu.Unlock()
	if already {
		// Answer 2xx so PayMux stops retrying, but do the work only once.
		w.WriteHeader(http.StatusOK)
		return
	}

	switch event.Type {
	case "payment.paid":
		s.setStatus(event.ApplicationOrderID, "paid", event.PaymentID)
		log.Printf("fulfilled %s", event.ApplicationOrderID)
	case "payment.failed", "payment.expired", "payment.canceled":
		s.setStatus(event.ApplicationOrderID, "not paid", event.PaymentID)
	case "payment.refunded", "payment.partially_refunded":
		s.setStatus(event.ApplicationOrderID, "refunded", event.PaymentID)
	}

	// Acknowledge quickly. PayMux does not wait for fulfilment, and a slow
	// handler earns itself a retry.
	w.WriteHeader(http.StatusOK)
}

func (s *shop) setStatus(reference, status, paymentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[reference]
	if !ok {
		// An event for an order this shop has never heard of. Recording it is
		// more useful than dropping it: it usually means two shops share a key.
		o = &order{Reference: reference, Product: "(unknown to this shop)", Placed: time.Now()}
		s.orders[reference] = o
	}
	o.Status = status
	if paymentID != "" {
		o.PaymentID = paymentID
	}
	if status == "paid" {
		o.Settled = time.Now()
	}
}

func (s *shop) product(sku string) (product, bool) {
	for _, p := range s.catalogue {
		if p.SKU == sku {
			return p, true
		}
	}
	return product{}, false
}

func (s *shop) snapshot() []*order {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*order, 0, len(s.orders))
	for _, o := range s.orders {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Placed.After(out[j].Placed) })
	return out
}

func (s *shop) storefront(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.render(w, "shop", map[string]any{"Catalogue": s.catalogue})
}

func (s *shop) ordersPage(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "orders", map[string]any{"Orders": s.snapshot()})
}

func (s *shop) render(w http.ResponseWriter, view string, data map[string]any) {
	data["Shop"] = s.name
	data["Tagline"] = s.tagline
	data["Accent"] = template.CSS(s.accent)
	data["View"] = view
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := page.Execute(w, data); err != nil {
		log.Printf("render: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func rupiah(minor int64) string {
	s := fmt.Sprintf("%d", minor)
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, '.')
		}
		out = append(out, c)
	}
	return "Rp " + string(out)
}
