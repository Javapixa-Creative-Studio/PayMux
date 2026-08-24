package payout

import (
	"context"
	"encoding/json"
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
	ConstraintPayoutReference = "payouts_application_reference_key"
	ConstraintIdempotencyKey  = "payouts_idempotency_key"
	ConstraintBeneficiary     = "beneficiaries_alias_key"
)

// Repository persists payouts, beneficiaries and the transition trail.
type Repository struct {
	db *storage.DB
}

// NewRepository builds a Repository.
func NewRepository(db *storage.DB) *Repository { return &Repository{db: db} }

func (r *Repository) q(ctx context.Context) storage.Querier { return r.db.FromContext(ctx) }

const payoutColumns = `
	id, application_id, gateway_account_id, gateway, application_payout_id,
	idempotency_key, reference_no, beneficiary_id,
	beneficiary_name, beneficiary_account, beneficiary_bank, beneficiary_email,
	amount, currency, notes,
	normalized_status, status_rank, gateway_status, failure_code, failure_reason,
	requested_by, approved_by, approved_at, rejected_by, rejected_at, reject_reason,
	submitted_at, completed_at, failed_at, last_synced_at, idempotency_expires_at,
	gateway_data, metadata, created_at, updated_at`

func scanPayout(row pgx.Row, p *Payout) error {
	err := row.Scan(
		&p.ID, &p.ApplicationID, &p.GatewayAccountID, &p.Gateway, &p.ApplicationPayoutID,
		&p.IdempotencyKey, &p.ReferenceNo, &p.BeneficiaryID,
		&p.BeneficiaryName, &p.BeneficiaryAccount, &p.BeneficiaryBank, &p.BeneficiaryEmail,
		&p.Amount, &p.Currency, &p.Notes,
		&p.Status, &p.StatusRank, &p.GatewayStatus, &p.FailureCode, &p.FailureReason,
		&p.RequestedBy, &p.ApprovedBy, &p.ApprovedAt, &p.RejectedBy, &p.RejectedAt, &p.RejectReason,
		&p.SubmittedAt, &p.CompletedAt, &p.FailedAt, &p.LastSyncedAt, &p.IdempotencyExpiresAt,
		&p.GatewayData, &p.Metadata, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return err
	}
	p.Object = "payout"
	return nil
}

// ---------------------------------------------------------------------------
// Payouts
// ---------------------------------------------------------------------------

// Create stores a payout.
//
// The idempotency key is assigned here and never regenerated afterwards: it is
// derived from the payout's own identifier, so a retry that finds this row
// reuses the key rather than minting one the gateway has not seen.
func (r *Repository) Create(ctx context.Context, p *Payout) error {
	if p.ID == "" {
		p.ID = ids.New(ids.Payout)
	}
	if p.IdempotencyKey == "" {
		p.IdempotencyKey = p.ID
	}
	metadata, err := encodeJSON(p.Metadata)
	if err != nil {
		return err
	}

	row := r.q(ctx).QueryRow(ctx, `
		INSERT INTO payouts (
			id, application_id, gateway_account_id, gateway, application_payout_id,
			idempotency_key, beneficiary_id,
			beneficiary_name, beneficiary_account, beneficiary_bank, beneficiary_email,
			amount, currency, notes,
			normalized_status, status_rank, requested_by, metadata
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		RETURNING `+payoutColumns,
		p.ID, p.ApplicationID, p.GatewayAccountID, p.Gateway, p.ApplicationPayoutID,
		p.IdempotencyKey, p.BeneficiaryID,
		p.BeneficiaryName, p.BeneficiaryAccount, p.BeneficiaryBank, p.BeneficiaryEmail,
		p.Amount, p.Currency, p.Notes,
		string(p.Status), p.Status.Rank(), p.RequestedBy, metadata,
	)
	if err := scanPayout(row, p); err != nil {
		if storage.IsUniqueViolation(err, ConstraintPayoutReference) {
			return ErrDuplicatePayoutID
		}
		return fmt.Errorf("payout: create: %w", err)
	}
	return nil
}

