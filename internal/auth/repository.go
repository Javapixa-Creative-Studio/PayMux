package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/anggapixa/paymux/internal/ids"
	"github.com/anggapixa/paymux/internal/storage"
)

// ConstraintEmailUnique is the unique index on an administrator's email.
const ConstraintEmailUnique = "admins_email_key"

// Repository persists administrators and their sessions.
type Repository struct {
	db *storage.DB
}

// NewRepository builds a Repository.
func NewRepository(db *storage.DB) *Repository { return &Repository{db: db} }

func (r *Repository) q(ctx context.Context) storage.Querier { return r.db.FromContext(ctx) }

const adminColumns = `id, email, name, password_hash, disabled_at, last_login_at, created_at, updated_at`

// CreateAdmin stores a new administrator.
func (r *Repository) CreateAdmin(ctx context.Context, admin *Admin) error {
	if admin.ID == "" {
		admin.ID = ids.New(ids.Admin)
	}
	row := r.q(ctx).QueryRow(ctx, `
		INSERT INTO admins (id, email, name, password_hash)
		VALUES ($1, $2, $3, $4)
		RETURNING `+adminColumns,
		admin.ID, strings.TrimSpace(admin.Email), admin.Name, admin.PasswordHash,
	)
	return scanAdmin(row, admin)
}

// GetAdmin loads an administrator by identifier.
func (r *Repository) GetAdmin(ctx context.Context, id string) (*Admin, error) {
	var admin Admin
	row := r.q(ctx).QueryRow(ctx, `SELECT `+adminColumns+` FROM admins WHERE id = $1`, id)
	if err := scanAdmin(row, &admin); err != nil {
		return nil, err
	}
	return &admin, nil
}

// GetAdminByEmail loads an administrator by case-insensitive email.
func (r *Repository) GetAdminByEmail(ctx context.Context, email string) (*Admin, error) {
	var admin Admin
	row := r.q(ctx).QueryRow(ctx,
		`SELECT `+adminColumns+` FROM admins WHERE lower(email) = lower($1)`, strings.TrimSpace(email))
	if err := scanAdmin(row, &admin); err != nil {
		return nil, err
	}
	return &admin, nil
}

// CountAdmins reports how many administrators exist, which is what decides
// whether the bootstrap account still needs creating.
func (r *Repository) CountAdmins(ctx context.Context) (int, error) {
	var n int
	if err := r.q(ctx).QueryRow(ctx, `SELECT count(*) FROM admins`).Scan(&n); err != nil {
		return 0, fmt.Errorf("auth: count admins: %w", err)
	}
	return n, nil
}

