package storage

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrNotFound is returned by repositories when a row does not exist. Callers
// translate it into the right API error for their resource, so the storage
// layer never decides what a client sees.
var ErrNotFound = errors.New("storage: record not found")

// PostgreSQL error codes PayMux reacts to.
const (
	codeUniqueViolation      = "23505"
	codeForeignKeyViolation  = "23503"
	codeCheckViolation       = "23514"
	codeSerializationFailure = "40001"
	codeDeadlockDetected     = "40P01"
)

// IsNotFound reports whether err means "no such row", covering both the
// repository sentinel and pgx's own no-rows error.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, pgx.ErrNoRows)
}

// IsUniqueViolation reports whether err is a unique-constraint violation,
// optionally narrowed to specific constraint names.
//
// Constraint names matter here: PayMux relies on unique indexes for
// idempotency, so "which constraint fired" decides whether a duplicate is a
// harmless replay or a genuine conflict.
func IsUniqueViolation(err error, constraints ...string) bool {
	pgErr := asPgError(err)
	if pgErr == nil || pgErr.Code != codeUniqueViolation {
		return false
	}
	if len(constraints) == 0 {
		return true
	}
	for _, name := range constraints {
		if pgErr.ConstraintName == name {
			return true
		}
	}
	return false
}

// ConstraintName returns the constraint a database error violated, if any.
func ConstraintName(err error) string {
	if pgErr := asPgError(err); pgErr != nil {
		return pgErr.ConstraintName
	}
	return ""
}

// IsForeignKeyViolation reports whether err is a foreign-key violation.
func IsForeignKeyViolation(err error) bool {
	pgErr := asPgError(err)
	return pgErr != nil && pgErr.Code == codeForeignKeyViolation
}

// IsCheckViolation reports whether err is a CHECK-constraint violation.
func IsCheckViolation(err error) bool {
	pgErr := asPgError(err)
	return pgErr != nil && pgErr.Code == codeCheckViolation
}

// IsRetryable reports whether err is a transient failure: a serialization
// conflict or deadlock: that is worth retrying with the same input.
func IsRetryable(err error) bool {
	pgErr := asPgError(err)
	if pgErr == nil {
		return false
	}
	return pgErr.Code == codeSerializationFailure || pgErr.Code == codeDeadlockDetected
}

func asPgError(err error) *pgconn.PgError {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr
	}
	return nil
}
