package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/ids"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/storage"
)

// ConstraintEventDestination is the unique index that makes enqueueing a
// delivery idempotent.
const ConstraintEventDestination = "deliveries_event_destination_key"

// ErrInFlight reports a retry requested while a worker is mid-attempt.
// Requeuing then would produce two concurrent attempts at the same delivery.
var ErrInFlight = errors.New("delivery: this delivery is being attempted right now")

// Repository persists deliveries and doubles as PayMux's job queue.
//
// Using the deliveries table itself as the queue keeps enqueueing
// transactional with the event that caused it: an event and its deliveries
// commit together or not at all, so no event can be published without being
// scheduled for delivery, and none can be scheduled for an event that was
// rolled back (PRD §9, §71).
type Repository struct {
	db *storage.DB
}

// NewRepository builds a Repository.
func NewRepository(db *storage.DB) *Repository { return &Repository{db: db} }

func (r *Repository) q(ctx context.Context) storage.Querier { return r.db.FromContext(ctx) }

const deliveryColumns = `
	id, event_id, application_id, destination_id, url, state,
	attempt_count, max_attempts, next_attempt_at, last_attempt_at,
	last_status_code, last_error, last_duration_ms, locked_at,
	coalesce(locked_by, ''), succeeded_at, created_at, updated_at`

// aliasedDeliveryColumns is the same list qualified for the claim query,
// which joins the table to a subselect and so needs each column disambiguated.
const aliasedDeliveryColumns = `
	d.id, d.event_id, d.application_id, d.destination_id, d.url, d.state,
	d.attempt_count, d.max_attempts, d.next_attempt_at, d.last_attempt_at,
	d.last_status_code, d.last_error, d.last_duration_ms, d.locked_at,
	coalesce(d.locked_by, ''), d.succeeded_at, d.created_at, d.updated_at`

// Enqueue schedules a delivery.
//
// Re-enqueueing the same event and destination is a no-op rather than an
// error: replaying a notification must not produce a second delivery.
func (r *Repository) Enqueue(ctx context.Context, d *Delivery) error {
	if d.ID == "" {
		d.ID = ids.New(ids.Delivery)
	}
	if d.MaxAttempts <= 0 {
		d.MaxAttempts = DefaultMaxAttempts
	}
	if d.NextAttemptAt.IsZero() {
		d.NextAttemptAt = time.Now()
	}

	row := r.q(ctx).QueryRow(ctx, `
		INSERT INTO deliveries
			(id, event_id, application_id, destination_id, url, state, max_attempts, next_attempt_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (event_id, destination_id) DO NOTHING
		RETURNING `+deliveryColumns,
		d.ID, d.EventID, d.ApplicationID, d.DestinationID, d.URL,
		string(StatePending), d.MaxAttempts, d.NextAttemptAt,
	)
	err := scanDelivery(row, d)
	if err == nil {
		return nil
	}
	if storage.IsNotFound(err) {
		// The delivery already existed; load it so the caller still gets one.
		existing, loadErr := r.byEventAndDestination(ctx, d.EventID, d.DestinationID)
		if loadErr != nil {
			return loadErr
		}
		*d = *existing
		return nil
	}
	return err
}

