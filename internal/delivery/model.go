// Package delivery sends PayMux events to application webhook destinations
// and owns the durable queue that makes that reliable.
package delivery

import (
	"math/rand"
	"time"
)

// State is where a delivery is in its lifecycle.
type State string

const (
	// StatePending is queued and never attempted.
	StatePending State = "pending"
	// StateDelivering is claimed by a worker right now.
	StateDelivering State = "delivering"
	// StateSucceeded means the destination returned 2xx.
	StateSucceeded State = "succeeded"
	// StateFailed means an attempt failed and another is scheduled.
	StateFailed State = "failed"
	// StateDead means every attempt was used up (PRD §46).
	StateDead State = "dead"
	// StateCanceled means an operator or a deleted destination stopped it.
	StateCanceled State = "canceled"
)

// Terminal reports whether a delivery will not be attempted again on its own.
func (s State) Terminal() bool {
	return s == StateSucceeded || s == StateDead || s == StateCanceled
}

// Delivery is one event addressed to one destination.
type Delivery struct {
	ID             string
	EventID        string
	ApplicationID  string
	DestinationID  string
	URL            string
	State          State
	AttemptCount   int
	MaxAttempts    int
	NextAttemptAt  time.Time
	LastAttemptAt  *time.Time
	LastStatusCode *int
	LastError      string
	LastDurationMS *int
	LockedAt       *time.Time
	LockedBy       string
	SucceededAt    *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Attempt is one HTTP call made for a delivery.
type Attempt struct {
	ID           string
	DeliveryID   string
	Number       int
	StatusCode   *int
	Error        string
	DurationMS   int
	ResponseBody string
	CreatedAt    time.Time
}

// DefaultMaxAttempts is how many times a delivery is tried before it is
// declared dead. With the schedule below that spans just over 31 hours.
const DefaultMaxAttempts = 7

// retryDelays is the backoff between attempts (PRD §46). The first retry is
// quick because most failures are momentary; later ones stretch out so a
// destination that is down for a working day still gets a delivery when it
// returns.
var retryDelays = []time.Duration{
	1 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	1 * time.Hour,
	6 * time.Hour,
	24 * time.Hour,
}

// RetryDelay returns how long to wait before the given attempt number, where
// attempt 1 is the first retry after the initial try.
//
// Jitter of up to ±10% is applied so a destination coming back online is not
// hit by every pending delivery at the same instant (PRD §46).
func RetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	index := attempt - 1
	if index >= len(retryDelays) {
		index = len(retryDelays) - 1
	}
	base := retryDelays[index]
	return applyJitter(base)
}

// jitterFraction is the maximum proportion by which a delay is varied.
const jitterFraction = 0.1

func applyJitter(base time.Duration) time.Duration {
	spread := float64(base) * jitterFraction
	// rand's global source is fine here: this only needs to break up
	// synchronised retries, not resist prediction.
	offset := (rand.Float64()*2 - 1) * spread //nolint:gosec // jitter, not a secret
	delay := time.Duration(float64(base) + offset)
	if delay < time.Second {
		delay = time.Second
	}
	return delay
}

// ShouldRetry reports whether an HTTP status code deserves another attempt.
//
// A 2xx is success. Everything else is retried except the 4xx range, where the
// destination has told PayMux the request itself is unacceptable, with two
// exceptions that are explicitly about timing rather than content.
func ShouldRetry(statusCode int) bool {
	switch {
	case statusCode >= 200 && statusCode < 300:
		return false
	case statusCode == 408, statusCode == 425, statusCode == 429:
		return true
	case statusCode >= 400 && statusCode < 500:
		return false
	default:
		return true
	}
}

// Succeeded reports whether a status code counts as delivered (PRD §45).
func Succeeded(statusCode int) bool {
	return statusCode >= 200 && statusCode < 300
}
