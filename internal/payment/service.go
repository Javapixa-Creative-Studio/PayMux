package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/application"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/crypto"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/delivery"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/event"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/ids"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/money"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/storage"
)

// MetricsRecorder observes payment creation. The domain depends on this
// narrow interface rather than the metrics package, so instrumentation stays
// optional and testable.
type MetricsRecorder interface {
	RecordPaymentCreated(gateway string, err error)
}

// Service implements PayMux's payment operations: creating payments at a
// gateway, reading them back, and acting on them.
type Service struct {
	db        *storage.DB
	repo      *Repository
	accounts  *gateway.Repository
	registry  *gateway.Registry
	publisher *delivery.Publisher
	logger    *slog.Logger
	metrics   MetricsRecorder
}

// SetMetrics attaches a recorder. A nil recorder simply disables the counters.
func (s *Service) SetMetrics(recorder MetricsRecorder) { s.metrics = recorder }

func (s *Service) recordCreated(gatewayName string, err error) {
	if s.metrics != nil {
		s.metrics.RecordPaymentCreated(gatewayName, err)
	}
}

// NewService builds a Service.
func NewService(
	db *storage.DB,
	repo *Repository,
	accounts *gateway.Repository,
	registry *gateway.Registry,
	publisher *delivery.Publisher,
	logger *slog.Logger,
) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		db: db, repo: repo, accounts: accounts,
		registry: registry, publisher: publisher, logger: logger,
	}
}

// CreateInput describes a payment an application wants to open.
type CreateInput struct {
	Application           *application.Application
	KeyMode               crypto.KeyMode
	ApplicationOrderID    string
	Amount                int64
	Currency              string
	Customer              *Customer
	Items                 []Item
	EnabledPaymentMethods []string
	ExpiresAt             *time.Time
	CallbackURL           string
	CustomFields          []string
	Metadata              map[string]any
	GatewayOptions        json.RawMessage
}

// Create opens a payment at the gateway and records it.
//
// The order of operations matters. PayMux writes its own payment row first, so
// a gateway transaction can never exist that PayMux does not know about — the
// reverse would produce a payment a customer can pay and PayMux cannot
// attribute. If the gateway then rejects the request, the local row is removed.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Payment, error) {
	if err := validateCreate(&in); err != nil {
		return nil, err
	}

	account, err := s.accountFor(ctx, in.Application)
	if err != nil {
		return nil, err
	}
	// A test key must not be able to move real money, and a live key must not
	// land in the sandbox.
	if in.KeyMode != "" && in.KeyMode != account.ExpectedKeyMode() {
		return nil, fmt.Errorf("%w: a %s key cannot be used against the %s gateway account",
			ErrKeyModeMismatch, in.KeyMode, account.Environment)
	}

	adapter, err := s.registry.For(account)
	if err != nil {
		return nil, err
	}

	options, err := decodeOptions(in.GatewayOptions)
	if err != nil {
		return nil, err
	}

	p := &Payment{
		ID:                 ids.New(ids.Payment),
		ApplicationID:      in.Application.ID,
		GatewayAccountID:   account.ID,
		Gateway:            account.Gateway,
		ApplicationOrderID: in.ApplicationOrderID,
		GatewayOrderID:     ids.New(ids.Order),
		Amount:             in.Amount,
		Currency:           money.NormalizeCurrency(in.Currency),
		NormalizedStatus:   gateway.StatusPending,
		GatewayStatus:      "",
		Metadata:           in.Metadata,
		GatewayOptions:     options,
		Customer:           in.Customer,
		Items:              in.Items,
		ExpiresAt:          in.ExpiresAt,
	}

	if err := s.repo.Create(ctx, p); err != nil {
		if storage.IsUniqueViolation(err, ConstraintOrderUnique) {
			return nil, ErrDuplicateOrderID
		}
		return nil, err
	}

	created, createErr := adapter.CreatePayment(ctx, gateway.CreatePaymentRequest{
		OrderID:               p.GatewayOrderID,
		Amount:                p.Amount,
		Currency:              p.Currency,
		Customer:              toGatewayCustomer(in.Customer),
		Items:                 toGatewayItems(in.Items),
		EnabledPaymentMethods: in.EnabledPaymentMethods,
		ExpiresAt:             in.ExpiresAt,
		CallbackURL:           in.CallbackURL,
		CustomFields:          in.CustomFields,
		Options:               in.GatewayOptions,
	})
	s.recordCreated(account.Gateway, createErr)
	if err := createErr; err != nil {
		// The gateway never opened the payment, so PayMux must not keep a
		// record implying it did.
		if cleanupErr := s.deletePayment(ctx, p.ID); cleanupErr != nil {
			s.logger.Error("could not remove a payment the gateway rejected",
				"payment_id", p.ID, "error", cleanupErr)
		}
		return nil, err
	}

	p.SnapToken = created.Token
	p.SnapRedirectURL = created.RedirectURL
	p.GatewayStatus = created.Status
	if created.ExpiresAt != nil {
		p.ExpiresAt = created.ExpiresAt
	}
	if err := s.repo.SetCheckoutSession(ctx, p.ID, created.Token, created.RedirectURL, created.ExpiresAt); err != nil {
		return nil, err
	}

	s.publishPaymentEvent(ctx, p, event.PaymentCreated, "")
	return p, nil
}

