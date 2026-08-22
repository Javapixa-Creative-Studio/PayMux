package payment

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/event"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/ids"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/storage"
)

// RefundInput describes a refund request.
type RefundInput struct {
	ApplicationID string
	PaymentID     string
	// Amount in minor units. Zero means a full refund of the remaining
	// balance.
	Amount int64
	Reason string
	// RefundKey is the caller's idempotency key for this refund. One is
	// generated when the caller does not supply it.
	RefundKey string
}

// Refund refunds part or all of a payment (PRD §31).
//
// The refundable balance is checked with the payment row locked, so two
// concurrent refunds cannot each see the full balance and together refund more
// than was paid.
func (s *Service) Refund(ctx context.Context, in RefundInput) (*Refund, *Payment, error) {
	if in.Amount < 0 {
		return nil, nil, invalid("amount", "must not be negative")
	}
	if len(in.Reason) > 255 {
		return nil, nil, invalid("reason", "must be at most 255 characters")
	}
	if in.RefundKey == "" {
		in.RefundKey = ids.New(ids.Refund)
	}

	var (
		refund  *Refund
		payment *Payment
		adapter gateway.RefundGateway
		amount  int64
	)

	// Phase one: validate against locked state and reserve the refund row.
	err := s.db.InTx(ctx, func(ctx context.Context, _ storage.Querier) error {
		p, err := s.repo.LockForUpdate(ctx, in.PaymentID)
		if err != nil {
			return err
		}
		if in.ApplicationID != "" && p.ApplicationID != in.ApplicationID {
			// Reported as "not found" so one application cannot probe for
			// another's payment identifiers (PRD §49).
			return storage.ErrNotFound
		}
		if !p.NormalizedStatus.Settled() {
			return fmt.Errorf("%w: the payment is %s", ErrNotRefundable, p.NormalizedStatus)
		}

		refunded, err := s.repo.SucceededRefundTotal(ctx, p.ID)
		if err != nil {
			return err
		}
		available := p.Amount - refunded
		if available <= 0 {
			return fmt.Errorf("%w: the payment is fully refunded", ErrNotRefundable)
		}

		amount = in.Amount
		if amount == 0 {
			amount = available
		}
		if amount > available {
			return fmt.Errorf("%w: %d requested, %d available", ErrRefundExceedsBalance, amount, available)
		}

		account, err := s.accounts.Get(ctx, p.GatewayAccountID)
		if err != nil {
			return err
		}
		g, err := s.registry.For(account)
		if err != nil {
			return err
		}
		refundGateway, ok := g.(gateway.RefundGateway)
		if !ok {
			return gateway.ErrNotSupported
		}
		adapter = refundGateway

		refund = &Refund{
			PaymentID:     p.ID,
			ApplicationID: p.ApplicationID,
			RefundKey:     in.RefundKey,
			Amount:        amount,
			Currency:      p.Currency,
			Reason:        strings.TrimSpace(in.Reason),
			Status:        gateway.RefundPending,
		}
		if err := s.repo.CreateRefund(ctx, refund); err != nil {
			if storage.IsUniqueViolation(err, ConstraintRefundKey) {
				// The same refund key was already used for this payment, so
				// this is a retry of a refund that already exists.
				return ErrIdempotencyConflict
			}
			return err
		}
		payment = p
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	// Phase two: call the gateway outside the transaction, so a slow gateway
	// never holds a database row lock.
	gatewayRefund, refundErr := adapter.RefundTransaction(ctx, gateway.RefundRequest{
		OrderID:       payment.GatewayOrderID,
		TransactionID: payment.GatewayTransactionID,
		Amount:        amount,
		Reason:        refund.Reason,
		RefundKey:     refund.RefundKey,
	})
	if refundErr != nil {
		refund.Status = gateway.RefundFailed
		refund.FailureReason = refundErr.Error()
		if err := s.repo.UpdateRefund(ctx, refund); err != nil {
			s.logger.Error("could not record a failed refund", "refund_id", refund.ID, "error", err)
		}
		s.publishRefundEvent(ctx, payment, refund, event.RefundFailed)
		return nil, nil, refundErr
	}

	refund.Status = gateway.RefundSucceeded
	refund.GatewayRefundID = gatewayRefund.RefundID
	refund.GatewayStatus = gatewayRefund.Status
	refund.RawResponse = gatewayRefund.Raw
	if err := s.repo.UpdateRefund(ctx, refund); err != nil {
		return nil, nil, err
	}

	updated, err := s.applyRefundToPayment(ctx, payment, gatewayRefund)
	if err != nil {
		return nil, nil, err
	}

	s.publishRefundEvent(ctx, updated, refund, event.RefundCompleted)
	return refund, updated, nil
}

// applyRefundToPayment moves the payment to its post-refund state.
func (s *Service) applyRefundToPayment(ctx context.Context, p *Payment, gatewayRefund *gateway.Refund) (*Payment, error) {
	refundedTotal := gatewayRefund.RefundedAmount
	if refundedTotal == 0 {
		// The gateway did not report a cumulative total; derive it from
		// PayMux's own successful refunds.
		total, err := s.repo.SucceededRefundTotal(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		refundedTotal = total
	}

	status := gateway.StatusPartiallyRefunded
	if refundedTotal >= p.Amount {
		status = gateway.StatusRefunded
	}
	if gatewayRefund.PaymentStatus != "" {
		status = gatewayRefund.PaymentStatus
	}

	updated, err := s.repo.ApplyState(ctx, p.ID, StateUpdate{
		NormalizedStatus: status,
		GatewayStatus:    gatewayRefund.Status,
		RefundedAmount:   &refundedTotal,
		GatewayData:      gatewayRefund.Raw,
	})
	if err != nil {
		if errors.Is(err, ErrStaleTransition) {
			// The payment is already at or past this state; the refund record
			// itself is what matters and it is stored.
			return s.repo.Get(ctx, p.ID)
		}
		return nil, err
	}

	if eventType, ok := event.TypeForStatus(updated.NormalizedStatus); ok {
		s.publishPaymentEvent(ctx, updated, eventType, "")
	}
	return updated, nil
}

// ListRefunds returns a payment's refunds, enforcing ownership.
func (s *Service) ListRefunds(ctx context.Context, applicationID, paymentID string) ([]*Refund, error) {
	if _, err := s.load(ctx, applicationID, paymentID); err != nil {
		return nil, err
	}
	return s.repo.ListRefunds(ctx, paymentID)
}

// ListAllRefunds returns refunds across payments, for the dashboard.
func (s *Service) ListAllRefunds(ctx context.Context, filter RefundFilter, page storage.Page) (storage.List[*Refund], error) {
	return s.repo.ListAll(ctx, filter, page)
}

// GetRefund returns one refund, enforcing ownership.
func (s *Service) GetRefund(ctx context.Context, applicationID, paymentID, refundID string) (*Refund, error) {
	if _, err := s.load(ctx, applicationID, paymentID); err != nil {
		return nil, err
	}
	return s.repo.GetRefund(ctx, paymentID, refundID)
}

func (s *Service) publishRefundEvent(ctx context.Context, p *Payment, refund *Refund, eventType event.Type) {
	payload := BuildPayload(p, eventType)
	payload.RefundID = refund.ID
	payload.Amount = refund.Amount
	payload.RefundedAmount = p.RefundedAmount

	e := &event.Event{
		ApplicationID: p.ApplicationID,
		Type:          eventType,
		Gateway:       p.Gateway,
		PaymentID:     p.ID,
		RefundID:      refund.ID,
		DedupeKey:     event.RefundDedupeKey(refund.ID, eventType),
		Payload:       payload,
	}
	if _, err := s.publisher.Publish(ctx, e); err != nil {
		s.logger.Error("could not publish a refund event",
			"refund_id", refund.ID, "type", eventType, "error", err)
	}
}
