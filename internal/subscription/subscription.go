// Package subscription owns PayMux's recurring-billing domain.
//
// Recurring payments depend on gateway features that a merchant must have
// activated. PayMux implements the lifecycle, but never claims a merchant is
// entitled to it: whether recurring billing is available is the gateway's
// answer to give, and its refusal is surfaced rather than hidden (PRD §33).
package subscription

import (
	"time"

	"github.com/anggapixa/paymux/internal/gateway"
)

// Subscription is one recurring charge PayMux manages for an application.
type Subscription struct {
	ID                    string
	ApplicationID         string
	GatewayAccountID      string
	Gateway               string
	GatewaySubscriptionID string

	Name     string
	Amount   int64
	Currency string

	Status        gateway.SubscriptionStatus
	GatewayStatus string

	IntervalUnit  string
	IntervalCount int
	MaxInterval   *int
	StartTime     *time.Time

	PaymentType string
	// PaymentToken is a gateway-issued token. PayMux never stores a card
	// number, and tokenization is the only card flow it supports (PRD §35).
	PaymentToken string

	Metadata    map[string]any
	GatewayData map[string]any

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Active reports whether the subscription is currently charging.
func (s *Subscription) Active() bool {
	return s != nil && s.Status == gateway.SubscriptionActive
}
