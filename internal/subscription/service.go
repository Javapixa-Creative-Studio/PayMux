package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/anggapixa/paymux/internal/application"
	"github.com/anggapixa/paymux/internal/delivery"
	"github.com/anggapixa/paymux/internal/event"
	"github.com/anggapixa/paymux/internal/gateway"
	"github.com/anggapixa/paymux/internal/money"
	"github.com/anggapixa/paymux/internal/storage"
)

// ErrGatewayNotConfigured reports an application with no usable gateway.
var ErrGatewayNotConfigured = errors.New("subscription: no gateway account is configured")

// ValidationError reports a specific invalid field.
type ValidationError struct {
	Field   string
	Message string
}

// Error implements error.
func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

func invalid(field, message string) error { return &ValidationError{Field: field, Message: message} }

// Service implements the subscription lifecycle.
type Service struct {
	repo      *Repository
	accounts  *gateway.Repository
	registry  *gateway.Registry
	publisher *delivery.Publisher
	logger    *slog.Logger
}

// NewService builds a Service.
func NewService(repo *Repository, accounts *gateway.Repository, registry *gateway.Registry, publisher *delivery.Publisher, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, accounts: accounts, registry: registry, publisher: publisher, logger: logger}
}

// CreateInput describes a subscription an application wants to start.
type CreateInput struct {
	Application   *application.Application
	Name          string
	Amount        int64
	Currency      string
	PaymentType   string
	PaymentToken  string
	IntervalUnit  string
	IntervalCount int
	MaxInterval   int
	StartTime     *time.Time
	Customer      gateway.Customer
	Metadata      map[string]any
	Options       json.RawMessage
}

// Create starts a subscription at the gateway and records it.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Subscription, error) {
	if err := validateCreate(&in); err != nil {
		return nil, err
	}

	account, err := s.accountFor(ctx, in.Application)
	if err != nil {
		return nil, err
	}
	adapter, err := s.subscriptionGateway(account)
	if err != nil {
		return nil, err
	}

	created, err := adapter.CreateSubscription(ctx, gateway.CreateSubscriptionRequest{
		Name:          in.Name,
		Amount:        in.Amount,
		Currency:      in.Currency,
		PaymentType:   in.PaymentType,
		PaymentToken:  in.PaymentToken,
		IntervalUnit:  in.IntervalUnit,
		IntervalCount: in.IntervalCount,
		MaxInterval:   in.MaxInterval,
		StartTime:     in.StartTime,
		Customer:      in.Customer,
		Metadata:      in.Metadata,
		Options:       in.Options,
	})
	if err != nil {
		return nil, err
	}

	sub := &Subscription{
		ApplicationID:         in.Application.ID,
		GatewayAccountID:      account.ID,
		Gateway:               account.Gateway,
		GatewaySubscriptionID: created.ID,
		Name:                  in.Name,
		Amount:                in.Amount,
		Currency:              money.NormalizeCurrency(in.Currency),
		Status:                created.Normalized,
		GatewayStatus:         created.Status,
		IntervalUnit:          in.IntervalUnit,
		IntervalCount:         in.IntervalCount,
		StartTime:             in.StartTime,
		PaymentType:           created.PaymentType,
		PaymentToken:          in.PaymentToken,
		Metadata:              in.Metadata,
		GatewayData:           created.Raw,
	}
	if in.MaxInterval > 0 {
		max := in.MaxInterval
		sub.MaxInterval = &max
	}
	if err := s.repo.Create(ctx, sub); err != nil {
		return nil, err
	}

	s.publish(ctx, sub, event.SubscriptionCreated)
	return sub, nil
}

// Get returns a subscription owned by the application.
func (s *Service) Get(ctx context.Context, applicationID, id string) (*Subscription, error) {
	if applicationID == "" {
		return s.repo.Get(ctx, id)
	}
	return s.repo.GetForApplication(ctx, applicationID, id)
}

// List returns a page of subscriptions.
func (s *Service) List(ctx context.Context, applicationID string, page storage.Page) (storage.List[*Subscription], error) {
	return s.repo.List(ctx, applicationID, page)
}

