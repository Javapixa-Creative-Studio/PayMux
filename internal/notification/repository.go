// Package notification receives gateway callbacks, verifies them, attributes
// them to the owning application and turns them into PayMux events.
package notification

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

// Routing describes what PayMux was able to do with an inbound notification.
type Routing string

const (
	// RoutingRouted means the notification was attributed and applied.
	RoutingRouted Routing = "routed"
	// RoutingDuplicate means PayMux had already processed this exact state.
	RoutingDuplicate Routing = "duplicate"
	// RoutingUnrouted means the notification is valid but belongs to no known
	// payment. It is kept for an operator to inspect (PRD §91 rule 15).
	RoutingUnrouted Routing = "unrouted"
	// RoutingRejected means verification failed.
	RoutingRejected Routing = "rejected"
	// RoutingIgnored means the notification was understood but changed
	// nothing: a stale state, or a status PayMux does not map.
	RoutingIgnored Routing = "ignored"
)

// ConstraintDedupe is the unique index that makes duplicate notifications
// harmless.
const ConstraintDedupe = "gateway_events_dedupe_key"

// ErrDuplicate reports a notification PayMux has already recorded.
var ErrDuplicate = errors.New("notification: this notification has already been recorded")

// GatewayEvent is one inbound notification exactly as it was received.
type GatewayEvent struct {
	ID                   string
	Gateway              string
	GatewayAccountID     string
	PaymentID            string
	ApplicationID        string
	GatewayOrderID       string
	GatewayTransactionID string
	GatewayStatus        string
	FraudStatus          string
	DedupeKey            string
	SignatureVerified    bool
	Routing              Routing
	RoutingError         string
	Payload              map[string]any
	ReceivedAt           time.Time
	ProcessedAt          *time.Time
}

// Repository persists inbound notifications.
type Repository struct {
	db *storage.DB
}

// NewRepository builds a Repository.
func NewRepository(db *storage.DB) *Repository { return &Repository{db: db} }

func (r *Repository) q(ctx context.Context) storage.Querier { return r.db.FromContext(ctx) }

const gatewayEventColumns = `
	id, gateway, coalesce(gateway_account_id, ''), coalesce(payment_id, ''),
	coalesce(application_id, ''), gateway_order_id, gateway_transaction_id,
	gateway_status, fraud_status, dedupe_key, signature_verified,
	routing_status, routing_error, payload, received_at, processed_at`

// Record stores an inbound notification.
//
// The unique index on the dedupe key is what makes redelivery harmless: the
// second copy of a notification is refused by the database, so PayMux cannot
// process the same gateway state twice however many times it arrives
// (PRD §39).
func (r *Repository) Record(ctx context.Context, e *GatewayEvent) error {
	if e.ID == "" {
		e.ID = ids.New(ids.GatewayEvent)
	}
	payload, err := json.Marshal(e.Payload)
	if err != nil {
		return fmt.Errorf("notification: encode payload: %w", err)
	}

	row := r.q(ctx).QueryRow(ctx, `
		INSERT INTO gateway_events (
			id, gateway, gateway_account_id, payment_id, application_id,
			gateway_order_id, gateway_transaction_id, gateway_status, fraud_status,
			dedupe_key, signature_verified, routing_status, routing_error, payload
		) VALUES ($1, $2, nullif($3, ''), nullif($4, ''), nullif($5, ''),
		          $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING `+gatewayEventColumns,
		e.ID, e.Gateway, e.GatewayAccountID, e.PaymentID, e.ApplicationID,
		e.GatewayOrderID, e.GatewayTransactionID, e.GatewayStatus, e.FraudStatus,
		e.DedupeKey, e.SignatureVerified, string(e.Routing), e.RoutingError, payload,
	)
	if err := scanGatewayEvent(row, e); err != nil {
		if storage.IsUniqueViolation(err, ConstraintDedupe) {
			return ErrDuplicate
		}
		return err
	}
	return nil
}

