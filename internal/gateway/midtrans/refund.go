package midtrans

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/money"
)

// RefundTransaction refunds part or all of a settled transaction (PRD §31).
//
// Midtrans exposes two refund endpoints. The direct "online" refund is used
// for channels that can return funds immediately; everything else goes through
// the standard refund endpoint. The refund key is Midtrans's own idempotency
// mechanism, so a retried request cannot refund twice.
func (a *Adapter) RefundTransaction(ctx context.Context, req gateway.RefundRequest) (*gateway.Refund, error) {
	amount := int64(0)
	if req.Amount > 0 {
		amount = req.Amount
	}
	payload := refundRequest{
		RefundKey: req.RefundKey,
		Amount:    amount,
		Reason:    truncate(req.Reason, 255),
	}

	var resp transactionResponse
	path := "/v2/" + url.PathEscape(req.OrderID) + "/refund"
	err := a.client.do(ctx, http.MethodPost, a.client.coreEndpoint(path), payload, &resp)
	if err != nil {
		return nil, translateRefundError(err)
	}

	return toRefund(&resp, req), nil
}

// toRefund extracts the refund outcome from Midtrans's transaction response.
func toRefund(resp *transactionResponse, req gateway.RefundRequest) *gateway.Refund {
	currency := resp.Currency
	if currency == "" {
		currency = "IDR"
	}
	refundedTotal, _ := money.Parse(resp.RefundAmount, currency)

	amount := req.Amount
	// Midtrans echoes the refunds it has recorded; the matching entry is the
	// authoritative amount for this refund.
	for _, entry := range resp.Refunds {
		if entry.RefundKey != "" && entry.RefundKey == req.RefundKey {
			if parsed, err := money.Parse(entry.RefundAmount, currency); err == nil {
				amount = parsed
			}
			break
		}
	}
	if amount == 0 {
		// A refund with no amount is a full refund.
		amount, _ = money.Parse(resp.GrossAmount, currency)
	}

	normalizedPayment, _ := NormalizeStatus(resp.TransactionStatus, resp.FraudStatus)

	return &gateway.Refund{
		RefundKey:      req.RefundKey,
		Amount:         amount,
		Status:         resp.TransactionStatus,
		Normalized:     gateway.RefundSucceeded,
		PaymentStatus:  normalizedPayment,
		RefundedAmount: refundedTotal,
		Raw:            toRaw(resp),
	}
}

// translateRefundError maps Midtrans's refund rejections onto domain errors.
func translateRefundError(err error) error {
	var gwErr *gateway.Error
	if !asGatewayError(err, &gwErr) {
		return err
	}
	message := strings.ToLower(gwErr.Message)
	switch {
	case gwErr.Code == "404":
		return gateway.ErrTransactionNotFound
	case strings.Contains(message, "not allowed") && strings.Contains(message, "refund"):
		// The channel or transaction state does not permit a refund at all.
		return gateway.ErrNotSupported
	default:
		return err
	}
}
