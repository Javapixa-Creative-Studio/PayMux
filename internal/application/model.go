// Package application owns PayMux tenants: the applications that share a
// gateway account, their API credentials and their webhook destinations.
package application

import (
	"time"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/crypto"
)

// Application is one product using PayMux. It is the unit of isolation:
// ownership of every payment, event and delivery traces back to exactly one
// application (PRD §49).
type Application struct {
	ID               string
	Name             string
	Slug             string
	Description      string
	GatewayAccountID string
	DisabledAt       *time.Time
	Metadata         map[string]any
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Active reports whether the application may create or receive traffic.
func (a *Application) Active() bool { return a != nil && a.DisabledAt == nil }

// APIKey is a credential belonging to an application. Only the hash is
// persisted; the plaintext exists once, in the response that created it.
type APIKey struct {
	ID            string
	ApplicationID string
	Name          string
	Mode          crypto.KeyMode
	DisplayPrefix string
	LastUsedAt    *time.Time
	ExpiresAt     *time.Time
	RevokedAt     *time.Time
	CreatedAt     time.Time
}

// Usable reports whether the key may authenticate a request at time now.
func (k *APIKey) Usable(now time.Time) bool {
	if k == nil || k.RevokedAt != nil {
		return false
	}
	return k.ExpiresAt == nil || k.ExpiresAt.After(now)
}

// Destination is a URL PayMux delivers an application's events to.
type Destination struct {
	ID            string
	ApplicationID string
	URL           string
	Description   string
	// Secret is populated only when a caller explicitly needs to sign with it.
	Secret     crypto.Secret
	EventTypes []string
	Enabled    bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Accepts reports whether this destination wants the given event type. An
// empty filter means every type, which is the default for a new destination.
func (d *Destination) Accepts(eventType string) bool {
	if d == nil || !d.Enabled {
		return false
	}
	if len(d.EventTypes) == 0 {
		return true
	}
	for _, t := range d.EventTypes {
		if t == eventType {
			return true
		}
	}
	return false
}
