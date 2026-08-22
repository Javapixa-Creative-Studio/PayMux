package payment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/anggapixa/paymux/internal/ids"
	"github.com/anggapixa/paymux/internal/storage"
)

// IdempotencyTTL is how long a key is remembered. After it lapses the same
// key may be used again, which keeps the table bounded without making a
// legitimate retry window too short.
const IdempotencyTTL = 24 * time.Hour

// IdempotencyRecord is a remembered request outcome.
type IdempotencyRecord struct {
	ID             string
	ApplicationID  string
	Key            string
	Endpoint       string
	RequestHash    string
	State          string
	PaymentID      string
	ResponseStatus int
	ResponseBody   json.RawMessage
	CreatedAt      time.Time
	CompletedAt    *time.Time
	ExpiresAt      time.Time
}

// Idempotency states.
const (
	idempotencyInProgress = "in_progress"
	idempotencyCompleted  = "completed"
)

// IdempotencyStore remembers request outcomes so a retried call returns the
// original result instead of creating a second payment (PRD §63).
//
// The design is a claim-then-complete protocol: a caller inserts an
// in-progress row, and the unique index on (application, endpoint, key) is
// what makes the claim exclusive. Only the winner performs the work; everyone
// else either replays the stored response or is told the request is still
// running.
type IdempotencyStore struct {
	db *storage.DB
}

// NewIdempotencyStore builds an IdempotencyStore.
func NewIdempotencyStore(db *storage.DB) *IdempotencyStore {
	return &IdempotencyStore{db: db}
}