// MarkProcessed records the outcome of processing a notification.
func (r *Repository) MarkProcessed(ctx context.Context, id string, routing Routing, routingError string, paymentID, applicationID string) error {
	_, err := r.q(ctx).Exec(ctx, `
		UPDATE gateway_events
		SET routing_status = $2, routing_error = $3,
		    payment_id = coalesce(nullif($4, ''), payment_id),
		    application_id = coalesce(nullif($5, ''), application_id),
		    processed_at = now()
		WHERE id = $1`,
		id, string(routing), truncate(routingError, 1000), paymentID, applicationID)
	if err != nil {
		return fmt.Errorf("notification: mark processed: %w", err)
	}
	return nil
}

// Get loads one recorded notification.
func (r *Repository) Get(ctx context.Context, id string) (*GatewayEvent, error) {
	var e GatewayEvent
	row := r.q(ctx).QueryRow(ctx, `SELECT `+gatewayEventColumns+` FROM gateway_events WHERE id = $1`, id)
	if err := scanGatewayEvent(row, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// Filter narrows a notification listing.
type Filter struct {
	PaymentID     string
	ApplicationID string
	Routing       Routing
	Gateway       string
}

// List returns a page of notifications, newest first.
func (r *Repository) List(ctx context.Context, filter Filter, page storage.Page) (storage.List[*GatewayEvent], error) {
	page = page.Normalize()

	var (
		conditions []string
		args       []any
	)
	add := func(condition string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(condition, len(args)))
	}
	if filter.PaymentID != "" {
		add("payment_id = $%d", filter.PaymentID)
	}
	if filter.ApplicationID != "" {
		add("application_id = $%d", filter.ApplicationID)
	}
	if filter.Routing != "" {
		add("routing_status = $%d", string(filter.Routing))
	}
	if filter.Gateway != "" {
		add("gateway = $%d", filter.Gateway)
	}
	if page.StartingAfter != "" {
		add("id < $%d", page.StartingAfter)
	}

	query := `SELECT ` + gatewayEventColumns + ` FROM gateway_events`
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	args = append(args, page.FetchLimit())
	query += fmt.Sprintf(" ORDER BY id DESC LIMIT $%d", len(args))

	rows, err := r.q(ctx).Query(ctx, query, args...)
	if err != nil {
		return storage.List[*GatewayEvent]{}, fmt.Errorf("notification: list: %w", err)
	}
	defer rows.Close()

	var out []*GatewayEvent
	for rows.Next() {
		var e GatewayEvent
		if err := scanGatewayEvent(rows, &e); err != nil {
			return storage.List[*GatewayEvent]{}, err
		}
		out = append(out, &e)
	}
	if err := rows.Err(); err != nil {
		return storage.List[*GatewayEvent]{}, fmt.Errorf("notification: list: %w", err)
	}
	return storage.NewList(out, page), nil
}

// CountUnrouted reports how many notifications could not be attributed, which
// the dashboard surfaces as an operational warning (PRD §52).
func (r *Repository) CountUnrouted(ctx context.Context, since time.Time) (int64, error) {
	var n int64
	err := r.q(ctx).QueryRow(ctx, `
		SELECT count(*) FROM gateway_events
		WHERE routing_status IN ('unrouted', 'rejected') AND received_at >= $1`, since).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("notification: count unrouted: %w", err)
	}
	return n, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanGatewayEvent(row scanner, e *GatewayEvent) error {
	var (
		routing string
		payload []byte
	)
	err := row.Scan(
		&e.ID, &e.Gateway, &e.GatewayAccountID, &e.PaymentID, &e.ApplicationID,
		&e.GatewayOrderID, &e.GatewayTransactionID, &e.GatewayStatus, &e.FraudStatus,
		&e.DedupeKey, &e.SignatureVerified, &routing, &e.RoutingError,
		&payload, &e.ReceivedAt, &e.ProcessedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storage.ErrNotFound
		}
		return fmt.Errorf("notification: scan gateway event: %w", err)
	}
	e.Routing = Routing(routing)
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &e.Payload); err != nil {
			return fmt.Errorf("notification: decode payload: %w", err)
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
