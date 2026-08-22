package notification

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/anggapixa/paymux/internal/delivery"
	"github.com/anggapixa/paymux/internal/event"
	"github.com/anggapixa/paymux/internal/gateway"
	"github.com/anggapixa/paymux/internal/gateway/midtrans"
	"github.com/anggapixa/paymux/internal/payment"
	"github.com/anggapixa/paymux/internal/storage"
)

// MetricsRecorder observes inbound gateway notifications.
type MetricsRecorder interface {
	RecordWebhookReceived(gateway, routing string)
}

// Processor turns a verified gateway notification into PayMux state and events.
//
// The pipeline is: verify, record, attribute, apply, publish (PRD §37). Each
// step is separate so a notification that fails one of them is still stored
// with the reason — PayMux never silently drops a message from a gateway.
type Processor struct {
	repo      *Repository
	payments  *payment.Repository
	accounts  *gateway.Repository
	registry  *gateway.Registry
	publisher *delivery.Publisher
	db        *storage.DB
	logger    *slog.Logger
	metrics   MetricsRecorder
}

// SetMetrics attaches a recorder. A nil recorder disables the counters.
func (p *Processor) SetMetrics(recorder MetricsRecorder) { p.metrics = recorder }

// observe records the outcome of one notification.
func (p *Processor) observe(gatewayName string, routing Routing) {
	if p.metrics != nil {
		p.metrics.RecordWebhookReceived(gatewayName, string(routing))
	}
}

// NewProcessor builds a Processor.
func NewProcessor(
	db *storage.DB,
	repo *Repository,
	payments *payment.Repository,
	accounts *gateway.Repository,
	registry *gateway.Registry,
	publisher *delivery.Publisher,
	logger *slog.Logger,
) *Processor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Processor{
		db: db, repo: repo, payments: payments, accounts: accounts,
		registry: registry, publisher: publisher, logger: logger,
	}
}

// Outcome reports what happened to a notification.
type Outcome struct {
	Routing        Routing
	GatewayEventID string
	PaymentID      string
	ApplicationID  string
	// Published is the PayMux event this notification produced, if any.
	Published *event.Event
	Reason    string
}

// Process handles one inbound notification.
//
// It returns an error only when PayMux itself failed — a database problem, or
// a gateway account it cannot load. A notification that is forged, unknown or
// stale is not an error: it is recorded with the appropriate routing status
// and reported back so the endpoint can answer the gateway correctly.
func (p *Processor) Process(ctx context.Context, gatewayName string, req gateway.WebhookRequest) (*Outcome, error) {
	account, err := p.accounts.Default(ctx, gatewayName)
	if err != nil {
		if storage.IsNotFound(err) {
			return nil, fmt.Errorf("notification: no %s gateway account is configured", gatewayName)
		}
		return nil, err
	}
	adapter, err := p.registry.For(account)
	if err != nil {
		return nil, err
	}

	// Verification comes first: nothing that fails it may touch a payment.
	verifyErr := adapter.VerifyWebhook(ctx, req)

	parsed, parseErr := adapter.ParseWebhook(ctx, req)
	if parseErr != nil {
		// A body PayMux cannot parse cannot be recorded against anything, but
		// the attempt is still worth logging loudly.
		p.logger.Warn("could not parse a gateway notification",
			"gateway", gatewayName, "error", parseErr)
		return &Outcome{Routing: RoutingRejected, Reason: parseErr.Error()},
			fmt.Errorf("notification: %w", parseErr)
	}

	record := &GatewayEvent{
		Gateway:              gatewayName,
		GatewayAccountID:     account.ID,
		GatewayOrderID:       parsed.OrderID,
		GatewayTransactionID: parsed.TransactionID,
		GatewayStatus:        parsed.Status,
		FraudStatus:          parsed.FraudStatus,
		DedupeKey:            parsed.DedupeKey,
		SignatureVerified:    verifyErr == nil,
		Payload:              midtrans.RedactPayload(parsed.Raw),
		Routing:              RoutingRouted,
	}

	if verifyErr != nil {
		record.Routing = RoutingRejected
		record.RoutingError = "signature verification failed"
		if err := p.record(ctx, record); err != nil {
			return nil, err
		}
		p.logger.Warn("rejected a gateway notification that failed verification",
			"gateway", gatewayName, "order_id", parsed.OrderID, "gateway_event_id", record.ID)
		p.observe(gatewayName, RoutingRejected)
		return &Outcome{
			Routing:        RoutingRejected,
			GatewayEventID: record.ID,
			Reason:         "signature verification failed",
		}, nil
	}

	// Attribute the notification to the payment PayMux created for this order.
	owner, err := p.payments.GetByGatewayOrderID(ctx, gatewayName, parsed.OrderID)
	if err != nil && !storage.IsNotFound(err) {
		return nil, err
	}
	if owner != nil {
		record.PaymentID = owner.ID
		record.ApplicationID = owner.ApplicationID
	} else {
		record.Routing = RoutingUnrouted
		record.RoutingError = "no payment matches this gateway order id"
	}

	if err := p.record(ctx, record); err != nil {
		if errors.Is(err, ErrDuplicate) {
			p.logger.Debug("ignored a duplicate gateway notification",
				"gateway", gatewayName, "order_id", parsed.OrderID)
			p.observe(gatewayName, RoutingDuplicate)
			return &Outcome{
				Routing:       RoutingDuplicate,
				PaymentID:     record.PaymentID,
				ApplicationID: record.ApplicationID,
			}, nil
		}
		return nil, err
	}

	if owner == nil {
		// Recorded, visible in the dashboard, and deliberately not discarded.
		p.logger.Warn("received a notification for an unknown order",
			"gateway", gatewayName,
			"order_id", parsed.OrderID,
			"gateway_event_id", record.ID)
		p.observe(gatewayName, RoutingUnrouted)
		return &Outcome{
			Routing:        RoutingUnrouted,
			GatewayEventID: record.ID,
			Reason:         "no payment matches this gateway order id",
		}, nil
	}

	outcome, err := p.apply(ctx, record, owner, parsed)
	if err == nil && outcome != nil {
		p.observe(gatewayName, outcome.Routing)
	}
	return outcome, err
}

