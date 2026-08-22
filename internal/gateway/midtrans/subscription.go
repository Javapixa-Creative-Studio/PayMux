package midtrans

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/money"
)

// Subscription support (PRD §33).
//
// Recurring billing must be activated on the merchant account before these
// calls succeed. The adapter implementing the interface says only that PayMux
// can make the call — not that the merchant is entitled to; Midtrans decides
// that, and its rejection is surfaced unchanged.

// CreateSubscription starts a recurring subscription.
func (a *Adapter) CreateSubscription(ctx context.Context, req gateway.CreateSubscriptionRequest) (*gateway.Subscription, error) {
	amount, err := money.Format(req.Amount, req.Currency)
	if err != nil {
		return nil, fmt.Errorf("midtrans: %w", err)
	}
	if req.PaymentToken == "" {
		return nil, fmt.Errorf("midtrans: a payment token is required to create a subscription")
	}
	intervalUnit := strings.ToLower(req.IntervalUnit)
	if !validIntervalUnit(intervalUnit) {
		return nil, fmt.Errorf("midtrans: interval unit %q is not supported", req.IntervalUnit)
	}
	interval := req.IntervalCount
	if interval < 1 {
		interval = 1
	}

	payload := subscriptionRequest{
		Name:        req.Name,
		Amount:      amount,
		Currency:    money.NormalizeCurrency(req.Currency),
		PaymentType: defaultString(req.PaymentType, "credit_card"),
		Token:       req.PaymentToken,
		Schedule: subscriptionSchedule{
			Interval:     interval,
			IntervalUnit: intervalUnit,
			MaxInterval:  req.MaxInterval,
		},
		Metadata:        req.Metadata,
		CustomerDetails: buildCustomer(req.Customer),
	}
	if req.StartTime != nil {
		payload.Schedule.StartTime = formatTime(*req.StartTime)
	}

	var resp subscriptionResponse
	if err := a.client.do(ctx, http.MethodPost, a.client.coreEndpoint("/v1/subscriptions"), payload, &resp); err != nil {
		return nil, translateSubscriptionError(err)
	}
	return toSubscription(&resp), nil
}

// GetSubscription fetches a subscription's current state.
func (a *Adapter) GetSubscription(ctx context.Context, id string) (*gateway.Subscription, error) {
	var resp subscriptionResponse
	path := "/v1/subscriptions/" + url.PathEscape(id)
	if err := a.client.do(ctx, http.MethodGet, a.client.coreEndpoint(path), nil, &resp); err != nil {
		return nil, translateSubscriptionError(err)
	}
	return toSubscription(&resp), nil
}

// UpdateSubscription changes a live subscription.
func (a *Adapter) UpdateSubscription(ctx context.Context, id string, req gateway.UpdateSubscriptionRequest) (*gateway.Subscription, error) {
	// Midtrans's update is a partial patch: only the supplied fields change.
	payload := map[string]any{}
	if req.Name != nil {
		payload["name"] = *req.Name
	}
	if req.Amount != nil {
		// The currency cannot change, so the existing one is read back first.
		current, err := a.GetSubscription(ctx, id)
		if err != nil {
			return nil, err
		}
		amount, err := money.Format(*req.Amount, current.Currency)
		if err != nil {
			return nil, fmt.Errorf("midtrans: %w", err)
		}
		payload["amount"] = amount
	}
	if req.PaymentToken != nil {
		payload["token"] = *req.PaymentToken
	}
	if req.IntervalUnit != nil || req.IntervalCount != nil {
		schedule := map[string]any{}
		if req.IntervalUnit != nil {
			unit := strings.ToLower(*req.IntervalUnit)
			if !validIntervalUnit(unit) {
				return nil, fmt.Errorf("midtrans: interval unit %q is not supported", *req.IntervalUnit)
			}
			schedule["interval_unit"] = unit
		}
		if req.IntervalCount != nil {
			schedule["interval"] = *req.IntervalCount
		}
		payload["schedule"] = schedule
	}
	if len(payload) == 0 {
		return a.GetSubscription(ctx, id)
	}

	path := "/v1/subscriptions/" + url.PathEscape(id)
	if err := a.client.do(ctx, http.MethodPatch, a.client.coreEndpoint(path), payload, nil); err != nil {
		return nil, translateSubscriptionError(err)
	}
	return a.GetSubscription(ctx, id)
}

// EnableSubscription resumes a disabled subscription.
func (a *Adapter) EnableSubscription(ctx context.Context, id string) error {
	return a.subscriptionAction(ctx, id, "enable")
}

// DisableSubscription pauses a subscription without cancelling it.
func (a *Adapter) DisableSubscription(ctx context.Context, id string) error {
	return a.subscriptionAction(ctx, id, "disable")
}

// CancelSubscription ends a subscription permanently.
func (a *Adapter) CancelSubscription(ctx context.Context, id string) error {
	return a.subscriptionAction(ctx, id, "cancel")
}

func (a *Adapter) subscriptionAction(ctx context.Context, id, action string) error {
	path := "/v1/subscriptions/" + url.PathEscape(id) + "/" + action
	if err := a.client.do(ctx, http.MethodPost, a.client.coreEndpoint(path), nil, nil); err != nil {
		return translateSubscriptionError(err)
	}
	return nil
}

func toSubscription(resp *subscriptionResponse) *gateway.Subscription {
	currency := resp.Currency
	if currency == "" {
		currency = "IDR"
	}
	amount, _ := money.Parse(resp.Amount, currency)
	return &gateway.Subscription{
		ID:            resp.ID,
		Name:          resp.Name,
		Amount:        amount,
		Currency:      money.NormalizeCurrency(currency),
		Status:        resp.Status,
		Normalized:    normalizeSubscriptionStatus(resp.Status),
		PaymentType:   resp.PaymentType,
		IntervalUnit:  resp.Schedule.IntervalUnit,
		IntervalCount: resp.Schedule.Interval,
		Raw:           toRaw(resp),
	}
}

// translateSubscriptionError distinguishes "the merchant is not entitled to
// recurring billing" from ordinary failures, because that distinction is what
// an operator needs in order to act (PRD §33).
func translateSubscriptionError(err error) error {
	var gwErr *gateway.Error
	if !asGatewayError(err, &gwErr) {
		return err
	}
	message := strings.ToLower(gwErr.Message)
	switch {
	case gwErr.StatusCode == http.StatusNotFound || gwErr.Code == "404":
		return gateway.ErrTransactionNotFound
	case strings.Contains(message, "not activated"),
		strings.Contains(message, "not enabled"),
		strings.Contains(message, "not allowed"):
		return fmt.Errorf("%w: %s", gateway.ErrNotSupported, gwErr.Message)
	default:
		return err
	}
}

func validIntervalUnit(unit string) bool {
	switch unit {
	case "day", "week", "month":
		return true
	}
	return false
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
