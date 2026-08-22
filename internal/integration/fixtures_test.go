package integration

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/app"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/application"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/crypto"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/delivery"
)

func applicationInput(name, slug string) application.CreateInput {
	return application.CreateInput{Name: name, Slug: slug}
}

func destinationInput(url string) application.CreateDestinationInput {
	return application.CreateDestinationInput{URL: url, Description: "integration test receiver"}
}

// receivedWebhook is one delivery a receiver accepted.
type receivedWebhook struct {
	Body      []byte
	Headers   http.Header
	EventID   string
	EventType string
	Signature string
	Timestamp string
}

// webhookReceiver stands in for a product's webhook endpoint.
//
// It records what it received so a test can assert on the signature and the
// payload, and it can be told to fail, which is how the retry behaviour is
// exercised.
type webhookReceiver struct {
	server *httptest.Server

	mu       sync.Mutex
	received []receivedWebhook
	// failuresRemaining makes the receiver reject that many more deliveries.
	failuresRemaining int
	// statusCode is returned once failuresRemaining reaches zero.
	statusCode int
	// failureStatus is returned while failures remain.
	failureStatus int
}

func newWebhookReceiver(t *testing.T) *webhookReceiver {
	t.Helper()
	r := &webhookReceiver{statusCode: http.StatusOK, failureStatus: http.StatusInternalServerError}
	r.server = httptest.NewServer(http.HandlerFunc(r.handle))
	t.Cleanup(r.server.Close)
	return r
}

func (r *webhookReceiver) handle(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.failuresRemaining > 0 {
		r.failuresRemaining--
		w.WriteHeader(r.failureStatus)
		_, _ = w.Write([]byte(`{"error":"temporarily unavailable"}`))
		return
	}

	r.received = append(r.received, receivedWebhook{
		Body:      body,
		Headers:   req.Header.Clone(),
		EventID:   req.Header.Get(delivery.HeaderEventID),
		EventType: req.Header.Get(delivery.HeaderEventType),
		Signature: req.Header.Get(delivery.HeaderSignature),
		Timestamp: req.Header.Get(delivery.HeaderTimestamp),
	})
	w.WriteHeader(r.statusCode)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// URL is where PayMux should deliver.
func (r *webhookReceiver) URL() string { return r.server.URL + "/webhooks/paymux" }

// count reports how many deliveries were accepted.
func (r *webhookReceiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.received)
}

// all returns every accepted delivery.
func (r *webhookReceiver) all() []receivedWebhook {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]receivedWebhook(nil), r.received...)
}

// failNext makes the receiver reject the next n deliveries.
func (r *webhookReceiver) failNext(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failuresRemaining = n
}

// failWith sets the status returned while failures remain.
func (r *webhookReceiver) failWith(status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failureStatus = status
}

// verifySignature checks a received delivery against the destination secret.
func (w receivedWebhook) verifySignature(secret, deliveryID string, timestamp int64) bool {
	return crypto.VerifyWebhookSignature(crypto.Secret(secret), timestamp, deliveryID, w.Body, w.Signature)
}

// newTestWorker builds a delivery worker that polls quickly.
func newTestWorker(container *app.Container) *delivery.Worker {
	return delivery.NewWorker(
		container.Deliveries,
		container.Events,
		container.AppRepository,
		container.Sender,
		delivery.WorkerOptions{
			Concurrency:  4,
			PollInterval: 25 * millisecond,
			Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
			ID:           "integration-worker",
		},
	)
}