// HashRequest fingerprints a request body so a key reused with different
// content can be rejected rather than silently returning the wrong payment.
func HashRequest(body any) (string, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("payment: hash idempotent request: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// Claim attempts to reserve a key.
//
// It returns (nil, nil) when the caller owns the claim and should do the work.
// It returns a completed record when the request was already carried out.
// It returns ErrIdempotencyInProgress when another request holds the claim,
// and ErrIdempotencyConflict when the key was used for a different request.
func (s *IdempotencyStore) Claim(ctx context.Context, applicationID, endpoint, key, requestHash string) (*IdempotencyRecord, error) {
	now := time.Now()
	record := &IdempotencyRecord{
		ID:            ids.New(ids.Request),
		ApplicationID: applicationID,
		Key:           key,
		Endpoint:      endpoint,
		RequestHash:   requestHash,
		State:         idempotencyInProgress,
		ExpiresAt:     now.Add(IdempotencyTTL),
	}

	// An expired claim is replaced rather than blocking the caller forever.
	_, err := s.db.FromContext(ctx).Exec(ctx, `
		INSERT INTO idempotency_keys
			(id, application_id, idempotency_key, endpoint, request_hash, state, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (application_id, endpoint, idempotency_key) DO UPDATE
		SET id           = EXCLUDED.id,
		    request_hash = EXCLUDED.request_hash,
		    state        = EXCLUDED.state,
		    payment_id   = NULL,
		    response_status = NULL,
		    response_body   = NULL,
		    created_at   = now(),
		    completed_at = NULL,
		    expires_at   = EXCLUDED.expires_at
		WHERE idempotency_keys.expires_at < now()`,
		record.ID, applicationID, key, endpoint, requestHash, record.State, record.ExpiresAt,
	)
	if err == nil {
		// The insert or the expired-claim takeover succeeded. A zero-row
		// result means the WHERE clause blocked the takeover, so an existing
		// live claim must be examined.
		existing, lookupErr := s.get(ctx, applicationID, endpoint, key)
		if lookupErr != nil {
			return nil, lookupErr
		}
		if existing.ID == record.ID {
			return nil, nil // this caller owns the claim
		}
		return nil, classifyExisting(existing, requestHash)
	}
	if !storage.IsUniqueViolation(err, ConstraintIdempotencyKey) {
		return nil, fmt.Errorf("payment: claim idempotency key: %w", err)
	}

	existing, lookupErr := s.get(ctx, applicationID, endpoint, key)
	if lookupErr != nil {
		return nil, lookupErr
	}
	return nil, classifyExisting(existing, requestHash)
}

// classifyExisting decides what an existing claim means for a new caller.
func classifyExisting(existing *IdempotencyRecord, requestHash string) error {
	if existing.RequestHash != requestHash {
		return ErrIdempotencyConflict
	}
	if existing.State == idempotencyCompleted {
		return &replayError{record: existing}
	}
	return ErrIdempotencyInProgress
}

// replayError carries a completed record back to the caller. It is an error
// type so a single return path can express "stop, and use this instead".
type replayError struct {
	record *IdempotencyRecord
}

func (e *replayError) Error() string { return "payment: replaying a completed idempotent request" }

// Replay extracts the stored outcome from an error returned by Claim, or nil
// when the error means something else.
func Replay(err error) *IdempotencyRecord {
	var replay *replayError
	if errors.As(err, &replay) {
		return replay.record
	}
	return nil
}

// Complete records the outcome of a claimed request.
func (s *IdempotencyStore) Complete(ctx context.Context, applicationID, endpoint, key, paymentID string, status int, body any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("payment: encode idempotent response: %w", err)
	}
	_, err = s.db.FromContext(ctx).Exec(ctx, `
		UPDATE idempotency_keys
		SET state = $5, payment_id = nullif($6, ''), response_status = $7,
		    response_body = $8, completed_at = now()
		WHERE application_id = $1 AND endpoint = $2 AND idempotency_key = $3 AND state = $4`,
		applicationID, endpoint, key, idempotencyInProgress,
		idempotencyCompleted, paymentID, status, encoded,
	)
	if err != nil {
		return fmt.Errorf("payment: complete idempotency key: %w", err)
	}
	return nil
}

// Release drops a claim whose work failed, so the caller can retry rather than
// being locked out by their own failed attempt.
func (s *IdempotencyStore) Release(ctx context.Context, applicationID, endpoint, key string) error {
	_, err := s.db.FromContext(ctx).Exec(ctx, `
		DELETE FROM idempotency_keys
		WHERE application_id = $1 AND endpoint = $2 AND idempotency_key = $3 AND state = $4`,
		applicationID, endpoint, key, idempotencyInProgress)
	if err != nil {
		return fmt.Errorf("payment: release idempotency key: %w", err)
	}
	return nil
}

// Prune deletes expired keys.
func (s *IdempotencyStore) Prune(ctx context.Context) (int64, error) {
	tag, err := s.db.FromContext(ctx).Exec(ctx,
		`DELETE FROM idempotency_keys WHERE expires_at < now()`)
	if err != nil {
		return 0, fmt.Errorf("payment: prune idempotency keys: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (s *IdempotencyStore) get(ctx context.Context, applicationID, endpoint, key string) (*IdempotencyRecord, error) {
	var (
		record    IdempotencyRecord
		paymentID *string
		status    *int
		body      []byte
	)
	err := s.db.FromContext(ctx).QueryRow(ctx, `
		SELECT id, application_id, idempotency_key, endpoint, request_hash, state,
		       payment_id, response_status, response_body, created_at, completed_at, expires_at
		FROM idempotency_keys
		WHERE application_id = $1 AND endpoint = $2 AND idempotency_key = $3`,
		applicationID, endpoint, key,
	).Scan(
		&record.ID, &record.ApplicationID, &record.Key, &record.Endpoint,
		&record.RequestHash, &record.State, &paymentID, &status, &body,
		&record.CreatedAt, &record.CompletedAt, &record.ExpiresAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("payment: load idempotency key: %w", err)
	}
	if paymentID != nil {
		record.PaymentID = *paymentID
	}
	if status != nil {
		record.ResponseStatus = *status
	}
	record.ResponseBody = body
	return &record, nil
}