// Get reads one payout by id.
func (r *Repository) Get(ctx context.Context, id string) (*Payout, error) {
	var p Payout
	row := r.q(ctx).QueryRow(ctx, `SELECT `+payoutColumns+` FROM payouts WHERE id = $1`, id)
	if err := scanPayout(row, &p); err != nil {
		if storage.IsNotFound(err) {
			return nil, ErrPayoutNotFound
		}
		return nil, fmt.Errorf("payout: get: %w", err)
	}
	return &p, nil
}

// GetForApplication reads a payout an application owns, so one tenant cannot
// read another's transfers by guessing an identifier.
func (r *Repository) GetForApplication(ctx context.Context, applicationID, id string) (*Payout, error) {
	var p Payout
	row := r.q(ctx).QueryRow(ctx,
		`SELECT `+payoutColumns+` FROM payouts WHERE id = $1 AND application_id = $2`, id, applicationID)
	if err := scanPayout(row, &p); err != nil {
		if storage.IsNotFound(err) {
			return nil, ErrPayoutNotFound
		}
		return nil, fmt.Errorf("payout: get for application: %w", err)
	}
	return &p, nil
}

// GetByApplicationReference finds the payout an application already created
// under a reference. It is what turns a retried request into the original.
func (r *Repository) GetByApplicationReference(ctx context.Context, applicationID, reference string) (*Payout, error) {
	var p Payout
	row := r.q(ctx).QueryRow(ctx,
		`SELECT `+payoutColumns+` FROM payouts WHERE application_id = $1 AND application_payout_id = $2`,
		applicationID, reference)
	if err := scanPayout(row, &p); err != nil {
		if storage.IsNotFound(err) {
			return nil, ErrPayoutNotFound
		}
		return nil, fmt.Errorf("payout: get by reference: %w", err)
	}
	return &p, nil
}

// List returns a page of payouts matching the filter.
func (r *Repository) List(ctx context.Context, filter Filter, page storage.Page) (storage.List[*Payout], error) {
	var (
		where []string
		args  []any
	)
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if filter.ApplicationID != "" {
		add("application_id = $%d", filter.ApplicationID)
	}
	if filter.Status != "" {
		add("normalized_status = $%d", filter.Status)
	}
	if filter.BeneficiaryID != "" {
		add("beneficiary_id = $%d", filter.BeneficiaryID)
	}
	if filter.Search != "" {
		add("(application_payout_id ILIKE '%%' || $%d || '%%' OR beneficiary_name ILIKE '%%' || $%[1]d || '%%')", filter.Search)
	}

	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, page.FetchLimit())

	rows, err := r.q(ctx).Query(ctx,
		`SELECT `+payoutColumns+` FROM payouts`+clause+
			fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d", len(args)), args...)
	if err != nil {
		return storage.List[*Payout]{}, fmt.Errorf("payout: list: %w", err)
	}
	defer rows.Close()

	var out []*Payout
	for rows.Next() {
		var p Payout
		if err := scanPayout(rows, &p); err != nil {
			return storage.List[*Payout]{}, fmt.Errorf("payout: scan: %w", err)
		}
		out = append(out, &p)
	}
	if err := rows.Err(); err != nil {
		return storage.List[*Payout]{}, fmt.Errorf("payout: list: %w", err)
	}

	return storage.NewList(out, page), nil
}

// StateUpdate is a change to a payout's status.
type StateUpdate struct {
	Status        gateway.PayoutStatus
	GatewayStatus string
	ReferenceNo   string
	FailureCode   string
	FailureReason string
	GatewayData   json.RawMessage

	ApprovedBy   string
	RejectedBy   string
	RejectReason string

	// IdempotencyExpiresAt is set when a payout becomes UNRESOLVED, recording
	// the moment after which retrying stops being safe.
	IdempotencyExpiresAt *time.Time

	MarkSynced bool
}

