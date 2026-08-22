package midtrans

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/anggapixa/paymux/internal/gateway"
)

// GetTransaction fetches Midtrans's authoritative view of a transaction
// (PRD §27).
func (a *Adapter) GetTransaction(ctx context.Context, orderID string) (*gateway.Transaction, error) {
	var resp transactionResponse
	path := "/v2/" + url.PathEscape(orderID) + "/status"
	if err := a.client.do(ctx, http.MethodGet, a.client.coreEndpoint(path), nil, &resp); err != nil {
		return nil, translateNotFound(err)
	}
	return toTransaction(&resp), nil
}

// CancelTransaction cancels a transaction that has not settled (PRD §28).
func (a *Adapter) CancelTransaction(ctx context.Context, orderID string) (*gateway.Transaction, error) {
	var resp transactionResponse
	path := "/v2/" + url.PathEscape(orderID) + "/cancel"
	if err := a.client.do(ctx, http.MethodPost, a.client.coreEndpoint(path), nil, &resp); err != nil {
		return nil, translateNotFound(err)
	}
	return toTransaction(&resp), nil
}

// ExpireTransaction expires a pending transaction (PRD §29).
//
// Expiring is also how a Snap checkout session is retired: once the underlying
// transaction expires, the Snap token can no longer be paid.
func (a *Adapter) ExpireTransaction(ctx context.Context, orderID string) (*gateway.Transaction, error) {
	var resp transactionResponse
	path := "/v2/" + url.PathEscape(orderID) + "/expire"
	if err := a.client.do(ctx, http.MethodPost, a.client.coreEndpoint(path), nil, &resp); err != nil {
		return nil, translateNotFound(err)
	}
	return toTransaction(&resp), nil
}

// translateNotFound converts Midtrans's "transaction doesn't exist" response
// into the domain's sentinel error, so callers do not have to know that
// Midtrans reports it as status code 404 inside a 200 response body.
func translateNotFound(err error) error {
	var gwErr *gateway.Error
	if errors.As(err, &gwErr) && (gwErr.Code == "404" || gwErr.StatusCode == http.StatusNotFound) {
		return gateway.ErrTransactionNotFound
	}
	return err
}
