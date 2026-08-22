package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/anggapixa/paymux/internal/crypto"
	"github.com/anggapixa/paymux/internal/ids"
	"github.com/anggapixa/paymux/internal/storage"
)

// ConstraintNameUnique is the unique index on an account's display name.
const ConstraintNameUnique = "gateway_accounts_name_key"

// Repository persists gateway accounts, sealing server keys at rest.
type Repository struct {
	db     *storage.DB
	sealer *crypto.Sealer
}

// NewRepository builds a Repository.
func NewRepository(db *storage.DB, sealer *crypto.Sealer) *Repository {
	return &Repository{db: db, sealer: sealer}
}

func (r *Repository) q(ctx context.Context) storage.Querier { return r.db.FromContext(ctx) }

func serverKeyContext(accountID string) string {
	return "gateway_account:" + accountID + ":server_key"
}

const accountColumns = `
	id, gateway, name, environment, merchant_id, client_key, server_key_encrypted,
	enabled, is_default, capabilities, last_checked_at, last_check_ok,
	last_check_error, created_at, updated_at`

// Create stores a new gateway account.
func (r *Repository) Create(ctx context.Context, acc *Account) error {
	if acc.ID == "" {
		acc.ID = ids.New(ids.GatewayAccount)
	}
	sealed, err := r.sealer.SealString(acc.ServerKey.Reveal(), serverKeyContext(acc.ID))
	if err != nil {
		return fmt.Errorf("gateway: seal server key: %w", err)
	}
	capabilities, err := json.Marshal(acc.Capabilities)
	if err != nil {
		return fmt.Errorf("gateway: encode capabilities: %w", err)
	}

	return r.db.InTx(ctx, func(ctx context.Context, tx storage.Querier) error {
		if acc.IsDefault {
			if err := clearDefault(ctx, tx, acc.Gateway, acc.Environment, ""); err != nil {
				return err
			}
		}
		row := tx.QueryRow(ctx, `
			INSERT INTO gateway_accounts
				(id, gateway, name, environment, merchant_id, client_key,
				 server_key_encrypted, enabled, is_default, capabilities)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			RETURNING `+accountColumns,
			acc.ID, acc.Gateway, acc.Name, string(acc.Environment), acc.MerchantID,
			acc.ClientKey, sealed, acc.Enabled, acc.IsDefault, capabilities,
		)
		return r.scan(row, acc)
	})
}

// Get loads one account by identifier.
func (r *Repository) Get(ctx context.Context, id string) (*Account, error) {
	var acc Account
	row := r.q(ctx).QueryRow(ctx, `SELECT `+accountColumns+` FROM gateway_accounts WHERE id = $1`, id)
	if err := r.scan(row, &acc); err != nil {
		return nil, err
	}
	return &acc, nil
}

// Default returns the account to use when an application names no specific
// one: the account flagged default for the gateway, or the only enabled one.
func (r *Repository) Default(ctx context.Context, gatewayName string) (*Account, error) {
	var acc Account
	row := r.q(ctx).QueryRow(ctx, `
		SELECT `+accountColumns+`
		FROM gateway_accounts
		WHERE gateway = $1 AND enabled
		ORDER BY is_default DESC, id
		LIMIT 1`, gatewayName)
	if err := r.scan(row, &acc); err != nil {
		return nil, err
	}
	return &acc, nil
}

