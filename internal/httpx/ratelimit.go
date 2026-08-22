package httpx

import (
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimiter is a per-key token-bucket limiter with background eviction, so
// a long-running process cannot accumulate unbounded state from one-off
// callers (PRD §72).
type RateLimiter struct {
	limit rate.Limit
	burst int
	ttl   time.Duration

	mu      sync.Mutex
	buckets map[string]*bucket
	now     func() time.Time
}

type bucket struct {
	limiter *rate.Limiter
	seen    time.Time
}

// NewRateLimiter allows perSecond requests per key with the given burst.
func NewRateLimiter(perSecond float64, burst int) *RateLimiter {
	if burst < 1 {
		burst = 1
	}
	return &RateLimiter{
		limit:   rate.Limit(perSecond),
		burst:   burst,
		ttl:     10 * time.Minute,
		buckets: make(map[string]*bucket),
		now:     time.Now,
	}
}

// Allow reports whether a request for key may proceed.
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.now()
	b, ok := rl.buckets[key]
	if !ok {
		b = &bucket{limiter: rate.NewLimiter(rl.limit, rl.burst)}
		rl.buckets[key] = b
		rl.evictLocked(now)
	}
	b.seen = now
	return b.limiter.Allow()
}

// evictLocked drops buckets untouched for longer than the TTL. It runs on
// insert, which keeps the map proportional to the active key set without
// needing a background goroutine.
func (rl *RateLimiter) evictLocked(now time.Time) {
	if len(rl.buckets) < 1024 {
		return
	}
	for key, b := range rl.buckets {
		if now.Sub(b.seen) > rl.ttl {
			delete(rl.buckets, key)
		}
	}
}

// KeyFunc derives the rate-limit key for a request.
type KeyFunc func(*http.Request) string

// Middleware limits requests by the key returned from keyFn. An empty key
// bypasses the limiter, which lets callers exempt trusted paths.
func (rl *RateLimiter) Middleware(keyFn KeyFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFn(r)
			if key != "" && !rl.Allow(key) {
				w.Header().Set("Retry-After", "1")
				Fail(w, r, NewError(http.StatusTooManyRequests, CodeRateLimited,
					"Too many requests. Please retry shortly."))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ByClientIP keys the limiter on the caller's address.
func ByClientIP(r *http.Request) string { return ClientIP(r) }
