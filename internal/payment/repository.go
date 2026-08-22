package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/ids"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/storage"
)

// Constraint names this package interprets.
const (
	ConstraintOrderUnique        = "payments_application_order_key"
	ConstraintGatewayOrderUnique = "payments_gateway_order_id_key"
	ConstraintIdempotencyKey     = "idempotency_keys_scope_key"
	ConstraintRefundKey          = "refunds_payment_refund_key"
)

// Repository persists payments, their line items, refunds and the gateway
// transaction records that back them.
type Repository struct {
	db *storage.DB
}

// NewRepository builds a Repository.
func NewRepository(db *storage.DB) *Repository { return &Repository{db: db} }

func (r *Repository) q(ctx context.Context) storage.Querier { return r.db.FromContext(ctx) }

const paymentColumns = `
	id, application_id, gateway_account_id, gateway,
	application_order_id, gateway_order_id, coalesce(gateway_transaction_id, ''),
	amount, currency, normalized_status, gateway_status, fraud_status,
	payment_method, payment_type, coalesce(snap_token, ''), coalesce(snap_redirect_url, ''),
	refunded_amount, metadata, gateway_options, gateway_data,
	expires_at, paid_at, canceled_at, expired_at, refunded_at, last_synced_at,
	created_at, updated_at`

// Create stores a payment together with its customer and items in one
// transaction, so a payment can never exist without the details it was
// created from.
func (r *Repository) Create(ctx context.Context, p *Payment) error {
	if p.ID == "" {
		p.ID = ids.New(ids.Payment)
	}
	if p.GatewayOrderID == "" {
		p.GatewayOrderID = ids.New(ids.Order)
	}
	metadata, err := encodeJSON(p.Metadata)
	if err != nil {
		return err
	}
	options, err := encodeJSON(p.GatewayOptions)
	if err != nil {
		return err
	}
	data, err := encodeJSON(p.GatewayData)
	if err != nil {
		return err
	}

	return r.db.InTx(ctx, func(ctx context.Context, tx storage.Querier) error {
		row := tx.QueryRow(ctx, `
			INSERT INTO payments (
				id, application_id, gateway_account_id, gateway,
				application_order_id, gateway_order_id, gateway_transaction_id,
				amount, currency, normalized_status, gateway_status, fraud_status,
				status_rank, payment_method, payment_type, snap_token, snap_redirect_url,
				metadata, gateway_options, gateway_data, expires_at
			) VALUES (
				$1, $2, $3, $4, $5, $6, nullif($7, ''), $8, $9, $10, $11, $12,
				$13, $14, $15, nullif($16, ''), nullif($17, ''), $18, $19, $20, $21
			)
			RETURNING `+paymentColumns,
			p.ID, p.ApplicationID, p.GatewayAccountID, p.Gateway,
			p.ApplicationOrderID, p.GatewayOrderID, p.GatewayTransactionID,
			p.Amount, p.Currency, string(p.NormalizedStatus), p.GatewayStatus, p.FraudStatus,
			p.NormalizedStatus.Rank(), p.PaymentMethod, p.PaymentType, p.SnapToken, p.SnapRedirectURL,
			metadata, options, data, p.ExpiresAt,
		)
		if err := scanPayment(row, p); err != nil {
			return err
		}
		if err := insertCustomer(ctx, tx, p.ID, p.Customer); err != nil {
			return err
		}
		return insertItems(ctx, tx, p.ID, p.Items)
	})
}

func insertCustomer(ctx context.Context, tx storage.Querier, paymentID string, c *Customer) error {
	if c == nil {
		return nil
	}
	billing, err := encodeJSONOrNil(c.Billing)
	if err != nil {
		return err
	}
	shipping, err := encodeJSONOrNil(c.Shipping)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO payment_customers
			(payment_id, first_name, last_name, email, phone, billing_address, shipping_address)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		paymentID, c.FirstName, c.LastName, c.Email, c.Phone, billing, shipping)
	if err != nil {
		return fmt.Errorf("payment: insert customer: %w", err)
	}
	return nil
}

