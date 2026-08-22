package event

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/ids"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/storage"
)

// ConstraintDedupeKey is the unique index that keeps the same occurrence from
// being published twice.
const ConstraintDedupeKey = "events_dedupe_key"

// ErrDuplicate reports an event PayMux has already published for this payment
// and state. It is an expected outcome of a redelivered notification, not a
// failure (PRD §39).
var ErrDuplicate = errors.New("event: this event has already been published")

// Repository persists normalized events.
type Repository struct {
	db *storage.DB
}

// NewRepository builds a Repository.
func NewRepository(db *storage.DB) *Repository { return &Repository{db: db} }

func (r *Repository) q(ctx context.Context) storage.Querier { return r.db.FromContext(ctx) }

const eventColumns = `
	id, sequence, application_id, type, gateway,
	coalesce(payment_id, ''), coalesce(refund_id, ''), coalesce(subscription_id, ''),
	coalesce(gateway_event_id, ''), coalesce(dedupe_key, ''), payload, created_at`

// Create stores an event.
//
// The unique index on the dedupe key turns "publish this event" into an
// idempotent operation: a duplicate notification for a state PayMux already
// announced is rejected by the database rather than fanning out a second time.
func (r *Repository) Create(ctx context.Context, e *Event) error {
	if e.ID == "" {
		e.ID = ids.New(ids.Event)
	}
	e.Payload.ID = e.ID
	e.Payload.Type = e.Type
	if e.Payload.CreatedAt.IsZero() {
		e.Payload.CreatedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(e.Payload)
	if err != nil {
		return fmt.Errorf("event: encode payload: %w", err)
	}

	row := r.q(ctx).QueryRow(ctx, `
		INSERT INTO events
			(id, application_id, type, gateway, payment_id, refund_id,
			 subscription_id, gateway_event_id, dedupe_key, payload)
		VALUES ($1, $2, $3, $4, nullif($5, ''), nullif($6, ''), nullif($7, ''),
		        nullif($8, ''), nullif($9, ''), $10)
		RETURNING `+eventColumns,
		e.ID, e.ApplicationID, string(e.Type), e.Gateway, e.PaymentID,
		e.RefundID, e.SubscriptionID, e.GatewayEventID, e.DedupeKey, payload,
	)
	if err := scanEvent(row, e); err != nil {
		if storage.IsUniqueViolation(err, ConstraintDedupeKey) {
			return ErrDuplicate
		}
		return err
	}
	return nil
}

// Get loads one event.
func (r *Repository) Get(ctx context.Context, id string) (*Event, error) {
	var e Event
	row := r.q(ctx).QueryRow(ctx, `SELECT `+eventColumns+` FROM events WHERE id = $1`, id)
	if err := scanEvent(row, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// Filter narrows an event listing.
type Filter struct {
	ApplicationID string
	PaymentID     string
	Type          Type
	CreatedFrom   *time.Time
	CreatedTo     *time.Time
}

// List returns a page of events, newest first.
func (r *Repository) List(ctx context.Context, filter Filter, page storage.Page) (storage.List[*Event], error) {
	page = page.Normalize()

	var (
		conditions []string
		args       []any
	)
	add := func(condition string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(condition, len(args)))
	}
	if filter.ApplicationID != "" {
		add("application_id = $%d", filter.ApplicationID)
	}
	if filter.PaymentID != "" {
		add("payment_id = $%d", filter.PaymentID)
	}
	if filter.Type != "" {
		add("type = $%d", string(filter.Type))
	}
	if filter.CreatedFrom != nil {
		add("created_at >= $%d", *filter.CreatedFrom)
	}
	if filter.CreatedTo != nil {
		add("created_at <= $%d", *filter.CreatedTo)
	}
	if page.StartingAfter != "" {
		add("id < $%d", page.StartingAfter)
	}

	query := `SELECT ` + eventColumns + ` FROM events`
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	args = append(args, page.FetchLimit())
	query += fmt.Sprintf(" ORDER BY id DESC LIMIT $%d", len(args))

	rows, err := r.q(ctx).Query(ctx, query, args...)
	if err != nil {
		return storage.List[*Event]{}, fmt.Errorf("event: list: %w", err)
	}
	defer rows.Close()

	var out []*Event
	for rows.Next() {
		var e Event
		if err := scanEvent(rows, &e); err != nil {
			return storage.List[*Event]{}, err
		}
		out = append(out, &e)
	}
	if err := rows.Err(); err != nil {
		return storage.List[*Event]{}, fmt.Errorf("event: list: %w", err)
	}
	return storage.NewList(out, page), nil
}

// ListForPayment returns every event published for a payment, oldest first,
// which is the order an operator reads a payment's history in.
func (r *Repository) ListForPayment(ctx context.Context, paymentID string) ([]*Event, error) {
	rows, err := r.q(ctx).Query(ctx,
		`SELECT `+eventColumns+` FROM events WHERE payment_id = $1 ORDER BY sequence`, paymentID)
	if err != nil {
		return nil, fmt.Errorf("event: list for payment: %w", err)
	}
	defer rows.Close()

	var out []*Event
	for rows.Next() {
		var e Event
		if err := scanEvent(rows, &e); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("event: list for payment: %w", err)
	}
	return out, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanEvent(row scanner, e *Event) error {
	var (
		eventType string
		payload   []byte
	)
	err := row.Scan(
		&e.ID, &e.Sequence, &e.ApplicationID, &eventType, &e.Gateway,
		&e.PaymentID, &e.RefundID, &e.SubscriptionID, &e.GatewayEventID,
		&e.DedupeKey, &payload, &e.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storage.ErrNotFound
		}
		return fmt.Errorf("event: scan event: %w", err)
	}
	e.Type = Type(eventType)
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &e.Payload); err != nil {
			return fmt.Errorf("event: decode payload: %w", err)
		}
	}
	return nil
}