func (r *Repository) byEventAndDestination(ctx context.Context, eventID, destinationID string) (*Delivery, error) {
	var d Delivery
	row := r.q(ctx).QueryRow(ctx,
		`SELECT `+deliveryColumns+` FROM deliveries WHERE event_id = $1 AND destination_id = $2`,
		eventID, destinationID)
	if err := scanDelivery(row, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// Claim reserves up to limit due deliveries for a worker.
//
// FOR UPDATE SKIP LOCKED is what lets several workers share the queue: each
// takes rows no one else holds, so concurrency needs no external coordination
// and a crashed worker blocks nobody (PRD §70).
func (r *Repository) Claim(ctx context.Context, workerID string, limit int) ([]*Delivery, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := r.q(ctx).Query(ctx, `
		WITH due AS (
			SELECT id FROM deliveries
			WHERE state IN ('pending', 'failed')
			  AND next_attempt_at <= now()
			ORDER BY next_attempt_at
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE deliveries d
		SET state = 'delivering', locked_at = now(), locked_by = $1, updated_at = now()
		FROM due
		WHERE d.id = due.id
		RETURNING `+aliasedDeliveryColumns,
		workerID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("delivery: claim: %w", err)
	}
	defer rows.Close()

	var out []*Delivery
	for rows.Next() {
		var d Delivery
		if err := scanDelivery(rows, &d); err != nil {
			return nil, err
		}
		out = append(out, &d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("delivery: claim: %w", err)
	}
	return out, nil
}

// ReleaseStale returns deliveries abandoned by a crashed worker to the queue.
//
// A worker that dies mid-attempt leaves its rows in "delivering" forever;
// without this they would never be retried (PRD §69).
func (r *Repository) ReleaseStale(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := r.q(ctx).Exec(ctx, `
		UPDATE deliveries
		SET state = 'failed', locked_at = NULL, locked_by = NULL,
		    last_error = 'delivery was reclaimed after the worker stopped responding',
		    next_attempt_at = now(), updated_at = now()
		WHERE state = 'delivering' AND locked_at < now() - $1::interval`,
		fmt.Sprintf("%d seconds", int(olderThan.Seconds())))
	if err != nil {
		return 0, fmt.Errorf("delivery: release stale: %w", err)
	}
	return tag.RowsAffected(), nil
}

// RecordSuccess marks a delivery delivered and stores the attempt.
func (r *Repository) RecordSuccess(ctx context.Context, d *Delivery, attempt Attempt) error {
	return r.db.InTx(ctx, func(ctx context.Context, tx storage.Querier) error {
		if err := insertAttempt(ctx, tx, d.ID, attempt); err != nil {
			return err
		}
		row := tx.QueryRow(ctx, `
			UPDATE deliveries SET
				state = 'succeeded', attempt_count = $2, last_attempt_at = now(),
				last_status_code = $3, last_error = '', last_duration_ms = $4,
				locked_at = NULL, locked_by = NULL, succeeded_at = now(), updated_at = now()
			WHERE id = $1
			RETURNING `+deliveryColumns,
			d.ID, attempt.Number, attempt.StatusCode, attempt.DurationMS)
		return scanDelivery(row, d)
	})
}

// RecordFailure stores a failed attempt and schedules the next one, or gives
// up when the attempts are exhausted.
func (r *Repository) RecordFailure(ctx context.Context, d *Delivery, attempt Attempt, retry bool) error {
	state := StateFailed
	nextAttempt := time.Now().Add(RetryDelay(attempt.Number))
	if !retry || attempt.Number >= d.MaxAttempts {
		state = StateDead
		nextAttempt = time.Now()
	}

	return r.db.InTx(ctx, func(ctx context.Context, tx storage.Querier) error {
		if err := insertAttempt(ctx, tx, d.ID, attempt); err != nil {
			return err
		}
		row := tx.QueryRow(ctx, `
			UPDATE deliveries SET
				state = $2, attempt_count = $3, last_attempt_at = now(),
				last_status_code = $4, last_error = $5, last_duration_ms = $6,
				next_attempt_at = $7, locked_at = NULL, locked_by = NULL, updated_at = now()
			WHERE id = $1
			RETURNING `+deliveryColumns,
			d.ID, string(state), attempt.Number, attempt.StatusCode,
			truncate(attempt.Error, 2000), attempt.DurationMS, nextAttempt)
		return scanDelivery(row, d)
	})
}

// Retry requeues a delivery for immediate re-attempt (PRD §47).
//
// The attempt counter is reset so an operator's manual retry gets the full
// schedule again rather than one last try before the delivery dies.
func (r *Repository) Retry(ctx context.Context, id string) (*Delivery, error) {
	var d Delivery
	row := r.q(ctx).QueryRow(ctx, `
		UPDATE deliveries SET
			state = 'pending', attempt_count = 0, next_attempt_at = now(),
			locked_at = NULL, locked_by = NULL, last_error = '', updated_at = now()
		WHERE id = $1 AND state <> 'delivering'
		RETURNING `+deliveryColumns, id)
	if err := scanDelivery(row, &d); err != nil {
		if storage.IsNotFound(err) {
			// The update matched nothing: either there is no such delivery, or
			// a worker holds it. Distinguish the two so the caller can say
			// which it was.
			if _, getErr := r.Get(ctx, id); getErr == nil {
				return nil, ErrInFlight
			}
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return &d, nil
}

// Get loads one delivery.
func (r *Repository) Get(ctx context.Context, id string) (*Delivery, error) {
	var d Delivery
	row := r.q(ctx).QueryRow(ctx, `SELECT `+deliveryColumns+` FROM deliveries WHERE id = $1`, id)
	if err := scanDelivery(row, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// Filter narrows a delivery listing.
type Filter struct {
	ApplicationID string
	EventID       string
	State         State
	DestinationID string
}

// List returns a page of deliveries, newest first.
func (r *Repository) List(ctx context.Context, filter Filter, page storage.Page) (storage.List[*Delivery], error) {
	page = page.Normalize()

	var (
		conditions []string
		args       []any
	)
	add := func(condition string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(condition, len(args)))
	}
	if filter.ApplicationID != "" {
		add("application_id = $%d", filter.ApplicationID)
	}
	if filter.EventID != "" {
		add("event_id = $%d", filter.EventID)
	}
	if filter.State != "" {
		add("state = $%d", string(filter.State))
	}
	if filter.DestinationID != "" {
		add("destination_id = $%d", filter.DestinationID)
	}
	if page.StartingAfter != "" {
		add("id < $%d", page.StartingAfter)
	}

	query := `SELECT ` + deliveryColumns + ` FROM deliveries`
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	args = append(args, page.FetchLimit())
	query += fmt.Sprintf(" ORDER BY id DESC LIMIT $%d", len(args))

	rows, err := r.q(ctx).Query(ctx, query, args...)
	if err != nil {
		return storage.List[*Delivery]{}, fmt.Errorf("delivery: list: %w", err)
	}
	defer rows.Close()

	var out []*Delivery
	for rows.Next() {
		var d Delivery
		if err := scanDelivery(rows, &d); err != nil {
			return storage.List[*Delivery]{}, err
		}
		out = append(out, &d)
	}
	if err := rows.Err(); err != nil {
		return storage.List[*Delivery]{}, fmt.Errorf("delivery: list: %w", err)
	}
	return storage.NewList(out, page), nil
}

// ListAttempts returns a delivery's attempt history, newest first.
func (r *Repository) ListAttempts(ctx context.Context, deliveryID string) ([]*Attempt, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT id, delivery_id, attempt_number, status_code, error, duration_ms, response_body, created_at
		FROM delivery_attempts WHERE delivery_id = $1 ORDER BY attempt_number DESC`, deliveryID)
	if err != nil {
		return nil, fmt.Errorf("delivery: list attempts: %w", err)
	}
	defer rows.Close()

	var out []*Attempt
	for rows.Next() {
		var a Attempt
		if err := rows.Scan(&a.ID, &a.DeliveryID, &a.Number, &a.StatusCode,
			&a.Error, &a.DurationMS, &a.ResponseBody, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("delivery: scan attempt: %w", err)
		}
		out = append(out, &a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("delivery: list attempts: %w", err)
	}
	return out, nil
}

// Stats summarises queue health for the dashboard.
type Stats struct {
	Pending   int64 `json:"pending"`
	Failed    int64 `json:"failed"`
	Succeeded int64 `json:"succeeded"`
	Dead      int64 `json:"dead"`
}

// Stats counts deliveries by state.
func (r *Repository) Stats(ctx context.Context, since time.Time) (Stats, error) {
	var s Stats
	err := r.q(ctx).QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE state IN ('pending', 'delivering')),
			count(*) FILTER (WHERE state = 'failed'),
			count(*) FILTER (WHERE state = 'succeeded' AND created_at >= $1),
			count(*) FILTER (WHERE state = 'dead')
		FROM deliveries`, since,
	).Scan(&s.Pending, &s.Failed, &s.Succeeded, &s.Dead)
	if err != nil {
		return Stats{}, fmt.Errorf("delivery: stats: %w", err)
	}
	return s, nil
}

func insertAttempt(ctx context.Context, tx storage.Querier, deliveryID string, attempt Attempt) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO delivery_attempts
			(id, delivery_id, attempt_number, status_code, error, duration_ms, response_body)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (delivery_id, attempt_number) DO NOTHING`,
		ids.New(ids.Attempt), deliveryID, attempt.Number, attempt.StatusCode,
		truncate(attempt.Error, 2000), attempt.DurationMS, truncate(attempt.ResponseBody, 2000))
	if err != nil {
		return fmt.Errorf("delivery: insert attempt: %w", err)
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanDelivery(row scanner, d *Delivery) error {
	var state string
	err := row.Scan(
		&d.ID, &d.EventID, &d.ApplicationID, &d.DestinationID, &d.URL, &state,
		&d.AttemptCount, &d.MaxAttempts, &d.NextAttemptAt, &d.LastAttemptAt,
		&d.LastStatusCode, &d.LastError, &d.LastDurationMS, &d.LockedAt,
		&d.LockedBy, &d.SucceededAt, &d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return storage.ErrNotFound
		}
		return fmt.Errorf("delivery: scan delivery: %w", err)
	}
	d.State = State(state)
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
