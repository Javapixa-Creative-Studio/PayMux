// Package auth handles the two principals PayMux recognises: administrators
// using the dashboard, and applications calling the API with an API key.
package auth

import (
	"time"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/crypto"
)

// Admin is a dashboard operator.
type Admin struct {
	ID           string
	Email        string
	Name         string
	PasswordHash string
	DisabledAt   *time.Time
	LastLoginAt  *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Active reports whether the administrator may sign in.
func (a *Admin) Active() bool { return a != nil && a.DisabledAt == nil }

// Session is an authenticated dashboard session.
//
// Only the hash of the session token is stored, so a database disclosure does
// not hand over live sessions.
type Session struct {
	ID        string
	AdminID   string
	ExpiresAt time.Time
	RevokedAt *time.Time
	UserAgent string
	IPAddress string
	CreatedAt time.Time
}

// Valid reports whether the session may authenticate a request at time now.
func (s *Session) Valid(now time.Time) bool {
	return s != nil && s.RevokedAt == nil && s.ExpiresAt.After(now)
}

// IssuedSession pairs a stored session with the token handed to the browser.
type IssuedSession struct {
	Session *Session
	Admin   *Admin
	Token   crypto.Secret
}