// ApplyState advances a payout, refusing any transition the domain forbids.
//
// The allowed predecessors go into the WHERE clause rather than being checked
// in Go beforehand, for the same reason the payment repository does it: the
// check and the write are then one statement, so two workers reconciling the
// same payout cannot both decide they are allowed to proceed. A payout that
// does not match is reported as stale rather than overwritten.
func (r *Repository) ApplyState(ctx context.Context, payoutID string, update StateUpdate) (*Payout, error) {
	if !update.Status.Valid() {
		return nil, fmt.Errorf("payout: cannot apply unknown status %q", update.Status)
	}
	data, err := encodeJSONOrNil(update.GatewayData)
	if err != nil {
		return nil, err
	}

	var p Payout
	row := r.q(ctx).QueryRow(ctx, `
		UPDATE payouts SET
			normalized_status      = $2,
			status_rank            = $3,
			gateway_status         = coalesce(nullif($4, ''), gateway_status),
			reference_no           = coalesce(nullif($5, ''), reference_no),
			failure_code           = coalesce(nullif($6, ''), failure_code),
			failure_reason         = coalesce(nullif($7, ''), failure_reason),
			gateway_data           = coalesce($8, gateway_data),
			approved_by            = coalesce(nullif($9, ''), approved_by),
			approved_at            = CASE WHEN $2 = 'APPROVED'  AND approved_at  IS NULL THEN now() ELSE approved_at END,
			rejected_by            = coalesce(nullif($10, ''), rejected_by),
			rejected_at            = CASE WHEN $2 = 'REJECTED'  AND rejected_at  IS NULL THEN now() ELSE rejected_at END,
			reject_reason          = coalesce(nullif($11, ''), reject_reason),
			submitted_at           = CASE WHEN $2 = 'SUBMITTED' AND submitted_at IS NULL THEN now() ELSE submitted_at END,
			completed_at           = CASE WHEN $2 = 'COMPLETED' AND completed_at IS NULL THEN now() ELSE completed_at END,
			failed_at              = CASE WHEN $2 = 'FAILED'    AND failed_at    IS NULL THEN now() ELSE failed_at END,
			idempotency_expires_at = coalesce($12, idempotency_expires_at),
			last_synced_at         = CASE WHEN $13 THEN now() ELSE last_synced_at END,
			updated_at             = now()
		WHERE id = $1
		  AND normalized_status = ANY($14)
		RETURNING `+payoutColumns,
		payoutID, string(update.Status), update.Status.Rank(),
		update.GatewayStatus, update.ReferenceNo,
		update.FailureCode, update.FailureReason, data,
		update.ApprovedBy, update.RejectedBy, update.RejectReason,
		update.IdempotencyExpiresAt, update.MarkSynced,
		gateway.PayoutPredecessorsOf(update.Status),
	)
	if err := scanPayout(row, &p); err != nil {
		if storage.IsNotFound(err) {
			return nil, ErrStaleTransition
		}
		return nil, fmt.Errorf("payout: apply state: %w", err)
	}
	return &p, nil
}

// TouchSynced records a reconciliation that changed nothing, so the poller
// does not immediately pick the same payout up again.
func (r *Repository) TouchSynced(ctx context.Context, payoutID string) error {
	_, err := r.q(ctx).Exec(ctx,
		`UPDATE payouts SET last_synced_at = now() WHERE id = $1`, payoutID)
	if err != nil {
		return fmt.Errorf("payout: touch synced: %w", err)
	}
	return nil
}