// Sync refreshes a subscription from the gateway.
func (s *Service) Sync(ctx context.Context, applicationID, id string) (*Subscription, error) {
	sub, adapter, err := s.load(ctx, applicationID, id)
	if err != nil {
		return nil, err
	}
	current, err := adapter.GetSubscription(ctx, sub.GatewaySubscriptionID)
	if err != nil {
		return nil, err
	}
	applyGateway(sub, current)
	if err := s.repo.Update(ctx, sub); err != nil {
		return nil, err
	}
	return sub, nil
}

// UpdateInput carries the mutable fields of a subscription.
type UpdateInput struct {
	Name          *string
	Amount        *int64
	IntervalUnit  *string
	IntervalCount *int
	PaymentToken  *string
	Options       json.RawMessage
}

// Update changes a live subscription.
func (s *Service) Update(ctx context.Context, applicationID, id string, in UpdateInput) (*Subscription, error) {
	sub, adapter, err := s.load(ctx, applicationID, id)
	if err != nil {
		return nil, err
	}
	if in.Amount != nil && *in.Amount <= 0 {
		return nil, invalid("amount", "must be greater than zero")
	}

	updated, err := adapter.UpdateSubscription(ctx, sub.GatewaySubscriptionID, gateway.UpdateSubscriptionRequest{
		Name:          in.Name,
		Amount:        in.Amount,
		IntervalUnit:  in.IntervalUnit,
		IntervalCount: in.IntervalCount,
		PaymentToken:  in.PaymentToken,
		Options:       in.Options,
	})
	if err != nil {
		return nil, err
	}
	applyGateway(sub, updated)
	if in.PaymentToken != nil {
		sub.PaymentToken = *in.PaymentToken
	}
	if err := s.repo.Update(ctx, sub); err != nil {
		return nil, err
	}

	s.publish(ctx, sub, event.SubscriptionUpdated)
	return sub, nil
}

// Enable resumes a paused subscription.
func (s *Service) Enable(ctx context.Context, applicationID, id string) (*Subscription, error) {
	return s.lifecycle(ctx, applicationID, id, gateway.SubscriptionActive, event.SubscriptionEnabled,
		func(adapter gateway.SubscriptionGateway, gatewayID string) error {
			return adapter.EnableSubscription(ctx, gatewayID)
		})
}

// Disable pauses a subscription without ending it.
func (s *Service) Disable(ctx context.Context, applicationID, id string) (*Subscription, error) {
	return s.lifecycle(ctx, applicationID, id, gateway.SubscriptionInactive, event.SubscriptionDisabled,
		func(adapter gateway.SubscriptionGateway, gatewayID string) error {
			return adapter.DisableSubscription(ctx, gatewayID)
		})
}

// Cancel ends a subscription permanently.
func (s *Service) Cancel(ctx context.Context, applicationID, id string) (*Subscription, error) {
	return s.lifecycle(ctx, applicationID, id, gateway.SubscriptionCanceled, event.SubscriptionCanceled,
		func(adapter gateway.SubscriptionGateway, gatewayID string) error {
			return adapter.CancelSubscription(ctx, gatewayID)
		})
}

func (s *Service) lifecycle(
	ctx context.Context,
	applicationID, id string,
	status gateway.SubscriptionStatus,
	eventType event.Type,
	action func(gateway.SubscriptionGateway, string) error,
) (*Subscription, error) {
	sub, adapter, err := s.load(ctx, applicationID, id)
	if err != nil {
		return nil, err
	}
	if err := action(adapter, sub.GatewaySubscriptionID); err != nil {
		return nil, err
	}
	updated, err := s.repo.SetStatus(ctx, sub.ID, status)
	if err != nil {
		return nil, err
	}
	s.publish(ctx, updated, eventType)
	return updated, nil
}

// load fetches a subscription with the adapter that manages it.
func (s *Service) load(ctx context.Context, applicationID, id string) (*Subscription, gateway.SubscriptionGateway, error) {
	sub, err := s.Get(ctx, applicationID, id)
	if err != nil {
		return nil, nil, err
	}
	account, err := s.accounts.Get(ctx, sub.GatewayAccountID)
	if err != nil {
		return nil, nil, err
	}
	adapter, err := s.subscriptionGateway(account)
	if err != nil {
		return nil, nil, err
	}
	if sub.GatewaySubscriptionID == "" {
		return nil, nil, fmt.Errorf("subscription: %s has no gateway subscription id", sub.ID)
	}
	return sub, adapter, nil
}

