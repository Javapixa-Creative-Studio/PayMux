package application

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/crypto"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/netsafe"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/storage"
)

// Domain errors. Handlers map these onto the public error contract, which
// keeps HTTP concerns out of the domain.
var (
	ErrSlugTaken     = errors.New("application: slug is already in use")
	ErrNotFound      = storage.ErrNotFound
	ErrDisabled      = errors.New("application: application is disabled")
	ErrKeyNotUsable  = errors.New("application: api key is revoked or expired")
	ErrDestinationIn = errors.New("application: destination URL is not permitted")
)

// ValidationError reports a specific invalid field.
type ValidationError struct {
	Field   string
	Message string
}

// Error implements error.
func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

func invalid(field, message string) error { return &ValidationError{Field: field, Message: message} }

// Service applies PayMux's rules for managing applications and credentials.
type Service struct {
	repo  *Repository
	guard *netsafe.Guard
}

// NewService builds a Service.
func NewService(repo *Repository, guard *netsafe.Guard) *Service {
	return &Service{repo: repo, guard: guard}
}

// CreateInput describes a new application.
type CreateInput struct {
	Name             string
	Slug             string
	Description      string
	GatewayAccountID string
	Metadata         map[string]any
}

// Create validates and stores a new application.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Application, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, invalid("name", "must not be empty")
	}
	if len(name) > 120 {
		return nil, invalid("name", "must be at most 120 characters")
	}

	slug := strings.TrimSpace(in.Slug)
	if slug == "" {
		slug = Slugify(name)
	}
	if err := ValidateSlug(slug); err != nil {
		return nil, err
	}
	if len(in.Description) > 1000 {
		return nil, invalid("description", "must be at most 1000 characters")
	}

	app := &Application{
		Name:             name,
		Slug:             slug,
		Description:      strings.TrimSpace(in.Description),
		GatewayAccountID: in.GatewayAccountID,
		Metadata:         in.Metadata,
	}
	if err := s.repo.CreateApplication(ctx, app); err != nil {
		if storage.IsUniqueViolation(err, ConstraintSlugUnique) {
			return nil, ErrSlugTaken
		}
		return nil, err
	}
	return app, nil
}

// Get loads an application.
func (s *Service) Get(ctx context.Context, id string) (*Application, error) {
	return s.repo.GetApplication(ctx, id)
}

// List returns a page of applications.
func (s *Service) List(ctx context.Context, page storage.Page) (storage.List[*Application], error) {
	return s.repo.ListApplications(ctx, page)
}

// Update applies a partial update.
func (s *Service) Update(ctx context.Context, id string, update ApplicationUpdate) (*Application, error) {
	if update.Name != nil {
		name := strings.TrimSpace(*update.Name)
		if name == "" {
			return nil, invalid("name", "must not be empty")
		}
		update.Name = &name
	}
	app, err := s.repo.UpdateApplication(ctx, id, update)
	if err != nil {
		if storage.IsUniqueViolation(err, ConstraintSlugUnique) {
			return nil, ErrSlugTaken
		}
		return nil, err
	}
	return app, nil
}

// IssuedKey pairs a stored key with the plaintext shown once at creation.
type IssuedKey struct {
	Key       *APIKey
	Plaintext crypto.Secret
}

// CreateAPIKey mints a credential for an application.
func (s *Service) CreateAPIKey(ctx context.Context, applicationID, name string, mode crypto.KeyMode, expiresAt *time.Time) (*IssuedKey, error) {
	if !mode.Valid() {
		return nil, invalid("mode", `must be "live" or "test"`)
	}
	if expiresAt != nil && !expiresAt.After(time.Now()) {
		return nil, invalid("expires_at", "must be in the future")
	}
	if _, err := s.repo.GetApplication(ctx, applicationID); err != nil {
		return nil, err
	}

	generated, err := crypto.GenerateAPIKey(mode)
	if err != nil {
		return nil, err
	}
	key := &APIKey{
		ApplicationID: applicationID,
		Name:          strings.TrimSpace(name),
		Mode:          mode,
		DisplayPrefix: generated.DisplayPrefix,
		ExpiresAt:     expiresAt,
	}
	if err := s.repo.CreateAPIKey(ctx, key, generated.Hash); err != nil {
		return nil, err
	}
	return &IssuedKey{Key: key, Plaintext: generated.Plaintext}, nil
}

// ListAPIKeys returns an application's credentials.
func (s *Service) ListAPIKeys(ctx context.Context, applicationID string) ([]*APIKey, error) {
	return s.repo.ListAPIKeys(ctx, applicationID)
}

// RevokeAPIKey permanently disables a credential.
func (s *Service) RevokeAPIKey(ctx context.Context, applicationID, keyID string) (*APIKey, error) {
	return s.repo.RevokeAPIKey(ctx, applicationID, keyID)
}

