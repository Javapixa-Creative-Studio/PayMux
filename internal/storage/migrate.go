package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anggapixa/paymux/migrations"
)

// migrationsTable records which migrations have run.
const migrationsTable = "schema_migrations"

// Migration is one versioned schema change.
type Migration struct {
	Name     string
	SQL      string
	Checksum string
}

// LoadMigrations reads the embedded migrations in lexical filename order,
// which is also their intended application order.
func LoadMigrations() ([]Migration, error) {
	entries, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("storage: list migrations: %w", err)
	}
	sort.Strings(entries)

	out := make([]Migration, 0, len(entries))
	for _, name := range entries {
		body, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			return nil, fmt.Errorf("storage: read migration %s: %w", name, err)
		}
		sum := sha256.Sum256(body)
		out = append(out, Migration{
			Name:     name,
			SQL:      string(body),
			Checksum: hex.EncodeToString(sum[:]),
		})
	}
	return out, nil
}

// Migrate applies every migration that has not run yet.
//
// Each migration runs inside its own transaction together with the bookkeeping
// row, so a failure leaves neither a half-applied schema nor a false record of
// success. A session-level advisory lock serialises concurrent API and worker
// instances starting at the same time.
func (db *DB) Migrate(ctx context.Context) ([]string, error) {
	all, err := LoadMigrations()
	if err != nil {
		return nil, err
	}

	conn, err := db.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("storage: acquire connection: %w", err)
	}
	defer conn.Release()

	// Derived from the product name so unrelated applications sharing the
	// database cannot collide with this lock.
	const advisoryLockID = int64(7259105108831489) // "paymux" hashed to a constant
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockID); err != nil {
		return nil, fmt.Errorf("storage: acquire migration lock: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", advisoryLockID)
	}()

	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS `+migrationsTable+` (
			name        TEXT PRIMARY KEY,
			checksum    TEXT        NOT NULL,
			applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return nil, fmt.Errorf("storage: create migrations table: %w", err)
	}

	applied, err := appliedMigrations(ctx, conn)
	if err != nil {
		return nil, err
	}

	var ran []string
	for _, m := range all {
		if existing, ok := applied[m.Name]; ok {
			if existing != m.Checksum {
				return ran, fmt.Errorf(
					"storage: migration %s was modified after it was applied (checksum %s, expected %s); "+
						"add a new migration instead of editing an applied one",
					m.Name, m.Checksum, existing)
			}
			continue
		}
		if err := applyMigration(ctx, conn, m); err != nil {
			return ran, err
		}
		ran = append(ran, m.Name)
	}
	return ran, nil
}

func appliedMigrations(ctx context.Context, conn *pgxpool.Conn) (map[string]string, error) {
	rows, err := conn.Query(ctx, "SELECT name, checksum FROM "+migrationsTable)
	if err != nil {
		return nil, fmt.Errorf("storage: read applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]string)
	for rows.Next() {
		var name, checksum string
		if err := rows.Scan(&name, &checksum); err != nil {
			return nil, fmt.Errorf("storage: scan applied migration: %w", err)
		}
		applied[name] = checksum
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: read applied migrations: %w", err)
	}
	return applied, nil
}

func applyMigration(ctx context.Context, conn *pgxpool.Conn, m Migration) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("storage: begin migration %s: %w", m.Name, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, m.SQL); err != nil {
		return fmt.Errorf("storage: apply migration %s: %w", m.Name, err)
	}
	if _, err := tx.Exec(ctx,
		"INSERT INTO "+migrationsTable+" (name, checksum) VALUES ($1, $2)",
		m.Name, m.Checksum,
	); err != nil {
		return fmt.Errorf("storage: record migration %s: %w", m.Name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("storage: commit migration %s: %w", m.Name, err)
	}
	return nil
}

// MigrationNames returns the embedded migration filenames, for diagnostics.
func MigrationNames() ([]string, error) {
	all, err := LoadMigrations()
	if err != nil {
		return nil, err
	}
	names := make([]string, len(all))
	for i, m := range all {
		names[i] = m.Name
	}
	return names, nil
}

// StatementCount reports how many statements a migration contains, used by
// tests to catch an accidentally truncated file.
func StatementCount(sql string) int {
	n := 0
	for _, stmt := range strings.Split(sql, ";") {
		if strings.TrimSpace(stripComments(stmt)) != "" {
			n++
		}
	}
	return n
}

func stripComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
