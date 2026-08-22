package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/anggapixa/paymux/internal/application"
	"github.com/anggapixa/paymux/internal/crypto"
	"github.com/anggapixa/paymux/internal/event"
	"github.com/anggapixa/paymux/internal/ids"
	"github.com/anggapixa/paymux/internal/storage"
)

// staleLockAfter is how long a claimed delivery may sit untouched before it is
// assumed abandoned. It is comfortably longer than any single attempt can
// take, so a slow destination is never mistaken for a dead worker.
const staleLockAfter = 5 * time.Minute

// Worker drains the delivery queue.
type Worker struct {
	repo        *Repository
	events      *event.Repository
	apps        *application.Repository
	sender      *Sender
	logger      *slog.Logger
	concurrency int
	poll        time.Duration
	id          string
}

// WorkerOptions configures a Worker.
type WorkerOptions struct {
	Concurrency  int
	PollInterval time.Duration
	Logger       *slog.Logger
	// ID identifies this worker in the queue's lock records. It defaults to
	// the hostname plus a random suffix, so two workers on one host stay
	// distinguishable.
	ID string
}

// NewWorker builds a Worker.
func NewWorker(repo *Repository, events *event.Repository, apps *application.Repository, sender *Sender, opts WorkerOptions) *Worker {
	if opts.Concurrency < 1 {
		opts.Concurrency = 1
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 2 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.ID == "" {
		host, _ := os.Hostname()
		opts.ID = host + "-" + ids.New(ids.Job)
	}
	return &Worker{
		repo:        repo,
		events:      events,
		apps:        apps,
		sender:      sender,
		logger:      opts.Logger.With("worker_id", opts.ID),
		concurrency: opts.Concurrency,
		poll:        opts.PollInterval,
		id:          opts.ID,
	}
}

// Run drains the queue until ctx is cancelled.
//
// On shutdown it stops claiming new work and waits for in-flight deliveries to
// finish, so a deployment does not abandon a delivery mid-flight (PRD §69).
func (w *Worker) Run(ctx context.Context) error {
	w.logger.Info("delivery worker started", "concurrency", w.concurrency, "poll_interval", w.poll)

	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()

	recovery := time.NewTicker(staleLockAfter)
	defer recovery.Stop()

	var wg sync.WaitGroup
	defer wg.Wait()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("delivery worker stopping; waiting for in-flight deliveries")
			return nil

		case <-recovery.C:
			w.recoverStale(ctx)

		case <-ticker.C:
			processed, err := w.drain(ctx, &wg)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				w.logger.Error("could not claim deliveries", "error", err)
				continue
			}
			// A full batch means more work is waiting; keep going rather than
			// idling until the next tick.
			for processed >= w.concurrency && ctx.Err() == nil {
				processed, err = w.drain(ctx, &wg)
				if err != nil {
					break
				}
			}
		}
	}
}

// drain claims and dispatches one batch, returning how many were claimed.
func (w *Worker) drain(ctx context.Context, wg *sync.WaitGroup) (int, error) {
	claimed, err := w.repo.Claim(ctx, w.id, w.concurrency)
	if err != nil {
		return 0, err
	}
	for _, d := range claimed {
		wg.Add(1)
		go func(d *Delivery) {
			defer wg.Done()
			// Deliveries in flight use a context detached from shutdown so a
			// stopping worker still records their outcome; the sender's own
			// timeout bounds how long that can take.
			attemptCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), staleLockAfter)
			defer cancel()
			w.process(attemptCtx, d)
		}(d)
	}
	return len(claimed), nil
}

func (w *Worker) recoverStale(ctx context.Context) {
	released, err := w.repo.ReleaseStale(ctx, staleLockAfter)
	if err != nil {
		w.logger.Error("could not release stale deliveries", "error", err)
		return
	}
	if released > 0 {
		w.logger.Warn("released deliveries abandoned by a stopped worker", "count", released)
	}
}

// process performs one delivery and records its outcome.
func (w *Worker) process(ctx context.Context, d *Delivery) {
	logger := w.logger.With(
		"delivery_id", d.ID,
		"event_id", d.EventID,
		"application_id", d.ApplicationID,
	)

	payload, eventType, secret, err := w.prepare(ctx, d)
	if err != nil {
		// The delivery cannot be built at all — a deleted destination, or an
		// event that no longer exists. Retrying will not help.
		logger.Error("delivery could not be prepared", "error", err)
		w.recordFailure(ctx, d, Attempt{Number: d.AttemptCount + 1, Error: err.Error()}, false, logger)
		return
	}

	attemptNumber := d.AttemptCount + 1
	logger = logger.With("attempt", attemptNumber)

	result := w.sender.Send(ctx, Request{
		DeliveryID:    d.ID,
		EventID:       d.EventID,
		EventType:     eventType,
		URL:           d.URL,
		Body:          payload,
		Secret:        secret,
		AttemptNumber: attemptNumber,
	})

	attempt := Attempt{
		Number:       attemptNumber,
		DurationMS:   int(result.Duration.Milliseconds()),
		ResponseBody: result.ResponseBody,
	}
	switch {
	case result.Err != nil:
		attempt.Error = result.Err.Error()
	default:
		status := result.StatusCode
		attempt.StatusCode = &status
		if !Succeeded(status) {
			attempt.Error = fmt.Sprintf("destination returned HTTP %d", status)
		}
	}

	if result.Delivered() {
		if err := w.repo.RecordSuccess(ctx, d, attempt); err != nil {
			logger.Error("could not record a successful delivery", "error", err)
			return
		}
		logger.Info("event delivered",
			"status", result.StatusCode,
			"duration_ms", attempt.DurationMS,
		)
		return
	}
	w.recordFailure(ctx, d, attempt, result.Retryable(), logger)
}

// recordFailure stores a failed attempt and reports what happens next.
func (w *Worker) recordFailure(ctx context.Context, d *Delivery, attempt Attempt, retry bool, logger *slog.Logger) {
	if err := w.repo.RecordFailure(ctx, d, attempt, retry); err != nil {
		logger.Error("could not record a failed delivery", "error", err)
		return
	}
	if d.State == StateDead {
		logger.Error("delivery gave up after exhausting its attempts",
			"attempts", d.AttemptCount, "last_error", attempt.Error)
		return
	}
	logger.Warn("delivery failed and will be retried",
		"next_attempt_at", d.NextAttemptAt, "error", attempt.Error)
}

// prepare loads the event payload and the destination's current signing
// secret.
//
// The secret is read at send time rather than being captured when the delivery
// was queued, so a rotation takes effect on the next attempt.
func (w *Worker) prepare(ctx context.Context, d *Delivery) ([]byte, string, crypto.Secret, error) {
	evt, err := w.events.Get(ctx, d.EventID)
	if err != nil {
		return nil, "", "", fmt.Errorf("delivery: load event: %w", err)
	}
	destination, err := w.apps.GetDestination(ctx, d.ApplicationID, d.DestinationID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, "", "", errors.New("delivery: the destination no longer exists")
		}
		return nil, "", "", err
	}
	payload, err := json.Marshal(evt.Payload)
	if err != nil {
		return nil, "", "", fmt.Errorf("delivery: encode event payload: %w", err)
	}
	return payload, string(evt.Type), destination.Secret, nil
}
