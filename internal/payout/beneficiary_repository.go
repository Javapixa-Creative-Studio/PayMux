package payout

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/ids"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/storage"
)

const beneficiaryColumns = `
	id, application_id, alias, name, account, bank, email,
	verified_at, verified_name, disabled_at, metadata, created_at, updated_at`

func scanBeneficiary(row pgx.Row, b *Beneficiary) error {
	err := row.Scan(&b.ID, &b.ApplicationID, &b.Alias, &b.Name, &b.Account, &b.Bank, &b.Email,
		&b.VerifiedAt, &b.VerifiedName, &b.DisabledAt, &b.Metadata, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return err
	}
	b.Object = "beneficiary"
	return nil
}

// CreateBeneficiary stores a payout destination.
func (r *Repository) CreateBeneficiary(ctx context.Context, b *Beneficiary) error {
	if b.ID == "" {
		b.ID = ids.New(ids.Beneficiary)
	}
	metadata, err := encodeJSON(b.Metadata)
	if err != nil {
		return err
	}
	row := r.q(ctx).QueryRow(ctx, `
		INSERT INTO beneficiaries
			(id, application_id, alias, name, account, bank, email, metadata)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING `+beneficiaryColumns,
		b.ID, b.ApplicationID, b.Alias, b.Name, b.Account, b.Bank, b.Email, metadata)
	if err := scanBeneficiary(row, b); err != nil {
		if storage.IsUniqueViolation(err, ConstraintBeneficiary) {
			return ErrDuplicateAlias
		}
		return fmt.Errorf("payout: create beneficiary: %w", err)
	}
	return nil
}

// GetBeneficiary reads a destination an application owns.
func (r *Repository) GetBeneficiary(ctx context.Context, applicationID, id string) (*Beneficiary, error) {
	var b Beneficiary
	row := r.q(ctx).QueryRow(ctx,
		`SELECT `+beneficiaryColumns+` FROM beneficiaries WHERE id = $1 AND application_id = $2`,
		id, applicationID)
	if err := scanBeneficiary(row, &b); err != nil {
		if storage.IsNotFound(err) {
			return nil, ErrBeneficiaryNotFound
		}
		return nil, fmt.Errorf("payout: get beneficiary: %w", err)
	}
	return &b, nil
}

// GetBeneficiaryByAlias resolves an application's own handle for a
// destination, so callers can name one without holding PayMux identifiers.
func (r *Repository) GetBeneficiaryByAlias(ctx context.Context, applicationID, alias string) (*Beneficiary, error) {
	var b Beneficiary
	row := r.q(ctx).QueryRow(ctx,
		`SELECT `+beneficiaryColumns+` FROM beneficiaries
		 WHERE application_id = $1 AND lower(alias) = lower($2)`,
		applicationID, alias)
	if err := scanBeneficiary(row, &b); err != nil {
		if storage.IsNotFound(err) {
			return nil, ErrBeneficiaryNotFound
		}
		return nil, fmt.Errorf("payout: get beneficiary by alias: %w", err)
	}
	return &b, nil
}

// ListBeneficiaries returns an application's destinations.
func (r *Repository) ListBeneficiaries(ctx context.Context, applicationID string, page storage.Page) (storage.List[*Beneficiary], error) {
	rows, err := r.q(ctx).Query(ctx,
		`SELECT `+beneficiaryColumns+` FROM beneficiaries
		 WHERE application_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`,
		applicationID, page.FetchLimit())
	if err != nil {
		return storage.List[*Beneficiary]{}, fmt.Errorf("payout: list beneficiaries: %w", err)
	}
	defer rows.Close()

	var out []*Beneficiary
	for rows.Next() {
		var b Beneficiary
		if err := scanBeneficiary(rows, &b); err != nil {
			return storage.List[*Beneficiary]{}, fmt.Errorf("payout: scan beneficiary: %w", err)
		}
		out = append(out, &b)
	}
	if err := rows.Err(); err != nil {
		return storage.List[*Beneficiary]{}, fmt.Errorf("payout: list beneficiaries: %w", err)
	}
	return storage.NewList(out, page), nil
}

// BeneficiaryUpdate is a change to a destination.
//
// Fields are pointers so that "leave this alone" and "set this to empty" stay
// distinguishable — on a record that decides where money goes, the difference
// is not one to infer.
type BeneficiaryUpdate struct {
	Name     *string
	Account  *string
	Bank     *string
	Email    *string
	Disabled *bool
}

// UpdateBeneficiary changes a destination.
//
// Editing the account or bank clears any verification: the confirmation was
// about the old number and says nothing about the new one.
func (r *Repository) UpdateBeneficiary(ctx context.Context, applicationID, id string, u BeneficiaryUpdate) (*Beneficiary, error) {
	var b Beneficiary
	row := r.q(ctx).QueryRow(ctx, `
		UPDATE beneficiaries SET
			name          = coalesce($3, name),
			account       = coalesce($4, account),
			bank          = coalesce($5, bank),
			email         = coalesce($6, email),
			disabled_at   = CASE
			                  WHEN $7::boolean IS NULL THEN disabled_at
			                  WHEN $7 THEN coalesce(disabled_at, now())
			                  ELSE NULL
			                END,
			verified_at   = CASE WHEN $4 IS NOT NULL OR $5 IS NOT NULL THEN NULL ELSE verified_at END,
			verified_name = CASE WHEN $4 IS NOT NULL OR $5 IS NOT NULL THEN '' ELSE verified_name END,
			updated_at    = now()
		WHERE id = $1 AND application_id = $2
		RETURNING `+beneficiaryColumns,
		id, applicationID, u.Name, u.Account, u.Bank, u.Email, u.Disabled)
	if err := scanBeneficiary(row, &b); err != nil {
		if storage.IsNotFound(err) {
			return nil, ErrBeneficiaryNotFound
		}
		return nil, fmt.Errorf("payout: update beneficiary: %w", err)
	}
	return &b, nil
}

// MarkBeneficiaryVerified records that the gateway confirmed the account and
// the name the bank holds for it.
func (r *Repository) MarkBeneficiaryVerified(ctx context.Context, applicationID, id, verifiedName string, at time.Time) error {
	tag, err := r.q(ctx).Exec(ctx, `
		UPDATE beneficiaries
		SET verified_at = $3, verified_name = $4, updated_at = now()
		WHERE id = $1 AND application_id = $2`,
		id, applicationID, at, verifiedName)
	if err != nil {
		return fmt.Errorf("payout: mark beneficiary verified: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBeneficiaryNotFound
	}
	return nil
}

// DeleteBeneficiary removes a destination.
//
// It fails while payouts still reference it, by design: the row is part of the
// record of where money went, and the foreign key says so.
func (r *Repository) DeleteBeneficiary(ctx context.Context, applicationID, id string) error {
	tag, err := r.q(ctx).Exec(ctx,
		`DELETE FROM beneficiaries WHERE id = $1 AND application_id = $2`, id, applicationID)
	if err != nil {
		return fmt.Errorf("payout: delete beneficiary: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBeneficiaryNotFound
	}
	return nil
}