func insertItems(ctx context.Context, tx storage.Querier, paymentID string, items []Item) error {
	for i, item := range items {
		_, err := tx.Exec(ctx, `
			INSERT INTO payment_items
				(id, payment_id, position, sku, name, price, quantity, category, merchant, url)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			ids.New(ids.Payment), paymentID, i, item.SKU, item.Name,
			item.Price, item.Quantity, item.Category, item.Merchant, item.URL)
		if err != nil {
			return fmt.Errorf("payment: insert item %d: %w", i, err)
		}
	}
	return nil
}

// Get loads a payment by identifier without its customer or items.
func (r *Repository) Get(ctx context.Context, id string) (*Payment, error) {
	var p Payment
	row := r.q(ctx).QueryRow(ctx, `SELECT `+paymentColumns+` FROM payments WHERE id = $1`, id)
	if err := scanPayment(row, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// GetForApplication loads a payment, refusing to return one owned by another
// application. Ownership is enforced in the query itself so no handler can
// forget the check (PRD §49).
func (r *Repository) GetForApplication(ctx context.Context, applicationID, id string) (*Payment, error) {
	var p Payment
	row := r.q(ctx).QueryRow(ctx,
		`SELECT `+paymentColumns+` FROM payments WHERE id = $1 AND application_id = $2`,
		id, applicationID)
	if err := scanPayment(row, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// GetByGatewayOrderID loads the payment PayMux created for a gateway order.
// This is how an inbound notification is attributed to its owner.
func (r *Repository) GetByGatewayOrderID(ctx context.Context, gatewayName, orderID string) (*Payment, error) {
	var p Payment
	row := r.q(ctx).QueryRow(ctx,
		`SELECT `+paymentColumns+` FROM payments WHERE gateway = $1 AND gateway_order_id = $2`,
		gatewayName, orderID)
	if err := scanPayment(row, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// GetByApplicationOrderID loads a payment by the application's own reference.
func (r *Repository) GetByApplicationOrderID(ctx context.Context, applicationID, orderID string) (*Payment, error) {
	var p Payment
	row := r.q(ctx).QueryRow(ctx,
		`SELECT `+paymentColumns+` FROM payments WHERE application_id = $1 AND application_order_id = $2`,
		applicationID, orderID)
	if err := scanPayment(row, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// LockForUpdate loads a payment and holds a row lock until the surrounding
// transaction ends.
//
// Concurrent notifications for the same payment are the normal case, not an
// edge case: the lock is what makes "read the current state, decide, write"
// safe when two notifications arrive at once (PRD §40).
func (r *Repository) LockForUpdate(ctx context.Context, id string) (*Payment, error) {
	var p Payment
	row := r.q(ctx).QueryRow(ctx,
		`SELECT `+paymentColumns+` FROM payments WHERE id = $1 FOR UPDATE`, id)
	if err := scanPayment(row, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// LoadDetails fills in a payment's customer and items.
func (r *Repository) LoadDetails(ctx context.Context, p *Payment) error {
	customer, err := r.customer(ctx, p.ID)
	if err != nil {
		return err
	}
	p.Customer = customer

	items, err := r.items(ctx, p.ID)
	if err != nil {
		return err
	}
	p.Items = items
	return nil
}

func (r *Repository) customer(ctx context.Context, paymentID string) (*Customer, error) {
	var (
		c                 Customer
		billing, shipping []byte
	)
	err := r.q(ctx).QueryRow(ctx, `
		SELECT first_name, last_name, email, phone, billing_address, shipping_address
		FROM payment_customers WHERE payment_id = $1`, paymentID,
	).Scan(&c.FirstName, &c.LastName, &c.Email, &c.Phone, &billing, &shipping)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("payment: load customer: %w", err)
	}
	if c.Billing, err = decodeAddress(billing); err != nil {
		return nil, err
	}
	if c.Shipping, err = decodeAddress(shipping); err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *Repository) items(ctx context.Context, paymentID string) ([]Item, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT id, position, sku, name, price, quantity, category, merchant, url
		FROM payment_items WHERE payment_id = $1 ORDER BY position`, paymentID)
	if err != nil {
		return nil, fmt.Errorf("payment: load items: %w", err)
	}
	defer rows.Close()

	var out []Item
	for rows.Next() {
		var item Item
		if err := rows.Scan(&item.ID, &item.Position, &item.SKU, &item.Name,
			&item.Price, &item.Quantity, &item.Category, &item.Merchant, &item.URL); err != nil {
			return nil, fmt.Errorf("payment: scan item: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("payment: load items: %w", err)
	}
	return out, nil
}

// Filter narrows a payment listing.
type Filter struct {
	ApplicationID        string
	Status               gateway.Status
	Gateway              string
	ApplicationOrderID   string
	GatewayOrderID       string
	GatewayTransactionID string
	CreatedFrom          *time.Time
	CreatedTo            *time.Time
}

// List returns a page of payments matching the filter, newest first.
func (r *Repository) List(ctx context.Context, filter Filter, page storage.Page) (storage.List[*Payment], error) {
	page = page.Normalize()

	// Conditions are built with positional parameters rather than string
	// interpolation; no caller-supplied value ever reaches the SQL text.
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
	if filter.Status != "" {
		add("normalized_status = $%d", string(filter.Status))
	}
	if filter.Gateway != "" {
		add("gateway = $%d", filter.Gateway)
	}
	if filter.ApplicationOrderID != "" {
		add("application_order_id = $%d", filter.ApplicationOrderID)
	}
	if filter.GatewayOrderID != "" {
		add("gateway_order_id = $%d", filter.GatewayOrderID)
	}
	if filter.GatewayTransactionID != "" {
		add("gateway_transaction_id = $%d", filter.GatewayTransactionID)
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

	query := `SELECT ` + paymentColumns + ` FROM payments`
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	args = append(args, page.FetchLimit())
	query += fmt.Sprintf(" ORDER BY id DESC LIMIT $%d", len(args))

	rows, err := r.q(ctx).Query(ctx, query, args...)
	if err != nil {
		return storage.List[*Payment]{}, fmt.Errorf("payment: list: %w", err)
	}
	defer rows.Close()

	var out []*Payment
	for rows.Next() {
		var p Payment
		if err := scanPayment(rows, &p); err != nil {
			return storage.List[*Payment]{}, err
		}
		out = append(out, &p)
	}
	if err := rows.Err(); err != nil {
		return storage.List[*Payment]{}, fmt.Errorf("payment: list: %w", err)
	}
	return storage.NewList(out, page), nil
}

// StateUpdate carries a payment state change driven by the gateway.
type StateUpdate struct {
	NormalizedStatus     gateway.Status
	GatewayStatus        string
	FraudStatus          string
	GatewayTransactionID string
	PaymentType          string
	PaymentMethod        string
	RefundedAmount       *int64
	GatewayData          map[string]any
	OccurredAt           *time.Time
	MarkSynced           bool
}

// ApplyState writes a state transition, refusing to move a payment backwards.
//
// The guard lives in the UPDATE's WHERE clause rather than in Go: two workers
// can process notifications for the same payment concurrently, and only the
// database can settle which one wins. A caller that gets ErrStaleTransition
// has been overtaken by a more advanced state, which is not an error worth
// retrying (PRD §40).
func (r *Repository) ApplyState(ctx context.Context, paymentID string, update StateUpdate) (*Payment, error) {
	if !update.NormalizedStatus.Valid() {
		return nil, fmt.Errorf("payment: cannot apply unknown status %q", update.NormalizedStatus)
	}
	data, err := encodeJSONOrNil(update.GatewayData)
	if err != nil {
		return nil, err
	}

	occurred := time.Now()
	if update.OccurredAt != nil {
		occurred = *update.OccurredAt
	}
	status := string(update.NormalizedStatus)
	rank := update.NormalizedStatus.Rank()

	var p Payment
	row := r.q(ctx).QueryRow(ctx, `
		UPDATE payments SET
			normalized_status      = $2,
			status_rank            = $3,
			gateway_status         = $4,
			fraud_status           = coalesce(nullif($5, ''), fraud_status),
			gateway_transaction_id = coalesce(nullif($6, ''), gateway_transaction_id),
			payment_type           = coalesce(nullif($7, ''), payment_type),
			payment_method         = coalesce(nullif($8, ''), payment_method),
			refunded_amount        = coalesce($9, refunded_amount),
			gateway_data           = coalesce($10, gateway_data),
			paid_at                = CASE WHEN $2 = 'PAID'      AND paid_at     IS NULL THEN $11 ELSE paid_at END,
			canceled_at            = CASE WHEN $2 = 'CANCELED'  AND canceled_at IS NULL THEN $11 ELSE canceled_at END,
			expired_at             = CASE WHEN $2 = 'EXPIRED'   AND expired_at  IS NULL THEN $11 ELSE expired_at END,
			refunded_at            = CASE WHEN $2 IN ('REFUNDED', 'PARTIALLY_REFUNDED')
			                                   AND refunded_at IS NULL THEN $11 ELSE refunded_at END,
			last_synced_at         = CASE WHEN $12 THEN now() ELSE last_synced_at END,
			updated_at             = now()
		WHERE id = $1
		  AND normalized_status = ANY($13)
		RETURNING `+paymentColumns,
		paymentID, status, rank, update.GatewayStatus, update.FraudStatus,
		update.GatewayTransactionID, update.PaymentType, update.PaymentMethod,
		update.RefundedAmount, data, occurred, update.MarkSynced,
		gateway.PredecessorsOf(update.NormalizedStatus),
	)
	if err := scanPayment(row, &p); err != nil {
		if storage.IsNotFound(err) {
			return nil, ErrStaleTransition
		}
		return nil, err
	}
	return &p, nil
}

// TouchSynced records that PayMux reconciled a payment with the gateway even
// when nothing about its state changed.
func (r *Repository) TouchSynced(ctx context.Context, paymentID string) error {
	_, err := r.q(ctx).Exec(ctx,
		`UPDATE payments SET last_synced_at = now() WHERE id = $1`, paymentID)
	if err != nil {
		return fmt.Errorf("payment: touch synced: %w", err)
	}
	return nil
}

// SetCheckoutSession stores the hosted-checkout details returned by a gateway.
func (r *Repository) SetCheckoutSession(ctx context.Context, paymentID, token, redirectURL string, expiresAt *time.Time) error {
	_, err := r.q(ctx).Exec(ctx, `
		UPDATE payments
		SET snap_token = nullif($2, ''), snap_redirect_url = nullif($3, ''),
		    expires_at = coalesce($4, expires_at), updated_at = now()
		WHERE id = $1`, paymentID, token, redirectURL, expiresAt)
	if err != nil {
		return fmt.Errorf("payment: store checkout session: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Gateway transactions
// ---------------------------------------------------------------------------

// UpsertGatewayTransaction records the gateway's own view of a transaction.
func (r *Repository) UpsertGatewayTransaction(ctx context.Context, paymentID string, txn *gateway.Transaction) error {
	raw, err := encodeJSON(txn.Raw)
	if err != nil {
		return err
	}
	if txn.TransactionID == "" {
		// Without a gateway transaction id there is nothing stable to key on.
		return nil
	}
	_, err = r.q(ctx).Exec(ctx, `
		INSERT INTO gateway_transactions (
			id, payment_id, gateway, gateway_transaction_id, gateway_order_id,
			gateway_status, fraud_status, payment_type, gross_amount, currency,
			transaction_time, settlement_time, raw_payload
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (gateway, gateway_transaction_id) DO UPDATE SET
			gateway_status  = EXCLUDED.gateway_status,
			fraud_status    = EXCLUDED.fraud_status,
			payment_type    = EXCLUDED.payment_type,
			settlement_time = coalesce(EXCLUDED.settlement_time, gateway_transactions.settlement_time),
			raw_payload     = EXCLUDED.raw_payload,
			updated_at      = now()`,
		ids.New(ids.Transaction), paymentID, "midtrans", txn.TransactionID, txn.OrderID,
		txn.Status, txn.FraudStatus, txn.PaymentType, txn.GrossAmount, txn.Currency,
		txn.TransactionTime, txn.SettlementTime, raw,
	)
	if err != nil {
		return fmt.Errorf("payment: upsert gateway transaction: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Scanning and encoding helpers
// ---------------------------------------------------------------------------

type scanner interface {
	Scan(dest ...any) error
}

func scanPayment(row scanner, p *Payment) error {
	var (
		status                  string
		metadata, options, data []byte
	)
	err := row.Scan(
		&p.ID, &p.ApplicationID, &p.GatewayAccountID, &p.Gateway,
		&p.ApplicationOrderID, &p.GatewayOrderID, &p.GatewayTransactionID,
		&p.Amount, &p.Currency, &status, &p.GatewayStatus, &p.FraudStatus,
		&p.PaymentMethod, &p.PaymentType, &p.SnapToken, &p.SnapRedirectURL,
		&p.RefundedAmount, &metadata, &options, &data,
		&p.ExpiresAt, &p.PaidAt, &p.CanceledAt, &p.ExpiredAt, &p.RefundedAt, &p.LastSyncedAt,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storage.ErrNotFound
		}
		return fmt.Errorf("payment: scan payment: %w", err)
	}
	p.NormalizedStatus = gateway.Status(status)
	if p.Metadata, err = decodeJSON(metadata); err != nil {
		return err
	}
	if p.GatewayOptions, err = decodeJSON(options); err != nil {
		return err
	}
	if p.GatewayData, err = decodeJSON(data); err != nil {
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
		return nil, fmt.Errorf("payment: encode json: %w", err)
	}
	return b, nil
}

func encodeJSONOrNil(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	if m, ok := v.(map[string]any); ok && m == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("payment: encode json: %w", err)
	}
	return b, nil
}

func decodeJSON(b []byte) (map[string]any, error) {
	if len(b) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("payment: decode json: %w", err)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func decodeAddress(b []byte) (*Address, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var addr Address
	if err := json.Unmarshal(b, &addr); err != nil {
		return nil, fmt.Errorf("payment: decode address: %w", err)
	}
	return &addr, nil
}

// Stats summarises payment activity for the dashboard overview (PRD §52).
type Stats struct {
	Created    int64
	Paid       int64
	Pending    int64
	Failed     int64
	Currencies []CurrencyTotal
}

// CurrencyTotal is the settled value in one currency.
//
// Totals are reported per currency rather than summed: adding rupiah to
// dollars would produce a number that means nothing.
type CurrencyTotal struct {
	Currency  string
	PaidTotal int64
	Count     int64
}

// Stats counts payments created since a cutoff, grouped by outcome.
func (r *Repository) Stats(ctx context.Context, since time.Time) (Stats, error) {
	var s Stats
	err := r.q(ctx).QueryRow(ctx, `
		SELECT
			count(*),
			count(*) FILTER (WHERE normalized_status IN ('PAID', 'REFUNDED', 'PARTIALLY_REFUNDED')),
			count(*) FILTER (WHERE normalized_status IN ('PENDING', 'AUTHORIZED')),
			count(*) FILTER (WHERE normalized_status IN ('FAILED', 'CANCELED', 'EXPIRED'))
		FROM payments
		WHERE created_at >= $1`, since,
	).Scan(&s.Created, &s.Paid, &s.Pending, &s.Failed)
	if err != nil {
		return Stats{}, fmt.Errorf("payment: stats: %w", err)
	}

	rows, err := r.q(ctx).Query(ctx, `
		SELECT currency, coalesce(sum(amount - refunded_amount), 0), count(*)
		FROM payments
		WHERE created_at >= $1
		  AND normalized_status IN ('PAID', 'PARTIALLY_REFUNDED')
		GROUP BY currency
		ORDER BY currency`, since)
	if err != nil {
		return Stats{}, fmt.Errorf("payment: currency stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var total CurrencyTotal
		if err := rows.Scan(&total.Currency, &total.PaidTotal, &total.Count); err != nil {
			return Stats{}, fmt.Errorf("payment: scan currency stats: %w", err)
		}
		s.Currencies = append(s.Currencies, total)
	}
	if err := rows.Err(); err != nil {
		return Stats{}, fmt.Errorf("payment: currency stats: %w", err)
	}
	return s, nil
}