// apply moves the payment to the notified state and publishes the event.
func (p *Processor) apply(ctx context.Context, record *GatewayEvent, owner *payment.Payment, parsed *gateway.Event) (*Outcome, error) {
	outcome := &Outcome{
		GatewayEventID: record.ID,
		PaymentID:      owner.ID,
		ApplicationID:  owner.ApplicationID,
	}

	if parsed.Normalized == "" {
		// The gateway reported a status this build does not map. The payment
		// is left untouched and the event is kept for an operator to see,
		// rather than being guessed at or dropped (PRD §91 rule 15).
		outcome.Routing = RoutingIgnored
		outcome.Reason = "gateway reported an unrecognised status: " + parsed.Status
		if err := p.repo.MarkProcessed(ctx, record.ID, RoutingIgnored, outcome.Reason, owner.ID, owner.ApplicationID); err != nil {
			return nil, err
		}
		p.logger.Warn("gateway reported an unrecognised transaction status",
			"payment_id", owner.ID, "gateway_status", parsed.Status)
		return outcome, nil
	}

	if err := p.payments.UpsertGatewayTransaction(ctx, owner.ID, &gateway.Transaction{
		OrderID:         parsed.OrderID,
		TransactionID:   parsed.TransactionID,
		Status:          parsed.Status,
		FraudStatus:     parsed.FraudStatus,
		PaymentType:     parsed.PaymentType,
		GrossAmount:     parsed.GrossAmount,
		Currency:        parsed.Currency,
		TransactionTime: parsed.TransactionTime,
		SettlementTime:  parsed.SettlementTime,
		Raw:             midtrans.RedactPayload(parsed.Raw),
	}); err != nil {
		p.logger.Warn("could not record the gateway transaction",
			"payment_id", owner.ID, "error", err)
	}

	occurred := parsed.SettlementTime
	if occurred == nil {
		occurred = parsed.TransactionTime
	}

	updated, err := p.payments.ApplyState(ctx, owner.ID, payment.StateUpdate{
		NormalizedStatus:     parsed.Normalized,
		GatewayStatus:        parsed.Status,
		FraudStatus:          parsed.FraudStatus,
		GatewayTransactionID: parsed.TransactionID,
		PaymentType:          parsed.PaymentType,
		PaymentMethod:        parsed.PaymentType,
		GatewayData:          midtrans.RedactPayload(parsed.Raw),
		OccurredAt:           occurred,
	})
	if err != nil {
		if errors.Is(err, payment.ErrStaleTransition) {
			// A delayed notification for a state PayMux has already moved past.
			// Recording it and doing nothing is the correct response (PRD §40).
			outcome.Routing = RoutingIgnored
			outcome.Reason = fmt.Sprintf("payment is already %s; %s would move it backwards",
				owner.NormalizedStatus, parsed.Normalized)
			if markErr := p.repo.MarkProcessed(ctx, record.ID, RoutingIgnored, outcome.Reason, owner.ID, owner.ApplicationID); markErr != nil {
				return nil, markErr
			}
			p.logger.Info("ignored a stale gateway notification",
				"payment_id", owner.ID,
				"current_status", owner.NormalizedStatus,
				"notified_status", parsed.Normalized)
			return outcome, nil
		}
		return nil, err
	}

	eventType, ok := event.TypeForStatus(updated.NormalizedStatus)
	if !ok {
		outcome.Routing = RoutingIgnored
		outcome.Reason = "no event type is defined for status " + string(updated.NormalizedStatus)
		if err := p.repo.MarkProcessed(ctx, record.ID, RoutingIgnored, outcome.Reason, owner.ID, owner.ApplicationID); err != nil {
			return nil, err
		}
		return outcome, nil
	}

	e := &event.Event{
		ApplicationID:  updated.ApplicationID,
		Type:           eventType,
		Gateway:        updated.Gateway,
		PaymentID:      updated.ID,
		GatewayEventID: record.ID,
		DedupeKey:      event.PaymentDedupeKey(updated.ID, eventType, ""),
		Payload:        payment.BuildPayload(updated, eventType),
	}
	published, err := p.publisher.Publish(ctx, e)
	if err != nil {
		return nil, err
	}

	outcome.Routing = RoutingRouted
	if published.Duplicate {
		outcome.Routing = RoutingDuplicate
		outcome.Reason = "this event had already been published"
	} else {
		outcome.Published = e
	}
	if err := p.repo.MarkProcessed(ctx, record.ID, outcome.Routing, outcome.Reason, updated.ID, updated.ApplicationID); err != nil {
		return nil, err
	}

	p.logger.Info("processed a gateway notification",
		"payment_id", updated.ID,
		"application_id", updated.ApplicationID,
		"gateway_status", parsed.Status,
		"status", updated.NormalizedStatus,
		"event_type", eventType,
		"deliveries", len(published.Deliveries),
	)
	return outcome, nil
}

// record persists the notification, translating a duplicate into ErrDuplicate.
func (p *Processor) record(ctx context.Context, e *GatewayEvent) error {
	return p.repo.Record(ctx, e)
}
