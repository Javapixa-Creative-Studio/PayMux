package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/anggapixa/paymux/internal/crypto"
	"github.com/anggapixa/paymux/internal/ids"
	"github.com/anggapixa/paymux/internal/storage"
)

// Constraint names the repository interprets. They are referenced by name so
// a duplicate can be reported against the field that actually collided.
const (
	ConstraintSlugUnique = "applications_slug_key"
	ConstraintKeyHash    = "application_api_keys_key_hash_key"
)

// Repository reads and writes applications, API keys and destinations.
//
// It owns the encryption boundary for destination secrets: callers hand over
// and receive plaintext, and nothing outside this type sees a sealed value.
type Repository struct {
	db     *storage.DB
	sealer *crypto.Sealer
}

// NewRepository builds a Repository.
func NewRepository(db *storage.DB, sealer *crypto.Sealer) *Repository {
	return &Repository{db: db, sealer: sealer}
}

func (r *Repository) q(ctx context.Context) storage.Querier { return r.db.FromContext(ctx) }

// secretContext binds a sealed webhook secret to the destination that owns it.
func secretContext(destinationID string) string {
	return "webhook_destination:" + destinationID + ":secret"
}

// ---------------------------------------------------------------------------
// Applications
// ---------------------------------------------------------------------------

const applicationColumns = `
	id, name, slug, description, coalesce(gateway_account_id, ''),
	disabled_at, metadata, created_at, updated_at`

// CreateApplication inserts a new application.
func (r *Repository) CreateApplication(ctx context.Context, app *Application) error {
	if app.ID == "" {
		app.ID = ids.New(ids.Application)
	}
	metadata, err := marshalMetadata(app.Metadata)
	if err != nil {
		return err
	}
	row := r.q(ctx).QueryRow(ctx, `
		INSERT INTO applications (id, name, slug, description, gateway_account_id, metadata)
		VALUES ($1, $2, $3, $4, nullif($5, ''), $6)
		RETURNING `+applicationColumns,
		app.ID, app.Name, app.Slug, app.Description, app.GatewayAccountID, metadata,
	)
	return scanApplication(row, app)
}

