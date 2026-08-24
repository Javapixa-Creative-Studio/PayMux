package api

import (
	"time"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/application"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/auth"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
)

// The response types below are PayMux's public wire format. They exist
// separately from the domain models on purpose: a field only reaches a client
// if it is written here, so adding a column to a table cannot accidentally
// publish it, which is what keeps secrets from leaking (PRD §58).

type applicationResponse struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Slug             string         `json:"slug"`
	Description      string         `json:"description"`
	GatewayAccountID string         `json:"gateway_account_id,omitempty"`
	Status           string         `json:"status"`
	Metadata         map[string]any `json:"metadata"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

func renderApplication(app *application.Application) applicationResponse {
	status := "active"
	if !app.Active() {
		status = "disabled"
	}
	metadata := app.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	return applicationResponse{
		ID:               app.ID,
		Name:             app.Name,
		Slug:             app.Slug,
		Description:      app.Description,
		GatewayAccountID: app.GatewayAccountID,
		Status:           status,
		Metadata:         metadata,
		CreatedAt:        app.CreatedAt,
		UpdatedAt:        app.UpdatedAt,
	}
}

type apiKeyResponse struct {
	ID            string     `json:"id"`
	ApplicationID string     `json:"application_id"`
	Name          string     `json:"name"`
	Mode          string     `json:"mode"`
	DisplayPrefix string     `json:"display_prefix"`
	Status        string     `json:"status"`
	LastUsedAt    *time.Time `json:"last_used_at"`
	ExpiresAt     *time.Time `json:"expires_at"`
	RevokedAt     *time.Time `json:"revoked_at"`
	CreatedAt     time.Time  `json:"created_at"`
	// Key is the plaintext credential. It is present only in the response
	// that created the key and is never retrievable afterwards.
	Key string `json:"key,omitempty"`
}

func renderAPIKey(key *application.APIKey) apiKeyResponse {
	status := "active"
	switch {
	case key.RevokedAt != nil:
		status = "revoked"
	case key.ExpiresAt != nil && !key.ExpiresAt.After(time.Now()):
		status = "expired"
	}
	return apiKeyResponse{
		ID:            key.ID,
		ApplicationID: key.ApplicationID,
		Name:          key.Name,
		Mode:          string(key.Mode),
		DisplayPrefix: key.DisplayPrefix,
		Status:        status,
		LastUsedAt:    key.LastUsedAt,
		ExpiresAt:     key.ExpiresAt,
		RevokedAt:     key.RevokedAt,
		CreatedAt:     key.CreatedAt,
	}
}

type destinationResponse struct {
	ID            string    `json:"id"`
	ApplicationID string    `json:"application_id"`
	URL           string    `json:"url"`
	Description   string    `json:"description"`
	EventTypes    []string  `json:"event_types"`
	Enabled       bool      `json:"enabled"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	// Secret is returned only when it is created or rotated.
	Secret string `json:"secret,omitempty"`
}

func renderDestination(dst *application.Destination) destinationResponse {
	types := dst.EventTypes
	if types == nil {
		types = []string{}
	}
	return destinationResponse{
		ID:            dst.ID,
		ApplicationID: dst.ApplicationID,
		URL:           dst.URL,
		Description:   dst.Description,
		EventTypes:    types,
		Enabled:       dst.Enabled,
		CreatedAt:     dst.CreatedAt,
		UpdatedAt:     dst.UpdatedAt,
	}
}

type gatewayAccountResponse struct {
	ID          string `json:"id"`
	Gateway     string `json:"gateway"`
	Name        string `json:"name"`
	Environment string `json:"environment"`
	MerchantID  string `json:"merchant_id"`
	ClientKey   string `json:"client_key"`
	// ServerKeySet reports whether a server key is configured. The key itself
	// is write-only and is never returned (PRD §58).
	ServerKeySet bool `json:"server_key_set"`
	// Disbursement credentials, reported the same write-only way. These are
	// derived from the keys rather than read from Capabilities: that blob is
	// only refreshed when somebody runs a connection test, so it would say an
	// account cannot pay out for as long as nobody had tested it, while the
	// keys sat right there.
	DisbursementCreatorKeySet  bool `json:"disbursement_creator_key_set"`
	DisbursementApproverKeySet bool `json:"disbursement_approver_key_set"`

	Enabled        bool                 `json:"enabled"`
	IsDefault      bool                 `json:"is_default"`
	Capabilities   gateway.Capabilities `json:"capabilities"`
	LastCheckedAt  *time.Time           `json:"last_checked_at"`
	LastCheckOK    *bool                `json:"last_check_ok"`
	LastCheckError string               `json:"last_check_error,omitempty"`
	CreatedAt      time.Time            `json:"created_at"`
	UpdatedAt      time.Time            `json:"updated_at"`
}

func renderGatewayAccount(acc *gateway.Account) gatewayAccountResponse {
	return gatewayAccountResponse{
		ID:                         acc.ID,
		Gateway:                    acc.Gateway,
		Name:                       acc.Name,
		Environment:                string(acc.Environment),
		MerchantID:                 acc.MerchantID,
		ClientKey:                  acc.ClientKey,
		ServerKeySet:               acc.ServerKey != "",
		DisbursementCreatorKeySet:  acc.DisbursementCreatorKey != "",
		DisbursementApproverKeySet: acc.DisbursementApproverKey != "",

		Enabled:        acc.Enabled,
		IsDefault:      acc.IsDefault,
		Capabilities:   acc.Capabilities,
		LastCheckedAt:  acc.LastCheckedAt,
		LastCheckOK:    acc.LastCheckOK,
		LastCheckError: acc.LastCheckError,
		CreatedAt:      acc.CreatedAt,
		UpdatedAt:      acc.UpdatedAt,
	}
}

type adminResponse struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	LastLoginAt *time.Time `json:"last_login_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

func renderAdmin(admin *auth.Admin) adminResponse {
	status := "active"
	if !admin.Active() {
		status = "disabled"
	}
	return adminResponse{
		ID:          admin.ID,
		Email:       admin.Email,
		Name:        admin.Name,
		Status:      status,
		LastLoginAt: admin.LastLoginAt,
		CreatedAt:   admin.CreatedAt,
	}
}

// listResponse is the envelope every collection endpoint returns.
type listResponse[T any] struct {
	Data    []T  `json:"data"`
	HasMore bool `json:"has_more"`
	Limit   int  `json:"limit"`
}

func renderList[T any, S any](items []S, hasMore bool, limit int, render func(S) T) listResponse[T] {
	out := make([]T, 0, len(items))
	for _, item := range items {
		out = append(out, render(item))
	}
	return listResponse[T]{Data: out, HasMore: hasMore, Limit: limit}
}