// deletePayment removes a payment that was never opened at the gateway.
func (s *Service) deletePayment(ctx context.Context, paymentID string) error {
	_, err := s.db.FromContext(ctx).Exec(ctx, `DELETE FROM payments WHERE id = $1`, paymentID)
	if err != nil {
		return fmt.Errorf("payment: remove rejected payment: %w", err)
	}
	return nil
}

// Get returns a payment owned by the application.
func (s *Service) Get(ctx context.Context, applicationID, paymentID string) (*Payment, error) {
	p, err := s.repo.GetForApplication(ctx, applicationID, paymentID)
	if err != nil {
		return nil, err
	}
	if err := s.repo.LoadDetails(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// GetAny returns a payment without an ownership filter. It is for
// administrators, who see every application.
func (s *Service) GetAny(ctx context.Context, paymentID string) (*Payment, error) {
	p, err := s.repo.Get(ctx, paymentID)
	if err != nil {
		return nil, err
	}
	if err := s.repo.LoadDetails(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// List returns a page of payments matching the filter.
func (s *Service) List(ctx context.Context, filter Filter, page storage.Page) (storage.List[*Payment], error) {
	return s.repo.List(ctx, filter, page)
}

// Sync reconciles a payment with the gateway's authoritative state (PRD §27).
func (s *Service) Sync(ctx context.Context, applicationID, paymentID string) (*Payment, error) {
	p, err := s.load(ctx, applicationID, paymentID)
	if err != nil {
		return nil, err
	}
	adapter, err := s.adapterFor(ctx, p)
	if err != nil {
		return nil, err
	}

	txn, err := adapter.GetTransaction(ctx, p.GatewayOrderID)
	if err != nil {
		return nil, err
	}
	return s.applyGatewayTransaction(ctx, p, txn)
}

// Cancel cancels a payment that has not settled (PRD §28).
func (s *Service) Cancel(ctx context.Context, applicationID, paymentID string) (*Payment, error) {
	p, err := s.load(ctx, applicationID, paymentID)
	if err != nil {
		return nil, err
	}
	if !p.Cancelable() {
		return nil, fmt.Errorf("%w: the payment is %s", ErrNotCancelable, p.NormalizedStatus)
	}
	adapter, err := s.adapterFor(ctx, p)
	if err != nil {
		return nil, err
	}
	txn, err := adapter.CancelTransaction(ctx, p.GatewayOrderID)
	if err != nil {
		return nil, err
	}
	return s.applyGatewayTransaction(ctx, p, txn)
}

// Expire expires a pending payment (PRD §29).
func (s *Service) Expire(ctx context.Context, applicationID, paymentID string) (*Payment, error) {
	p, err := s.load(ctx, applicationID, paymentID)
	if err != nil {
		return nil, err
	}
	adapter, err := s.adapterFor(ctx, p)
	if err != nil {
		return nil, err
	}
	txn, err := adapter.ExpireTransaction(ctx, p.GatewayOrderID)
	if err != nil {
		return nil, err
	}
	return s.applyGatewayTransaction(ctx, p, txn)
}

// CancelCheckoutSession retires the hosted checkout session for a payment
// (PRD §30).
func (s *Service) CancelCheckoutSession(ctx context.Context, applicationID, paymentID string) (*Payment, error) {
	p, err := s.load(ctx, applicationID, paymentID)
	if err != nil {
		return nil, err
	}
	adapter, err := s.adapterFor(ctx, p)
	if err != nil {
		return nil, err
	}
	checkout, ok := adapter.(gateway.CheckoutGateway)
	if !ok {
		return nil, gateway.ErrNotSupported
	}
	if err := checkout.CancelCheckoutSession(ctx, p.GatewayOrderID); err != nil {
		return nil, err
	}
	return s.Sync(ctx, applicationID, paymentID)
}

// applyGatewayTransaction folds a gateway transaction into PayMux's state and
// publishes the resulting event.
func (s *Service) applyGatewayTransaction(ctx context.Context, p *Payment, txn *gateway.Transaction) (*Payment, error) {
	if err := s.repo.UpsertGatewayTransaction(ctx, p.ID, txn); err != nil {
		s.logger.Warn("could not record the gateway transaction",
			"payment_id", p.ID, "error", err)
	}

	if txn.Normalized == "" {
		// The gateway reported a status PayMux does not map. The payment is
		// left alone and the unknown status is surfaced rather than guessed.
		s.logger.Warn("gateway reported an unmapped transaction status",
			"payment_id", p.ID, "gateway_status", txn.Status)
		if err := s.repo.TouchSynced(ctx, p.ID); err != nil {
			return nil, err
		}
		return p, nil
	}

	updated, err := s.repo.ApplyState(ctx, p.ID, StateUpdate{
		NormalizedStatus:     txn.Normalized,
		GatewayStatus:        txn.Status,
		FraudStatus:          txn.FraudStatus,
		GatewayTransactionID: txn.TransactionID,
		PaymentType:          txn.PaymentType,
		PaymentMethod:        txn.PaymentType,
		GatewayData:          txn.Raw,
		OccurredAt:           txn.SettlementTime,
		MarkSynced:           true,
	})
	if err != nil {
		if errors.Is(err, ErrStaleTransition) {
			// PayMux already knows a later state; nothing to do.
			if touchErr := s.repo.TouchSynced(ctx, p.ID); touchErr != nil {
				return nil, touchErr
			}
			return p, nil
		}
		return nil, err
	}

	if eventType, ok := event.TypeForStatus(updated.NormalizedStatus); ok {
		s.publishPaymentEvent(ctx, updated, eventType, "")
	}
	return updated, nil
}

// load fetches a payment, applying the ownership filter when the caller is an
// application rather than an administrator.
func (s *Service) load(ctx context.Context, applicationID, paymentID string) (*Payment, error) {
	if applicationID == "" {
		return s.repo.Get(ctx, paymentID)
	}
	return s.repo.GetForApplication(ctx, applicationID, paymentID)
}

func (s *Service) adapterFor(ctx context.Context, p *Payment) (gateway.Gateway, error) {
	account, err := s.accounts.Get(ctx, p.GatewayAccountID)
	if err != nil {
		return nil, err
	}
	return s.registry.For(account)
}

// accountFor resolves which gateway account an application transacts through.
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

	// No explicit assignment: fall back to the default account for the only
	// gateway V1 supports.
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

// publishPaymentEvent emits an event for a payment state.
//
// A failure to publish is logged but does not fail the operation that caused
// it: the payment state is already correct, and the notification pipeline will
// re-derive the event when the gateway next reports the same state.
func (s *Service) publishPaymentEvent(ctx context.Context, p *Payment, eventType event.Type, gatewayEventID string) {
	e := &event.Event{
		ApplicationID:  p.ApplicationID,
		Type:           eventType,
		Gateway:        p.Gateway,
		PaymentID:      p.ID,
		GatewayEventID: gatewayEventID,
		DedupeKey:      event.PaymentDedupeKey(p.ID, eventType, paymentDiscriminator(p, eventType)),
		Payload:        BuildPayload(p, eventType),
	}
	if _, err := s.publisher.Publish(ctx, e); err != nil {
		s.logger.Error("could not publish a payment event",
			"payment_id", p.ID, "type", eventType, "error", err)
	}
}

// paymentDiscriminator distinguishes repeated events of the same type on one
// payment.
//
// Most payment states happen once, so the payment and type alone identify the
// occurrence. Refunds are the exception: a payment can be partially refunded
// several times, and each one moves the refunded total, so the total is what
// separates those events from one another.
func paymentDiscriminator(p *Payment, eventType event.Type) string {
	switch eventType {
	case event.PaymentRefunded, event.PaymentPartiallyRefunded:
		return strconv.FormatInt(p.RefundedAmount, 10)
	default:
		return ""
	}
}

// BuildPayload renders a payment as an event payload (PRD §41).
func BuildPayload(p *Payment, eventType event.Type) event.Payload {
	return event.Payload{
		Type:                 eventType,
		Gateway:              p.Gateway,
		ApplicationID:        p.ApplicationID,
		PaymentID:            p.ID,
		ApplicationOrderID:   p.ApplicationOrderID,
		GatewayOrderID:       p.GatewayOrderID,
		GatewayTransactionID: p.GatewayTransactionID,
		Status:               p.NormalizedStatus,
		GatewayStatus:        p.GatewayStatus,
		FraudStatus:          p.FraudStatus,
		PaymentType:          p.PaymentType,
		Amount:               p.Amount,
		RefundedAmount:       p.RefundedAmount,
		Currency:             p.Currency,
		Metadata:             p.Metadata,
		CreatedAt:            time.Now().UTC(),
		GatewayData:          p.GatewayData,
	}
}

// ---------------------------------------------------------------------------
// Validation and conversion
// ---------------------------------------------------------------------------

func validateCreate(in *CreateInput) error {
	in.ApplicationOrderID = strings.TrimSpace(in.ApplicationOrderID)
	if in.ApplicationOrderID == "" {
		return invalid("application_order_id", "must not be empty")
	}
	if len(in.ApplicationOrderID) > 128 {
		return invalid("application_order_id", "must be at most 128 characters")
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
	if in.ExpiresAt != nil && !in.ExpiresAt.After(time.Now()) {
		return invalid("expires_at", "must be in the future")
	}
	for i, item := range in.Items {
		if item.Name == "" {
			return invalid(fmt.Sprintf("items[%d].name", i), "must not be empty")
		}
		if item.Quantity < 1 {
			return invalid(fmt.Sprintf("items[%d].quantity", i), "must be at least 1")
		}
		if item.Price < 0 {
			return invalid(fmt.Sprintf("items[%d].price", i), "must not be negative")
		}
	}
	if len(in.CustomFields) > 3 {
		return invalid("custom_fields", "at most three custom fields are supported")
	}
	return nil
}

func decodeOptions(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, invalid("gateway_options", "must be a JSON object")
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func toGatewayCustomer(c *Customer) gateway.Customer {
	if c == nil {
		return gateway.Customer{}
	}
	out := gateway.Customer{
		FirstName: c.FirstName,
		LastName:  c.LastName,
		Email:     c.Email,
		Phone:     c.Phone,
	}
	if c.Billing != nil {
		out.Billing = toGatewayAddress(c.Billing)
	}
	if c.Shipping != nil {
		out.Shipping = toGatewayAddress(c.Shipping)
	}
	return out
}

func toGatewayAddress(a *Address) *gateway.Address {
	return &gateway.Address{
		FirstName:   a.FirstName,
		LastName:    a.LastName,
		Phone:       a.Phone,
		Address:     a.Address,
		City:        a.City,
		PostalCode:  a.PostalCode,
		CountryCode: a.CountryCode,
	}
}

func toGatewayItems(items []Item) []gateway.Item {
	if len(items) == 0 {
		return nil
	}
	out := make([]gateway.Item, 0, len(items))
	for _, item := range items {
		out = append(out, gateway.Item{
			SKU:      item.SKU,
			Name:     item.Name,
			Price:    item.Price,
			Quantity: item.Quantity,
			Category: item.Category,
			Merchant: item.Merchant,
			URL:      item.URL,
		})
	}
	return out
}
