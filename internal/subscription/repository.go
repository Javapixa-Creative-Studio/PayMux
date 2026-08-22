package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/ids"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/storage"
)

// Repository persists subscriptions.
type Repository struct {
	db *storage.DB
}

// NewRepository builds a Repository.
func NewRepository(db *storage.DB) *Repository { return &Repository{db: db} }

func (r *Repository) q(ctx context.Context) storage.Querier { return r.db.FromContext(ctx) }

const columns = `
	id, application_id, gateway_account_id, gateway,
	coalesce(gateway_subscription_id, ''), name, amount, currency,
	status, gateway_status, interval_unit, interval_count, max_interval,
	start_time, payment_type, payment_token, metadata, gateway_data,
	created_at, updated_at`

// Create stores a subscription.
func (r *Repository) Create(ctx context.Context, s *Subscription) error {
	if s.ID == "" {
		s.ID = ids.New(ids.Subscription)
	}
	metadata, err := encodeJSON(s.Metadata)
	if err != nil {
		return err
	}
	data, err := encodeJSON(s.GatewayData)
	if err != nil {
		return err
	}
	row := r.q(ctx).QueryRow(ctx, `
		INSERT INTO subscriptions (
			id, application_id, gateway_account_id, gateway, gateway_subscription_id,
			name, amount, currency, status, gateway_status, interval_unit,
			interval_count, max_interval, start_time, payment_type, payment_token,
			metadata, gateway_data
		) VALUES ($1, $2, $3, $4, nullif($5, ''), $6, $7, $8, $9, $10, $11,
		          $12, $13, $14, $15, $16, $17, $18)
		RETURNING `+columns,
		s.ID, s.ApplicationID, s.GatewayAccountID, s.Gateway, s.GatewaySubscriptionID,
		s.Name, s.Amount, s.Currency, string(s.Status), s.GatewayStatus, s.IntervalUnit,
		s.IntervalCount, s.MaxInterval, s.StartTime, s.PaymentType, s.PaymentToken,
		metadata, data,
	)
	return scan(row, s)
}

// Get loads a subscription by identifier.
func (r *Repository) Get(ctx context.Context, id string) (*Subscription, error) {
	var s Subscription
	row := r.q(ctx).QueryRow(ctx, `SELECT `+columns+` FROM subscriptions WHERE id = $1`, id)
	if err := scan(row, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// GetForApplication loads a subscription scoped to its owner, so one
// application can never read another's (PRD §49).
func (r *Repository) GetForApplication(ctx context.Context, applicationID, id string) (*Subscription, error) {
	var s Subscription
	row := r.q(ctx).QueryRow(ctx,
		`SELECT `+columns+` FROM subscriptions WHERE id = $1 AND application_id = $2`,
		id, applicationID)
	if err := scan(row, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// List returns a page of an application's subscriptions, newest first.
func (r *Repository) List(ctx context.Context, applicationID string, page storage.Page) (storage.List[*Subscription], error) {
	page = page.Normalize()
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+columns+`
		FROM subscriptions
		WHERE ($1 = '' OR application_id = $1)
		  AND ($2 = '' OR id < $2)
		ORDER BY id DESC
		LIMIT $3`,
		applicationID, page.StartingAfter, page.FetchLimit())
	if err != nil {
		return storage.List[*Subscription]{}, fmt.Errorf("subscription: list: %w", err)
	}
	defer rows.Close()

	var out []*Subscription
	for rows.Next() {
		var s Subscription
		if err := scan(rows, &s); err != nil {
			return storage.List[*Subscription]{}, err
		}
		out = append(out, &s)
	}
	if err := rows.Err(); err != nil {
		return storage.List[*Subscription]{}, fmt.Errorf("subscription: list: %w", err)
	}
	return storage.NewList(out, page), nil
}

// Update writes the gateway's current view back to PayMux's record.
func (r *Repository) Update(ctx context.Context, s *Subscription) error {
	metadata, err := encodeJSON(s.Metadata)
	if err != nil {
		return err
	}
	data, err := encodeJSON(s.GatewayData)
	if err != nil {
		return err
	}
	row := r.q(ctx).QueryRow(ctx, `
		UPDATE subscriptions SET
			gateway_subscription_id = coalesce(nullif($2, ''), gateway_subscription_id),
			name           = $3,
			amount         = $4,
			status         = $5,
			gateway_status = $6,
			interval_unit  = $7,
			interval_count = $8,
			payment_type   = $9,
			payment_token  = coalesce(nullif($10, ''), payment_token),
			metadata       = $11,
			gateway_data   = $12,
			updated_at     = now()
		WHERE id = $1
		RETURNING `+columns,
		s.ID, s.GatewaySubscriptionID, s.Name, s.Amount, string(s.Status),
		s.GatewayStatus, s.IntervalUnit, s.IntervalCount, s.PaymentType,
		s.PaymentToken, metadata, data,
	)
	return scan(row, s)
}

// SetStatus records a lifecycle change.
func (r *Repository) SetStatus(ctx context.Context, id string, status gateway.SubscriptionStatus) (*Subscription, error) {
	var s Subscription
	row := r.q(ctx).QueryRow(ctx, `
		UPDATE subscriptions SET status = $2, updated_at = now()
		WHERE id = $1
		RETURNING `+columns, id, string(status))
	if err := scan(row, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scan(row scanner, s *Subscription) error {
	var (
		status         string
		metadata, data []byte
	)
	err := row.Scan(
		&s.ID, &s.ApplicationID, &s.GatewayAccountID, &s.Gateway, &s.GatewaySubscriptionID,
		&s.Name, &s.Amount, &s.Currency, &status, &s.GatewayStatus, &s.IntervalUnit,
		&s.IntervalCount, &s.MaxInterval, &s.StartTime, &s.PaymentType, &s.PaymentToken,
		&metadata, &data, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storage.ErrNotFound
		}
		return fmt.Errorf("subscription: scan: %w", err)
	}
	s.Status = gateway.SubscriptionStatus(status)
	if s.Metadata, err = decodeJSON(metadata); err != nil {
		return err
	}
	if s.GatewayData, err = decodeJSON(data); err != nil {
		return err
	}
	return nil
}

func encodeJSON(v map[string]any) ([]byte, error) {
	if v == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("subscription: encode json: %w", err)
	}
	return b, nil
}

func decodeJSON(b []byte) (map[string]any, error) {
	if len(b) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("subscription: decode json: %w", err)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}