// List returns every configured account, newest first.
func (r *Repository) List(ctx context.Context) ([]*Account, error) {
	rows, err := r.q(ctx).Query(ctx, `SELECT `+accountColumns+` FROM gateway_accounts ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("gateway: list accounts: %w", err)
	}
	defer rows.Close()

	var out []*Account
	for rows.Next() {
		var acc Account
		if err := r.scan(rows, &acc); err != nil {
			return nil, err
		}
		out = append(out, &acc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("gateway: list accounts: %w", err)
	}
	return out, nil
}

// AccountUpdate carries the mutable fields of an account. Nil means unchanged.
type AccountUpdate struct {
	Name        *string
	MerchantID  *string
	ClientKey   *string
	ServerKey   crypto.Secret
	Enabled     *bool
	IsDefault   *bool
	Environment *Environment
}

// Update applies a partial update to an account.
func (r *Repository) Update(ctx context.Context, id string, update AccountUpdate) (*Account, error) {
	var sealed *string
	if update.ServerKey != "" {
		s, err := r.sealer.SealString(update.ServerKey.Reveal(), serverKeyContext(id))
		if err != nil {
			return nil, fmt.Errorf("gateway: seal server key: %w", err)
		}
		sealed = &s
	}
	var environment *string
	if update.Environment != nil {
		env := string(*update.Environment)
		environment = &env
	}

	var acc Account
	err := r.db.InTx(ctx, func(ctx context.Context, tx storage.Querier) error {
		if update.IsDefault != nil && *update.IsDefault {
			current, err := r.Get(ctx, id)
			if err != nil {
				return err
			}
			env := current.Environment
			if update.Environment != nil {
				env = *update.Environment
			}
			if err := clearDefault(ctx, tx, current.Gateway, env, id); err != nil {
				return err
			}
		}
		row := tx.QueryRow(ctx, `
			UPDATE gateway_accounts SET
				name                 = coalesce($2, name),
				merchant_id          = coalesce($3, merchant_id),
				client_key           = coalesce($4, client_key),
				server_key_encrypted = coalesce($5, server_key_encrypted),
				enabled              = coalesce($6, enabled),
				is_default           = coalesce($7, is_default),
				environment          = coalesce($8, environment),
				updated_at           = now()
			WHERE id = $1
			RETURNING `+accountColumns,
			id, update.Name, update.MerchantID, update.ClientKey, sealed,
			update.Enabled, update.IsDefault, environment,
		)
		return r.scan(row, &acc)
	})
	if err != nil {
		return nil, err
	}
	return &acc, nil
}

// RecordCheck stores the outcome of a connection test.
func (r *Repository) RecordCheck(ctx context.Context, id string, ok bool, message string, caps Capabilities) error {
	encoded, err := json.Marshal(caps)
	if err != nil {
		return fmt.Errorf("gateway: encode capabilities: %w", err)
	}
	_, err = r.q(ctx).Exec(ctx, `
		UPDATE gateway_accounts
		SET last_checked_at = now(), last_check_ok = $2, last_check_error = $3,
		    capabilities = $4, updated_at = now()
		WHERE id = $1`, id, ok, message, encoded)
	if err != nil {
		return fmt.Errorf("gateway: record check: %w", err)
	}
	return nil
}

// Delete removes an account. It fails while payments still reference it,
// because the account is what makes those payments interpretable.
func (r *Repository) Delete(ctx context.Context, id string) error {
	tag, err := r.q(ctx).Exec(ctx, `DELETE FROM gateway_accounts WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("gateway: delete account: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrNotFound
	}
	return nil
}

// clearDefault unsets the default flag for a gateway and environment so the
// partial unique index cannot be violated by promoting a new default.
func clearDefault(ctx context.Context, tx storage.Querier, gatewayName string, env Environment, exceptID string) error {
	_, err := tx.Exec(ctx, `
		UPDATE gateway_accounts
		SET is_default = FALSE, updated_at = now()
		WHERE gateway = $1 AND environment = $2 AND is_default AND id <> $3`,
		gatewayName, string(env), exceptID)
	if err != nil {
		return fmt.Errorf("gateway: clear default account: %w", err)
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func (r *Repository) scan(row scanner, acc *Account) error {
	var (
		environment  string
		sealed       string
		capabilities []byte
	)
	err := row.Scan(
		&acc.ID, &acc.Gateway, &acc.Name, &environment, &acc.MerchantID, &acc.ClientKey,
		&sealed, &acc.Enabled, &acc.IsDefault, &capabilities, &acc.LastCheckedAt,
		&acc.LastCheckOK, &acc.LastCheckError, &acc.CreatedAt, &acc.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storage.ErrNotFound
		}
		return fmt.Errorf("gateway: scan account: %w", err)
	}
	acc.Environment = Environment(environment)

	serverKey, err := r.sealer.OpenString(sealed, serverKeyContext(acc.ID))
	if err != nil {
		return fmt.Errorf("gateway: account %s server key cannot be decrypted "+
			"(has PAYMUX_ENCRYPTION_KEY changed?): %w", acc.ID, err)
	}
	acc.ServerKey = crypto.Secret(serverKey)

	if len(capabilities) > 0 {
		if err := json.Unmarshal(capabilities, &acc.Capabilities); err != nil {
			return fmt.Errorf("gateway: decode capabilities: %w", err)
		}
	}
	return nil
}
