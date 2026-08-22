// Package storage owns PayMux's PostgreSQL connection pool, migration
// runner and the small helpers repositories share.
package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps a pgx pool with the transaction helpers repositories use.
type DB struct {
	pool *pgxpool.Pool
}

// Options configures the connection pool.
type Options struct {
	URL         string
	MaxConns    int32
	MinConns    int32
	ConnTimeout time.Duration
}

// Connect opens the pool and verifies the database is reachable.
func Connect(ctx context.Context, opts Options) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(opts.URL)
	if err != nil {
		return nil, fmt.Errorf("storage: parse DATABASE_URL: %w", err)
	}
	if opts.MaxConns > 0 {
		cfg.MaxConns = opts.MaxConns
	}
	if opts.MinConns > 0 {
		cfg.MinConns = opts.MinConns
	}
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute
	// Every statement runs in UTC so timestamps never depend on server locale.
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("storage: create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, orDefault(opts.ConnTimeout, 10*time.Second))
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("storage: ping database: %w", err)
	}
	return &DB{pool: pool}, nil
}

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

// Pool exposes the underlying pool for callers that need it directly.
func (db *DB) Pool() *pgxpool.Pool { return db.pool }

// Close releases every pooled connection.
func (db *DB) Close() {
	if db != nil && db.pool != nil {
		db.pool.Close()
	}
}

// Ping reports whether the database is reachable, backing /ready.
func (db *DB) Ping(ctx context.Context) error {
	if db == nil || db.pool == nil {
		return errors.New("storage: database is not configured")
	}
	return db.pool.Ping(ctx)
}

// Querier is satisfied by both the pool and an open transaction, so
// repository methods can run inside or outside a transaction unchanged.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var (
	_ Querier = (*pgxpool.Pool)(nil)
	_ Querier = (pgx.Tx)(nil)
)

// Q returns the pool as a Querier.
func (db *DB) Q() Querier { return db.pool }

// InTx runs fn inside a transaction, committing on success and rolling back
// on error or panic. Nested calls reuse the outer transaction.
func (db *DB) InTx(ctx context.Context, fn func(ctx context.Context, tx Querier) error) error {
	if tx, ok := txFromContext(ctx); ok {
		return fn(ctx, tx)
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("storage: begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			// Rollback with a fresh context: the caller's may already be done.
			rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = tx.Rollback(rollbackCtx)
		}
	}()

	if err := fn(withTx(ctx, tx), tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("storage: commit transaction: %w", err)
	}
	committed = true
	return nil
}

type txKey struct{}

func withTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

func txFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	return tx, ok
}

// FromContext returns the transaction bound to ctx, or the pool when the
// caller is not inside one.
func (db *DB) FromContext(ctx context.Context) Querier {
	if tx, ok := txFromContext(ctx); ok {
		return tx
	}
	return db.pool
}