// ListAdmins returns every administrator, newest first.
func (r *Repository) ListAdmins(ctx context.Context) ([]*Admin, error) {
	rows, err := r.q(ctx).Query(ctx, `SELECT `+adminColumns+` FROM admins ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("auth: list admins: %w", err)
	}
	defer rows.Close()

	var out []*Admin
	for rows.Next() {
		var admin Admin
		if err := scanAdmin(rows, &admin); err != nil {
			return nil, err
		}
		out = append(out, &admin)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: list admins: %w", err)
	}
	return out, nil
}

// UpdatePassword replaces an administrator's password hash and, as a
// precaution, revokes their other sessions.
func (r *Repository) UpdatePassword(ctx context.Context, adminID, passwordHash string) error {
	return r.db.InTx(ctx, func(ctx context.Context, tx storage.Querier) error {
		tag, err := tx.Exec(ctx,
			`UPDATE admins SET password_hash = $2, updated_at = now() WHERE id = $1`,
			adminID, passwordHash)
		if err != nil {
			return fmt.Errorf("auth: update password: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return storage.ErrNotFound
		}
		if _, err := tx.Exec(ctx,
			`UPDATE admin_sessions SET revoked_at = now() WHERE admin_id = $1 AND revoked_at IS NULL`,
			adminID); err != nil {
			return fmt.Errorf("auth: revoke sessions: %w", err)
		}
		return nil
	})
}

// SetAdminDisabled enables or disables an administrator, revoking their
// sessions when disabling so access ends immediately.
func (r *Repository) SetAdminDisabled(ctx context.Context, adminID string, disabled bool) (*Admin, error) {
	var admin Admin
	err := r.db.InTx(ctx, func(ctx context.Context, tx storage.Querier) error {
		row := tx.QueryRow(ctx, `
			UPDATE admins
			SET disabled_at = CASE WHEN $2 THEN coalesce(disabled_at, now()) ELSE NULL END,
			    updated_at = now()
			WHERE id = $1
			RETURNING `+adminColumns, adminID, disabled)
		if err := scanAdmin(row, &admin); err != nil {
			return err
		}
		if disabled {
			if _, err := tx.Exec(ctx,
				`UPDATE admin_sessions SET revoked_at = now() WHERE admin_id = $1 AND revoked_at IS NULL`,
				adminID); err != nil {
				return fmt.Errorf("auth: revoke sessions: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &admin, nil
}

// TouchLogin records a successful sign-in.
func (r *Repository) TouchLogin(ctx context.Context, adminID string) error {
	if _, err := r.q(ctx).Exec(ctx,
		`UPDATE admins SET last_login_at = now() WHERE id = $1`, adminID); err != nil {
		return fmt.Errorf("auth: record login: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Sessions
// ---------------------------------------------------------------------------

const sessionColumns = `id, admin_id, expires_at, revoked_at, user_agent, ip_address, created_at`

// CreateSession stores a session keyed by the hash of its token.
func (r *Repository) CreateSession(ctx context.Context, session *Session, tokenHash string) error {
	if session.ID == "" {
		session.ID = ids.New(ids.Session)
	}
	row := r.q(ctx).QueryRow(ctx, `
		INSERT INTO admin_sessions (id, admin_id, token_hash, expires_at, user_agent, ip_address)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+sessionColumns,
		session.ID, session.AdminID, tokenHash, session.ExpiresAt, session.UserAgent, session.IPAddress,
	)
	return scanSession(row, session)
}

// SessionWithAdmin resolves a session token hash to its session and owner in
// a single query.
func (r *Repository) SessionWithAdmin(ctx context.Context, tokenHash string) (*Session, *Admin, error) {
	row := r.q(ctx).QueryRow(ctx, `
		SELECT
			s.id, s.admin_id, s.expires_at, s.revoked_at, s.user_agent, s.ip_address, s.created_at,
			a.id, a.email, a.name, a.password_hash, a.disabled_at, a.last_login_at, a.created_at, a.updated_at
		FROM admin_sessions s
		JOIN admins a ON a.id = s.admin_id
		WHERE s.token_hash = $1`, tokenHash)

	var (
		session Session
		admin   Admin
	)
	err := row.Scan(
		&session.ID, &session.AdminID, &session.ExpiresAt, &session.RevokedAt,
		&session.UserAgent, &session.IPAddress, &session.CreatedAt,
		&admin.ID, &admin.Email, &admin.Name, &admin.PasswordHash,
		&admin.DisabledAt, &admin.LastLoginAt, &admin.CreatedAt, &admin.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, storage.ErrNotFound
		}
		return nil, nil, fmt.Errorf("auth: load session: %w", err)
	}
	return &session, &admin, nil
}

// RevokeSession ends one session.
func (r *Repository) RevokeSession(ctx context.Context, tokenHash string) error {
	_, err := r.q(ctx).Exec(ctx,
		`UPDATE admin_sessions SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL`,
		tokenHash)
	if err != nil {
		return fmt.Errorf("auth: revoke session: %w", err)
	}
	return nil
}

// DeleteExpiredSessions prunes sessions that expired before cutoff.
func (r *Repository) DeleteExpiredSessions(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := r.q(ctx).Exec(ctx, `DELETE FROM admin_sessions WHERE expires_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("auth: prune sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanAdmin(row scanner, admin *Admin) error {
	err := row.Scan(
		&admin.ID, &admin.Email, &admin.Name, &admin.PasswordHash,
		&admin.DisabledAt, &admin.LastLoginAt, &admin.CreatedAt, &admin.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storage.ErrNotFound
		}
		return fmt.Errorf("auth: scan admin: %w", err)
	}
	return nil
}

func scanSession(row scanner, session *Session) error {
	err := row.Scan(
		&session.ID, &session.AdminID, &session.ExpiresAt, &session.RevokedAt,
		&session.UserAgent, &session.IPAddress, &session.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storage.ErrNotFound
		}
		return fmt.Errorf("auth: scan session: %w", err)
	}
	return nil
}