func (s *Service) subscriptionGateway(account *gateway.Account) (gateway.SubscriptionGateway, error) {
	adapter, err := s.registry.For(account)
	if err != nil {
		return nil, err
	}
	subscriptionGateway, ok := adapter.(gateway.SubscriptionGateway)
	if !ok {
		return nil, gateway.ErrNotSupported
	}
	return subscriptionGateway, nil
}

func (s *Service) accountFor(ctx context.Context, app *application.Application) (*gateway.Account, error) {
	if app.GatewayAccountID != "" {
		account, err := s.accounts.Get(ctx, app.GatewayAccountID)
		if err != nil {
			if storage.IsNotFound(err) {
				return nil, ErrGatewayNotConfigured
			}
			return nil, err
		}
		if !account.Usable() {
			return nil, ErrGatewayNotConfigured
		}
		return account, nil
	}
	account, err := s.accounts.Default(ctx, "midtrans")
	if err != nil {
		if storage.IsNotFound(err) {
			return nil, ErrGatewayNotConfigured
		}
		return nil, err
	}
	if !account.Usable() {
		return nil, ErrGatewayNotConfigured
	}
	return account, nil
}

// applyGateway folds the gateway's view into PayMux's record.
func applyGateway(sub *Subscription, current *gateway.Subscription) {
	sub.GatewaySubscriptionID = current.ID
	sub.Name = current.Name
	if current.Amount > 0 {
		sub.Amount = current.Amount
	}
	sub.Status = current.Normalized
	sub.GatewayStatus = current.Status
	if current.IntervalUnit != "" {
		sub.IntervalUnit = current.IntervalUnit
	}
	if current.IntervalCount > 0 {
		sub.IntervalCount = current.IntervalCount
	}
	if current.PaymentType != "" {
		sub.PaymentType = current.PaymentType
	}
	sub.GatewayData = current.Raw
}

// publish emits a subscription event.
//
// The gateway status is part of the dedupe key so a repeat of the same
// lifecycle action does not fan out twice, while a genuine change does.
func (s *Service) publish(ctx context.Context, sub *Subscription, eventType event.Type) {
	e := &event.Event{
		ApplicationID:  sub.ApplicationID,
		Type:           eventType,
		Gateway:        sub.Gateway,
		SubscriptionID: sub.ID,
		DedupeKey:      event.SubscriptionDedupeKey(sub.ID, eventType, string(sub.Status)),
		Payload: event.Payload{
			Type:           eventType,
			Gateway:        sub.Gateway,
			ApplicationID:  sub.ApplicationID,
			SubscriptionID: sub.ID,
			Amount:         sub.Amount,
			Currency:       sub.Currency,
			GatewayStatus:  sub.GatewayStatus,
			Metadata:       sub.Metadata,
			CreatedAt:      time.Now().UTC(),
			GatewayData:    sub.GatewayData,
		},
	}
	if _, err := s.publisher.Publish(ctx, e); err != nil {
		s.logger.Error("could not publish a subscription event",
			"subscription_id", sub.ID, "type", eventType, "error", err)
	}
}

func validateCreate(in *CreateInput) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return invalid("name", "must not be empty")
	}
	if in.Amount <= 0 {
		return invalid("amount", "must be greater than zero")
	}
	if in.Currency == "" {
		in.Currency = "IDR"
	}
	if !money.Supported(in.Currency) {
		return invalid("currency", fmt.Sprintf("%q is not a supported currency", in.Currency))
	}
	if in.PaymentToken == "" {
		return invalid("payment_token", "must not be empty; PayMux never handles raw card details")
	}
	switch strings.ToLower(in.IntervalUnit) {
	case "day", "week", "month":
		in.IntervalUnit = strings.ToLower(in.IntervalUnit)
	default:
		return invalid("interval_unit", `must be "day", "week" or "month"`)
	}
	if in.IntervalCount < 1 {
		in.IntervalCount = 1
	}
	if in.StartTime != nil && in.StartTime.Before(time.Now().Add(-time.Minute)) {
		return invalid("start_time", "must not be in the past")
	}
	return nil
}
