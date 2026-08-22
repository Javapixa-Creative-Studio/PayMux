package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/anggapixa/paymux/internal/crypto"
	"github.com/anggapixa/paymux/internal/storage"
)

// Errors the auth domain reports. Handlers map these to responses.
var (
	// ErrInvalidCredentials covers every sign-in failure. It is deliberately
	// undifferentiated so an attacker cannot use the error to learn whether an
	// email address exists.
	ErrInvalidCredentials = errors.New("auth: email or password is incorrect")
	ErrSessionInvalid     = errors.New("auth: session is invalid or has expired")
	ErrEmailTaken         = errors.New("auth: email address is already registered")
	ErrWeakPassword       = errors.New("auth: password does not meet the minimum requirements")
)

// MinPasswordLength is the shortest password PayMux accepts. Length is the
// requirement that actually matters; composition rules mostly push people
// towards predictable substitutions.
const MinPasswordLength = 12

// Service implements administrator sign-in and session management.
type Service struct {
	repo       *Repository
	sessionTTL time.Duration
	logger     *slog.Logger
}

// NewService builds a Service.
func NewService(repo *Repository, sessionTTL time.Duration, logger *slog.Logger) *Service {
	if sessionTTL <= 0 {
		sessionTTL = 12 * time.Hour
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, sessionTTL: sessionTTL, logger: logger}
}

// CreateAdmin registers a new administrator.
func (s *Service) CreateAdmin(ctx context.Context, email, name, password string) (*Admin, error) {
	email = strings.TrimSpace(email)
	if err := ValidateEmail(email); err != nil {
		return nil, err
	}
	if err := ValidatePassword(password); err != nil {
		return nil, err
	}
	hash, err := crypto.HashPassword(password)
	if err != nil {
		return nil, err
	}
	admin := &Admin{Email: email, Name: strings.TrimSpace(name), PasswordHash: hash}
	if err := s.repo.CreateAdmin(ctx, admin); err != nil {
		if storage.IsUniqueViolation(err, ConstraintEmailUnique) {
			return nil, ErrEmailTaken
		}
		return nil, err
	}
	return admin, nil
}

// EnsureBootstrapAdmin creates the first administrator from configuration.
//
// It is a no-op once any administrator exists, so leaving the bootstrap
// credentials in the environment cannot silently re-create or reset an
// account.
func (s *Service) EnsureBootstrapAdmin(ctx context.Context, email, password string) (*Admin, error) {
	if email == "" || password == "" {
		return nil, nil
	}
	count, err := s.repo.CountAdmins(ctx)
	if err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, nil
	}
	admin, err := s.CreateAdmin(ctx, email, "", password)
	if err != nil {
		return nil, fmt.Errorf("auth: create bootstrap administrator: %w", err)
	}
	return admin, nil
}

// LoginInput carries a sign-in attempt.
type LoginInput struct {
	Email     string
	Password  string
	UserAgent string
	IPAddress string
}