// ClaimUnsettled locks a batch of payouts whose outcome PayMux does not know,
// so several workers can reconcile in parallel without racing each other.
//
// FOR UPDATE SKIP LOCKED is the same mechanism the delivery queue uses. It
// matters more here: two workers both deciding to submit the same approved
// payout is how one transfer becomes two.
func (r *Repository) ClaimUnsettled(ctx context.Context, limit int, olderThan time.Duration) ([]*Payout, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT `+payoutColumns+` FROM payouts
		WHERE normalized_status IN ('APPROVED', 'SUBMITTED', 'UNRESOLVED')
		  AND (last_synced_at IS NULL OR last_synced_at < now() - $2::interval)
		ORDER BY last_synced_at NULLS FIRST
		LIMIT $1
		FOR UPDATE SKIP LOCKED`,
		limit, olderThan.String())
	if err != nil {
		return nil, fmt.Errorf("payout: claim unsettled: %w", err)
	}
	defer rows.Close()

	var out []*Payout
	for rows.Next() {
		var p Payout
		if err := scanPayout(rows, &p); err != nil {
			return nil, fmt.Errorf("payout: scan unsettled: %w", err)
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// SentToday totals what an application has already committed today.
//
// SUBMITTED counts even though the money has not landed: once the gateway has
// it PayMux cannot recall it, so a limit that ignored it would let an
// application spend the same headroom twice while transfers were in flight.
// UNRESOLVED counts for the same reason — it may well have gone out.
func (r *Repository) SentToday(ctx context.Context, applicationID string) (int64, error) {
	var total int64
	err := r.q(ctx).QueryRow(ctx, `
		SELECT coalesce(sum(amount), 0) FROM payouts
		WHERE application_id = $1
		  AND created_at >= now() - interval '24 hours'
		  AND normalized_status IN ('REQUESTED', 'APPROVED', 'SUBMITTED', 'UNRESOLVED', 'COMPLETED')`,
		applicationID).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("payout: sent today: %w", err)
	}
	return total, nil
}

// ---------------------------------------------------------------------------
// Transitions
// ---------------------------------------------------------------------------

// RecordTransition appends to a payout's history.
func (r *Repository) RecordTransition(ctx context.Context, t *Transition) error {
	if t.ID == "" {
		t.ID = ids.New(ids.Transition)
	}
	data, err := encodeJSONOrNil(t.GatewayData)
	if err != nil {
		return err
	}
	_, err = r.q(ctx).Exec(ctx, `
		INSERT INTO payout_transitions
			(id, payout_id, from_status, to_status, actor_kind, actor_id, reason, gateway_data)
		VALUES ($1,$2,$3,$4,$5,$6,$7,coalesce($8, '{}'::jsonb))`,
		t.ID, t.PayoutID, string(t.FromStatus), string(t.ToStatus),
		t.ActorKind, t.ActorID, t.Reason, data)
	if err != nil {
		return fmt.Errorf("payout: record transition: %w", err)
	}
	return nil
}

// Transitions reads a payout's history, oldest first.
func (r *Repository) Transitions(ctx context.Context, payoutID string) ([]*Transition, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT id, payout_id, from_status, to_status, actor_kind, actor_id, reason, created_at
		FROM payout_transitions WHERE payout_id = $1 ORDER BY created_at, id`, payoutID)
	if err != nil {
		return nil, fmt.Errorf("payout: transitions: %w", err)
	}
	defer rows.Close()

	var out []*Transition
	for rows.Next() {
		var t Transition
		if err := rows.Scan(&t.ID, &t.PayoutID, &t.FromStatus, &t.ToStatus,
			&t.ActorKind, &t.ActorID, &t.Reason, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("payout: scan transition: %w", err)
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Limits
// ---------------------------------------------------------------------------

// LimitsFor reads an application's payout permissions.
func (r *Repository) LimitsFor(ctx context.Context, applicationID string) (Limits, error) {
	var l Limits
	err := r.q(ctx).QueryRow(ctx, `
		SELECT payout_enabled, payout_requires_approval, payout_max_amount, payout_daily_limit
		FROM applications WHERE id = $1 AND disabled_at IS NULL`, applicationID).
		Scan(&l.Enabled, &l.RequiresApproval, &l.MaxAmount, &l.DailyLimit)
	if err != nil {
		if storage.IsNotFound(err) {
			// An application that does not exist, or is disabled, cannot pay
			// out. Reporting it as "not permitted" rather than "not found"
			// keeps the two cases indistinguishable to a caller probing ids.
			return Limits{}, ErrPayoutsDisabled
		}
		return Limits{}, fmt.Errorf("payout: limits: %w", err)
	}
	return l, nil
}

// SetLimits updates an application's payout permissions.
func (r *Repository) SetLimits(ctx context.Context, applicationID string, l Limits) error {
	_, err := r.q(ctx).Exec(ctx, `
		UPDATE applications SET
			payout_enabled           = $2,
			payout_requires_approval = $3,
			payout_max_amount        = $4,
			payout_daily_limit       = $5,
			updated_at               = now()
		WHERE id = $1`,
		applicationID, l.Enabled, l.RequiresApproval, l.MaxAmount, l.DailyLimit)
	if err != nil {
		return fmt.Errorf("payout: set limits: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// JSON helpers
// ---------------------------------------------------------------------------

func encodeJSON(v json.RawMessage) ([]byte, error) {
	if len(v) == 0 {
		return []byte(`{}`), nil
	}
	return v, nil
}

func encodeJSONOrNil(v json.RawMessage) ([]byte, error) {
	if len(v) == 0 {
		return nil, nil
	}
	return v, nil
}
