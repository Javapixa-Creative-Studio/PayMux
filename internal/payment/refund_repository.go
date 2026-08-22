package payment

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/ids"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/storage"
)

const refundColumns = `
	id, payment_id, application_id, coalesce(gateway_refund_id, ''), coalesce(refund_key, ''),
	amount, currency, reason, status, gateway_status, failure_reason,
	raw_response, created_at, updated_at`

// CreateRefund stores a refund record.
func (r *Repository) CreateRefund(ctx context.Context, refund *Refund) error {
	if refund.ID == "" {
		refund.ID = ids.New(ids.Refund)
	}
	raw, err := encodeJSON(refund.RawResponse)
	if err != nil {
		return err
	}
	row := r.q(ctx).QueryRow(ctx, `
		INSERT INTO refunds (
			id, payment_id, application_id, gateway_refund_id, refund_key,
			amount, currency, reason, status, gateway_status, failure_reason, raw_response
		) VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6, $7, $8, $9, $10, $11, $12)
		RETURNING `+refundColumns,
		refund.ID, refund.PaymentID, refund.ApplicationID, refund.GatewayRefundID, refund.RefundKey,
		refund.Amount, refund.Currency, refund.Reason, string(refund.Status),
		refund.GatewayStatus, refund.FailureReason, raw,
	)
	return scanRefund(row, refund)
}

// UpdateRefund records the outcome of a refund attempt.
func (r *Repository) UpdateRefund(ctx context.Context, refund *Refund) error {
	raw, err := encodeJSON(refund.RawResponse)
	if err != nil {
		return err
	}
	row := r.q(ctx).QueryRow(ctx, `
		UPDATE refunds SET
			gateway_refund_id = coalesce(nullif($2, ''), gateway_refund_id),
			status            = $3,
			gateway_status    = $4,
			failure_reason    = $5,
			raw_response      = $6,
			updated_at        = now()
		WHERE id = $1
		RETURNING `+refundColumns,
		refund.ID, refund.GatewayRefundID, string(refund.Status),
		refund.GatewayStatus, refund.FailureReason, raw,
	)
	return scanRefund(row, refund)
}

// GetRefund loads one refund scoped to its payment.
func (r *Repository) GetRefund(ctx context.Context, paymentID, refundID string) (*Refund, error) {
	var refund Refund
	row := r.q(ctx).QueryRow(ctx,
		`SELECT `+refundColumns+` FROM refunds WHERE id = $1 AND payment_id = $2`,
		refundID, paymentID)
	if err := scanRefund(row, &refund); err != nil {
		return nil, err
	}
	return &refund, nil
}

// ListRefunds returns a payment's refunds, newest first.
func (r *Repository) ListRefunds(ctx context.Context, paymentID string) ([]*Refund, error) {
	rows, err := r.q(ctx).Query(ctx,
		`SELECT `+refundColumns+` FROM refunds WHERE payment_id = $1 ORDER BY id DESC`, paymentID)
	if err != nil {
		return nil, fmt.Errorf("payment: list refunds: %w", err)
	}
	defer rows.Close()

	var out []*Refund
	for rows.Next() {
		var refund Refund
		if err := scanRefund(rows, &refund); err != nil {
			return nil, err
		}
		out = append(out, &refund)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("payment: list refunds: %w", err)
	}
	return out, nil
}

// SucceededRefundTotal sums the refunds that actually completed.
//
// It is read inside the refund transaction with the payment row locked, so it
// cannot race a concurrent refund into over-refunding a payment.
func (r *Repository) SucceededRefundTotal(ctx context.Context, paymentID string) (int64, error) {
	var total int64
	err := r.q(ctx).QueryRow(ctx, `
		SELECT coalesce(sum(amount), 0) FROM refunds
		WHERE payment_id = $1 AND status = $2`,
		paymentID, string(gateway.RefundSucceeded)).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("payment: sum refunds: %w", err)
	}
	return total, nil
}

func scanRefund(row scanner, refund *Refund) error {
	var (
		status string
		raw    []byte
	)
	err := row.Scan(
		&refund.ID, &refund.PaymentID, &refund.ApplicationID, &refund.GatewayRefundID,
		&refund.RefundKey, &refund.Amount, &refund.Currency, &refund.Reason,
		&status, &refund.GatewayStatus, &refund.FailureReason, &raw,
		&refund.CreatedAt, &refund.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storage.ErrNotFound
		}
		return fmt.Errorf("payment: scan refund: %w", err)
	}
	refund.Status = gateway.RefundStatus(status)
	if refund.RawResponse, err = decodeJSON(raw); err != nil {
		return err
	}
	return nil
}