// Login verifies credentials and issues a session.
//
// A verification is performed even when the email is unknown, so the response
// time does not reveal whether an account exists.
func (s *Service) Login(ctx context.Context, in LoginInput) (*IssuedSession, error) {
	admin, err := s.repo.GetAdminByEmail(ctx, in.Email)
	if err != nil {
		if storage.IsNotFound(err) {
			burnPasswordTime(in.Password)
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if err := crypto.VerifyPassword(in.Password, admin.PasswordHash); err != nil {
		return nil, ErrInvalidCredentials
	}
	if !admin.Active() {
		return nil, ErrInvalidCredentials
	}

	issued, err := s.issueSession(ctx, admin, in.UserAgent, in.IPAddress)
	if err != nil {
		return nil, err
	}
	if err := s.repo.TouchLogin(ctx, admin.ID); err != nil {
		s.logger.Warn("could not record login time", "admin_id", admin.ID, "error", err)
	}
	return issued, nil
}

func (s *Service) issueSession(ctx context.Context, admin *Admin, userAgent, ip string) (*IssuedSession, error) {
	token, err := generateSessionToken()
	if err != nil {
		return nil, err
	}
	session := &Session{
		AdminID:   admin.ID,
		ExpiresAt: time.Now().Add(s.sessionTTL),
		UserAgent: truncate(userAgent, 400),
		IPAddress: ip,
	}
	if err := s.repo.CreateSession(ctx, session, HashSessionToken(token.Reveal())); err != nil {
		return nil, err
	}
	return &IssuedSession{Session: session, Admin: admin, Token: token}, nil
}

// Authenticate resolves a session token to its administrator.
func (s *Service) Authenticate(ctx context.Context, token string) (*Admin, *Session, error) {
	if token == "" {
		return nil, nil, ErrSessionInvalid
	}
	session, admin, err := s.repo.SessionWithAdmin(ctx, HashSessionToken(token))
	if err != nil {
		if storage.IsNotFound(err) {
			return nil, nil, ErrSessionInvalid
		}
		return nil, nil, err
	}
	if !session.Valid(time.Now()) || !admin.Active() {
		return nil, nil, ErrSessionInvalid
	}
	return admin, session, nil
}

// Logout ends the session identified by token.
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.repo.RevokeSession(ctx, HashSessionToken(token))
}

// ChangePassword replaces an administrator's password after verifying the
// current one, then invalidates their existing sessions.
func (s *Service) ChangePassword(ctx context.Context, adminID, currentPassword, newPassword string) error {
	admin, err := s.repo.GetAdmin(ctx, adminID)
	if err != nil {
		return err
	}
	if err := crypto.VerifyPassword(currentPassword, admin.PasswordHash); err != nil {
		return ErrInvalidCredentials
	}
	if err := ValidatePassword(newPassword); err != nil {
		return err
	}
	hash, err := crypto.HashPassword(newPassword)
	if err != nil {
		return err
	}
	return s.repo.UpdatePassword(ctx, adminID, hash)
}

// ListAdmins returns every administrator.
func (s *Service) ListAdmins(ctx context.Context) ([]*Admin, error) {
	return s.repo.ListAdmins(ctx)
}

// SetDisabled enables or disables an administrator.
func (s *Service) SetDisabled(ctx context.Context, adminID string, disabled bool) (*Admin, error) {
	return s.repo.SetAdminDisabled(ctx, adminID, disabled)
}

// PruneSessions deletes sessions that expired before now.
func (s *Service) PruneSessions(ctx context.Context) (int64, error) {
	return s.repo.DeleteExpiredSessions(ctx, time.Now())
}

// SessionTTL is how long a new session lasts.
func (s *Service) SessionTTL() time.Duration { return s.sessionTTL }

// ---------------------------------------------------------------------------
// Tokens and validation
// ---------------------------------------------------------------------------

// generateSessionToken mints a 256-bit session token.
func generateSessionToken() (crypto.Secret, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: entropy source failed: %w", err)
	}
	return crypto.Secret(base64.RawURLEncoding.EncodeToString(buf)), nil
}

// HashSessionToken derives the stored lookup hash for a session token. The
// token carries full entropy, so an unsalted SHA-256 is appropriate.
func HashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ValidateEmail checks an administrator email address.
func ValidateEmail(email string) error {
	if email == "" {
		return errors.New("auth: email address must not be empty")
	}
	if len(email) > 320 {
		return errors.New("auth: email address is too long")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return errors.New("auth: email address is not valid")
	}
	return nil
}

// ValidatePassword enforces the minimum password policy.
func ValidatePassword(password string) error {
	if len([]rune(password)) < MinPasswordLength {
		return fmt.Errorf("%w: at least %d characters are required", ErrWeakPassword, MinPasswordLength)
	}
	if len(password) > 1024 {
		// Bound the work an unauthenticated caller can force Argon2id to do.
		return fmt.Errorf("%w: at most 1024 characters are allowed", ErrWeakPassword)
	}
	return nil
}

// burnPasswordTime performs a hash verification against a fixed dummy hash so
// that an unknown email costs the same as a known one.
func burnPasswordTime(password string) {
	_ = crypto.VerifyPassword(password, dummyHash)
}

// dummyHash is a real Argon2id hash of an unused password, generated once at
// startup so the comparison cost matches a genuine verification.
var dummyHash = func() string {
	h, err := crypto.HashPassword("paymux-timing-equalizer")
	if err != nil {
		return ""
	}
	return h
}()

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
