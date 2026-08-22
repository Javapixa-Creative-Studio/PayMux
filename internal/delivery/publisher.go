package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/application"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/event"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/storage"
)

// Publisher records an event and queues it to every destination that wants it.
//
// Both happen in one database transaction so the two can never disagree: an
// event is never visible without its deliveries, and a delivery never
// references an event that was rolled back (PRD §71).
type Publisher struct {
	db           *storage.DB
	events       *event.Repository
	deliveries   *Repository
	destinations *application.Repository
	logger       *slog.Logger
}

// NewPublisher builds a Publisher.
func NewPublisher(db *storage.DB, events *event.Repository, deliveries *Repository, destinations *application.Repository, logger *slog.Logger) *Publisher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Publisher{
		db:           db,
		events:       events,
		deliveries:   deliveries,
		destinations: destinations,
		logger:       logger,
	}
}

// PublishResult reports what a publish produced.
type PublishResult struct {
	Event      *event.Event
	Deliveries []*Delivery
	// Duplicate is true when this event had already been published, in which
	// case nothing new was queued.
	Duplicate bool
}

// Publish stores an event and enqueues its deliveries.
//
// A duplicate — an event PayMux already published for this payment and state —
// is reported, not treated as an error: redelivered gateway notifications are
// routine, and the correct response is to do nothing again (PRD §39).
func (p *Publisher) Publish(ctx context.Context, e *event.Event) (*PublishResult, error) {
	result := &PublishResult{}

	err := p.db.InTx(ctx, func(ctx context.Context, _ storage.Querier) error {
		if err := p.events.Create(ctx, e); err != nil {
			if errors.Is(err, event.ErrDuplicate) {
				result.Duplicate = true
				return nil
			}
			return err
		}
		result.Event = e

		destinations, err := p.destinations.ListEnabledDestinations(ctx, e.ApplicationID, string(e.Type))
		if err != nil {
			return err
		}
		for _, destination := range destinations {
			d := &Delivery{
				EventID:       e.ID,
				ApplicationID: e.ApplicationID,
				DestinationID: destination.ID,
				URL:           destination.URL,
			}
			if err := p.deliveries.Enqueue(ctx, d); err != nil {
				return fmt.Errorf("delivery: enqueue for destination %s: %w", destination.ID, err)
			}
			result.Deliveries = append(result.Deliveries, d)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if result.Duplicate {
		p.logger.Debug("event was already published; nothing queued",
			"application_id", e.ApplicationID, "type", e.Type, "payment_id", e.PaymentID)
		return result, nil
	}

	// An application with no destination is a configuration gap worth
	// surfacing: the event is stored, but nobody is listening for it.
	if len(result.Deliveries) == 0 {
		p.logger.Warn("event has no webhook destination to deliver to",
			"event_id", e.ID, "application_id", e.ApplicationID, "type", e.Type)
	} else {
		p.logger.Info("event published",
			"event_id", e.ID,
			"application_id", e.ApplicationID,
			"type", e.Type,
			"deliveries", len(result.Deliveries),
		)
	}
	return result, nil
}