// GetApplication loads one application by identifier.
func (r *Repository) GetApplication(ctx context.Context, id string) (*Application, error) {
	var app Application
	row := r.q(ctx).QueryRow(ctx, `SELECT `+applicationColumns+` FROM applications WHERE id = $1`, id)
	if err := scanApplication(row, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// GetApplicationBySlug loads one application by its case-insensitive slug.
func (r *Repository) GetApplicationBySlug(ctx context.Context, slug string) (*Application, error) {
	var app Application
	row := r.q(ctx).QueryRow(ctx,
		`SELECT `+applicationColumns+` FROM applications WHERE lower(slug) = lower($1)`, slug)
	if err := scanApplication(row, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// ListApplications returns a page of applications, newest first.
func (r *Repository) ListApplications(ctx context.Context, page storage.Page) (storage.List[*Application], error) {
	page = page.Normalize()
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+applicationColumns+`
		FROM applications
		WHERE ($1 = '' OR id < $1)
		ORDER BY id DESC
		LIMIT $2`,
		page.StartingAfter, page.FetchLimit(),
	)
	if err != nil {
		return storage.List[*Application]{}, fmt.Errorf("application: list: %w", err)
	}
	defer rows.Close()

	var out []*Application
	for rows.Next() {
		var app Application
		if err := scanApplication(rows, &app); err != nil {
			return storage.List[*Application]{}, err
		}
		out = append(out, &app)
	}
	if err := rows.Err(); err != nil {
		return storage.List[*Application]{}, fmt.Errorf("application: list: %w", err)
	}
	return storage.NewList(out, page), nil
}

// ApplicationUpdate carries the mutable fields of an application. A nil field
// is left unchanged, which lets PATCH semantics distinguish "omitted" from
// "set to empty".
type ApplicationUpdate struct {
	Name             *string
	Description      *string
	GatewayAccountID *string
	Metadata         map[string]any
	Disabled         *bool
}

// UpdateApplication applies a partial update and returns the stored row.
func (r *Repository) UpdateApplication(ctx context.Context, id string, update ApplicationUpdate) (*Application, error) {
	metadata, err := marshalMetadataPtr(update.Metadata)
	if err != nil {
		return nil, err
	}
	var disabled *bool = update.Disabled

	var app Application
	row := r.q(ctx).QueryRow(ctx, `
		UPDATE applications SET
			name               = coalesce($2, name),
			description        = coalesce($3, description),
			gateway_account_id = CASE WHEN $4::text IS NULL THEN gateway_account_id
			                          ELSE nullif($4, '') END,
			metadata           = coalesce($5, metadata),
			disabled_at        = CASE WHEN $6::boolean IS NULL THEN disabled_at
			                          WHEN $6 THEN coalesce(disabled_at, now())
			                          ELSE NULL END,
			updated_at         = now()
		WHERE id = $1
		RETURNING `+applicationColumns,
		id, update.Name, update.Description, update.GatewayAccountID, metadata, disabled,
	)
	if err := scanApplication(row, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// ---------------------------------------------------------------------------
// API keys
// ---------------------------------------------------------------------------

const apiKeyColumns = `
	id, application_id, name, mode, display_prefix,
	last_used_at, expires_at, revoked_at, created_at`

// CreateAPIKey stores the hash of a freshly generated key.
func (r *Repository) CreateAPIKey(ctx context.Context, key *APIKey, hash string) error {
	if key.ID == "" {
		key.ID = ids.New(ids.APIKey)
	}
	row := r.q(ctx).QueryRow(ctx, `
		INSERT INTO application_api_keys
			(id, application_id, name, mode, key_hash, display_prefix, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+apiKeyColumns,
		key.ID, key.ApplicationID, key.Name, string(key.Mode), hash, key.DisplayPrefix, key.ExpiresAt,
	)
	return scanAPIKey(row, key)
}

// Authenticated is the result of resolving an API key to its owner.
type Authenticated struct {
	Application *Application
	APIKey      *APIKey
}

// AuthenticateAPIKey resolves a key hash to its application in one query.
//
// The lookup is by hash, so the plaintext key never reaches the database and a
// timing difference between "no such key" and "wrong key" cannot arise.
func (r *Repository) AuthenticateAPIKey(ctx context.Context, hash string) (*Authenticated, error) {
	row := r.q(ctx).QueryRow(ctx, `
		SELECT
			k.id, k.application_id, k.name, k.mode, k.display_prefix,
			k.last_used_at, k.expires_at, k.revoked_at, k.created_at,
			a.id, a.name, a.slug, a.description, coalesce(a.gateway_account_id, ''),
			a.disabled_at, a.metadata, a.created_at, a.updated_at
		FROM application_api_keys k
		JOIN applications a ON a.id = k.application_id
		WHERE k.key_hash = $1`, hash)

	var (
		key  APIKey
		app  Application
		mode string
		meta []byte
	)
	err := row.Scan(
		&key.ID, &key.ApplicationID, &key.Name, &mode, &key.DisplayPrefix,
		&key.LastUsedAt, &key.ExpiresAt, &key.RevokedAt, &key.CreatedAt,
		&app.ID, &app.Name, &app.Slug, &app.Description, &app.GatewayAccountID,
		&app.DisabledAt, &meta, &app.CreatedAt, &app.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("application: authenticate api key: %w", err)
	}
	key.Mode = crypto.KeyMode(mode)
	if app.Metadata, err = unmarshalMetadata(meta); err != nil {
		return nil, err
	}
	return &Authenticated{Application: &app, APIKey: &key}, nil
}

// TouchAPIKeyUsed records that a key authenticated a request.
//
// The write is throttled to once a minute per key: last_used_at is an
// operational hint, and updating it on every request would turn a read-mostly
// table into a write hotspot.
func (r *Repository) TouchAPIKeyUsed(ctx context.Context, keyID string) error {
	_, err := r.q(ctx).Exec(ctx, `
		UPDATE application_api_keys
		SET last_used_at = now()
		WHERE id = $1
		  AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute')`, keyID)
	if err != nil {
		return fmt.Errorf("application: touch api key: %w", err)
	}
	return nil
}

// ListAPIKeys returns every key for an application, newest first.
func (r *Repository) ListAPIKeys(ctx context.Context, applicationID string) ([]*APIKey, error) {
	rows, err := r.q(ctx).Query(ctx,
		`SELECT `+apiKeyColumns+` FROM application_api_keys WHERE application_id = $1 ORDER BY id DESC`,
		applicationID)
	if err != nil {
		return nil, fmt.Errorf("application: list api keys: %w", err)
	}
	defer rows.Close()

	var out []*APIKey
	for rows.Next() {
		var key APIKey
		if err := scanAPIKey(rows, &key); err != nil {
			return nil, err
		}
		out = append(out, &key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("application: list api keys: %w", err)
	}
	return out, nil
}

// RevokeAPIKey marks a key unusable. Revoking an already revoked key keeps the
// original revocation time, so the audit trail is not rewritten.
func (r *Repository) RevokeAPIKey(ctx context.Context, applicationID, keyID string) (*APIKey, error) {
	var key APIKey
	row := r.q(ctx).QueryRow(ctx, `
		UPDATE application_api_keys
		SET revoked_at = coalesce(revoked_at, now())
		WHERE id = $1 AND application_id = $2
		RETURNING `+apiKeyColumns, keyID, applicationID)
	if err := scanAPIKey(row, &key); err != nil {
		return nil, err
	}
	return &key, nil
}

// ---------------------------------------------------------------------------
// Webhook destinations
// ---------------------------------------------------------------------------

const destinationColumns = `
	id, application_id, url, description, secret_encrypted,
	event_types, enabled, created_at, updated_at`

// CreateDestination stores a destination, sealing its signing secret.
func (r *Repository) CreateDestination(ctx context.Context, dst *Destination) error {
	if dst.ID == "" {
		dst.ID = ids.New(ids.Destination)
	}
	sealed, err := r.sealer.SealString(dst.Secret.Reveal(), secretContext(dst.ID))
	if err != nil {
		return fmt.Errorf("application: seal destination secret: %w", err)
	}
	row := r.q(ctx).QueryRow(ctx, `
		INSERT INTO webhook_destinations
			(id, application_id, url, description, secret_encrypted, event_types, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+destinationColumns,
		dst.ID, dst.ApplicationID, dst.URL, dst.Description, sealed,
		nonNilStrings(dst.EventTypes), dst.Enabled,
	)
	return r.scanDestination(row, dst)
}

// GetDestination loads one destination scoped to its application.
func (r *Repository) GetDestination(ctx context.Context, applicationID, id string) (*Destination, error) {
	var dst Destination
	row := r.q(ctx).QueryRow(ctx,
		`SELECT `+destinationColumns+` FROM webhook_destinations WHERE id = $1 AND application_id = $2`,
		id, applicationID)
	if err := r.scanDestination(row, &dst); err != nil {
		return nil, err
	}
	return &dst, nil
}

// ListDestinations returns an application's destinations, oldest first so the
// delivery fan-out order is stable.
func (r *Repository) ListDestinations(ctx context.Context, applicationID string) ([]*Destination, error) {
	rows, err := r.q(ctx).Query(ctx,
		`SELECT `+destinationColumns+` FROM webhook_destinations WHERE application_id = $1 ORDER BY id`,
		applicationID)
	if err != nil {
		return nil, fmt.Errorf("application: list destinations: %w", err)
	}
	defer rows.Close()

	var out []*Destination
	for rows.Next() {
		var dst Destination
		if err := r.scanDestination(rows, &dst); err != nil {
			return nil, err
		}
		out = append(out, &dst)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("application: list destinations: %w", err)
	}
	return out, nil
}

// ListEnabledDestinations returns the destinations eligible to receive an
// event of the given type.
func (r *Repository) ListEnabledDestinations(ctx context.Context, applicationID, eventType string) ([]*Destination, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+destinationColumns+`
		FROM webhook_destinations
		WHERE application_id = $1
		  AND enabled
		  AND (cardinality(event_types) = 0 OR $2 = ANY (event_types))
		ORDER BY id`, applicationID, eventType)
	if err != nil {
		return nil, fmt.Errorf("application: list enabled destinations: %w", err)
	}
	defer rows.Close()

	var out []*Destination
	for rows.Next() {
		var dst Destination
		if err := r.scanDestination(rows, &dst); err != nil {
			return nil, err
		}
		out = append(out, &dst)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("application: list enabled destinations: %w", err)
	}
	return out, nil
}

// DestinationUpdate carries the mutable fields of a destination.
type DestinationUpdate struct {
	URL         *string
	Description *string
	EventTypes  []string
	Enabled     *bool
	// Secret replaces the signing secret when non-empty (a rotation).
	Secret crypto.Secret
}

// UpdateDestination applies a partial update.
func (r *Repository) UpdateDestination(ctx context.Context, applicationID, id string, update DestinationUpdate) (*Destination, error) {
	var sealed *string
	if update.Secret != "" {
		s, err := r.sealer.SealString(update.Secret.Reveal(), secretContext(id))
		if err != nil {
			return nil, fmt.Errorf("application: seal destination secret: %w", err)
		}
		sealed = &s
	}
	var eventTypes *[]string
	if update.EventTypes != nil {
		types := nonNilStrings(update.EventTypes)
		eventTypes = &types
	}

	var dst Destination
	row := r.q(ctx).QueryRow(ctx, `
		UPDATE webhook_destinations SET
			url              = coalesce($3, url),
			description      = coalesce($4, description),
			event_types      = coalesce($5, event_types),
			enabled          = coalesce($6, enabled),
			secret_encrypted = coalesce($7, secret_encrypted),
			updated_at       = now()
		WHERE id = $1 AND application_id = $2
		RETURNING `+destinationColumns,
		id, applicationID, update.URL, update.Description, eventTypes, update.Enabled, sealed,
	)
	if err := r.scanDestination(row, &dst); err != nil {
		return nil, err
	}
	return &dst, nil
}

// DeleteDestination removes a destination and, by cascade, its deliveries.
func (r *Repository) DeleteDestination(ctx context.Context, applicationID, id string) error {
	tag, err := r.q(ctx).Exec(ctx,
		`DELETE FROM webhook_destinations WHERE id = $1 AND application_id = $2`, id, applicationID)
	if err != nil {
		return fmt.Errorf("application: delete destination: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------------------
// Scanning helpers
// ---------------------------------------------------------------------------

// scanner is satisfied by both pgx.Row and pgx.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanApplication(row scanner, app *Application) error {
	var metadata []byte
	err := row.Scan(
		&app.ID, &app.Name, &app.Slug, &app.Description, &app.GatewayAccountID,
		&app.DisabledAt, &metadata, &app.CreatedAt, &app.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storage.ErrNotFound
		}
		return fmt.Errorf("application: scan application: %w", err)
	}
	app.Metadata, err = unmarshalMetadata(metadata)
	return err
}

func scanAPIKey(row scanner, key *APIKey) error {
	var mode string
	err := row.Scan(
		&key.ID, &key.ApplicationID, &key.Name, &mode, &key.DisplayPrefix,
		&key.LastUsedAt, &key.ExpiresAt, &key.RevokedAt, &key.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storage.ErrNotFound
		}
		return fmt.Errorf("application: scan api key: %w", err)
	}
	key.Mode = crypto.KeyMode(mode)
	return nil
}

func (r *Repository) scanDestination(row scanner, dst *Destination) error {
	var sealed string
	err := row.Scan(
		&dst.ID, &dst.ApplicationID, &dst.URL, &dst.Description, &sealed,
		&dst.EventTypes, &dst.Enabled, &dst.CreatedAt, &dst.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storage.ErrNotFound
		}
		return fmt.Errorf("application: scan destination: %w", err)
	}
	secret, err := r.sealer.OpenString(sealed, secretContext(dst.ID))
	if err != nil {
		// A secret that cannot be opened means the encryption key changed.
		// Surface it plainly rather than silently signing with an empty key.
		return fmt.Errorf("application: destination %s secret cannot be decrypted "+
			"(has PAYMUX_ENCRYPTION_KEY changed?): %w", dst.ID, err)
	}
	dst.Secret = crypto.Secret(secret)
	return nil
}

func marshalMetadata(m map[string]any) ([]byte, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("application: encode metadata: %w", err)
	}
	return b, nil
}

func marshalMetadataPtr(m map[string]any) (*[]byte, error) {
	if m == nil {
		return nil, nil
	}
	b, err := marshalMetadata(m)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func unmarshalMetadata(b []byte) (map[string]any, error) {
	if len(b) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("application: decode metadata: %w", err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// nonNilStrings keeps a nil slice from being written as SQL NULL into a
// NOT NULL array column.
func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