// Authenticate resolves a plaintext API key to its application.
//
// Every failure returns the same error so a caller cannot distinguish an
// unknown key from a revoked one, or a valid key on a disabled application
// from an invalid key.
func (s *Service) Authenticate(ctx context.Context, plaintext string) (*Authenticated, error) {
	if _, err := crypto.ParseAPIKeyMode(plaintext); err != nil {
		return nil, ErrKeyNotUsable
	}
	auth, err := s.repo.AuthenticateAPIKey(ctx, crypto.HashAPIKey(plaintext))
	if err != nil {
		if storage.IsNotFound(err) {
			return nil, ErrKeyNotUsable
		}
		return nil, err
	}
	if !auth.APIKey.Usable(time.Now()) {
		return nil, ErrKeyNotUsable
	}
	if !auth.Application.Active() {
		return nil, ErrDisabled
	}
	if err := s.repo.TouchAPIKeyUsed(ctx, auth.APIKey.ID); err != nil {
		// A failed usage timestamp must not fail the request it describes.
		_ = err
	}
	return auth, nil
}

// CreateDestinationInput describes a new webhook destination.
type CreateDestinationInput struct {
	URL         string
	Description string
	EventTypes  []string
	Enabled     *bool
}

// CreateDestination registers a delivery target and mints its signing secret.
func (s *Service) CreateDestination(ctx context.Context, applicationID string, in CreateDestinationInput) (*Destination, error) {
	url := strings.TrimSpace(in.URL)
	if url == "" {
		return nil, invalid("url", "must not be empty")
	}
	if err := s.guard.Validate(ctx, url); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrDestinationIn, err.Error())
	}
	if _, err := s.repo.GetApplication(ctx, applicationID); err != nil {
		return nil, err
	}

	secret, err := crypto.GenerateWebhookSecret()
	if err != nil {
		return nil, err
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	dst := &Destination{
		ApplicationID: applicationID,
		URL:           url,
		Description:   strings.TrimSpace(in.Description),
		Secret:        secret,
		EventTypes:    in.EventTypes,
		Enabled:       enabled,
	}
	if err := s.repo.CreateDestination(ctx, dst); err != nil {
		return nil, err
	}
	return dst, nil
}

// ListDestinations returns an application's destinations.
func (s *Service) ListDestinations(ctx context.Context, applicationID string) ([]*Destination, error) {
	return s.repo.ListDestinations(ctx, applicationID)
}

// GetDestination loads one destination.
func (s *Service) GetDestination(ctx context.Context, applicationID, id string) (*Destination, error) {
	return s.repo.GetDestination(ctx, applicationID, id)
}

// UpdateDestination applies a partial update, re-validating a changed URL.
func (s *Service) UpdateDestination(ctx context.Context, applicationID, id string, update DestinationUpdate) (*Destination, error) {
	if update.URL != nil {
		url := strings.TrimSpace(*update.URL)
		if err := s.guard.Validate(ctx, url); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrDestinationIn, err.Error())
		}
		update.URL = &url
	}
	return s.repo.UpdateDestination(ctx, applicationID, id, update)
}

// RotateDestinationSecret issues a new signing secret for a destination.
//
// Rotation takes effect immediately: deliveries already queued are signed with
// whichever secret is current when the worker sends them, so receivers should
// accept both values during a rotation window.
func (s *Service) RotateDestinationSecret(ctx context.Context, applicationID, id string) (*Destination, error) {
	secret, err := crypto.GenerateWebhookSecret()
	if err != nil {
		return nil, err
	}
	return s.repo.UpdateDestination(ctx, applicationID, id, DestinationUpdate{Secret: secret})
}

// DeleteDestination removes a destination.
func (s *Service) DeleteDestination(ctx context.Context, applicationID, id string) error {
	return s.repo.DeleteDestination(ctx, applicationID, id)
}

// ---------------------------------------------------------------------------
// Slugs
// ---------------------------------------------------------------------------

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Slugify derives a URL-safe slug from a display name.
func Slugify(name string) string {
	var b strings.Builder
	lastDash := true // leading dashes are suppressed
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case unicode.IsLetter(r) && r < unicode.MaxASCII, unicode.IsDigit(r) && r < unicode.MaxASCII:
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// ValidateSlug enforces the slug format used in URLs and configuration.
func ValidateSlug(slug string) error {
	if slug == "" {
		return invalid("slug", "must not be empty")
	}
	if len(slug) > 64 {
		return invalid("slug", "must be at most 64 characters")
	}
	if !slugPattern.MatchString(slug) {
		return invalid("slug", "must contain only lowercase letters, digits and single hyphens")
	}
	return nil
}
