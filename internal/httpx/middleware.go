package httpx

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/ids"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/logging"
)

// HeaderRequestID carries the PayMux request identifier on responses and is
// accepted on requests from trusted upstream proxies.
const HeaderRequestID = "X-Request-Id"

type requestIDKey struct{}

// RequestIDFromContext returns the request identifier bound to ctx.
func RequestIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey{}).(string); ok {
		return id
	}
	return ""
}

// WithRequestID binds a request identifier to ctx.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID assigns every request an identifier (PRD §65) and echoes it back.
//
// An inbound X-Request-Id is honoured only when it already looks like a
// PayMux identifier; otherwise a fresh one is minted, so a client cannot
// inject arbitrary text into logs.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if ids.Validate(ids.Request, id) != nil {
			id = ids.New(ids.Request)
		}
		ctx := WithRequestID(r.Context(), id)
		ctx = logging.With(ctx, "request_id", id)
		w.Header().Set(HeaderRequestID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// responseRecorder captures the status and size of a response for logging.
type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (rec *responseRecorder) WriteHeader(status int) {
	if rec.status == 0 {
		rec.status = status
		rec.ResponseWriter.WriteHeader(status)
	}
}

func (rec *responseRecorder) Write(b []byte) (int, error) {
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	n, err := rec.ResponseWriter.Write(b)
	rec.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (rec *responseRecorder) Unwrap() http.ResponseWriter { return rec.ResponseWriter }

// RequestLogger logs one structured record per completed request.
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &responseRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		logger := logging.FromContext(r.Context())
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration_ms", float64(time.Since(start).Microseconds()) / 1000,
			"remote_ip", ClientIP(r),
		}
		switch {
		case rec.status >= 500:
			logger.Error("request completed", attrs...)
		case rec.status >= 400:
			logger.Info("request completed", attrs...)
		default:
			logger.Info("request completed", attrs...)
		}
	})
}

// Recoverer converts a panic into an opaque 500 rather than a dropped
// connection, and logs the failure with the request identifier attached.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec) // the server's own signal; let it through
				}
				logging.FromContext(r.Context()).Error("panic recovered",
					"panic", rec,
					"stack", stackTrace(),
				)
				Fail(w, r, ErrInternal(nil))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// LimitBody caps the number of bytes a handler will read from a request body
// (PRD §72).
func LimitBody(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}

// SecureHeaders sets conservative security response headers. PayMux's API
// serves JSON only, so a restrictive CSP and nosniff are safe defaults.
func SecureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// CORS allows the dashboard origin to call the admin API with credentials.
// Only configured origins are reflected; "*" disables the allow-list.
func CORS(origins []string) func(http.Handler) http.Handler {
	allowAll := false
	allowed := make(map[string]bool, len(origins))
	for _, o := range origins {
		if o == "*" {
			allowAll = true
		}
		allowed[strings.TrimRight(o, "/")] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimRight(r.Header.Get("Origin"), "/")
			if origin != "" && (allowAll || allowed[origin]) {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Credentials", "true")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, "+HeaderRequestID)
				h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
				h.Set("Access-Control-Max-Age", "600")
				h.Add("Vary", "Origin")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequestRecorder is implemented by the metrics collector. httpx depends on
// this narrow interface rather than the metrics package so the HTTP layer
// stays free of a Prometheus dependency.
type RequestRecorder interface {
	RecordHTTPRequest(method, route string, status int, duration time.Duration)
}

// RouteResolver reports the matched route pattern for a request.
//
// The pattern — not the path — is what a metric can be labelled with: a path
// contains payment identifiers, and one time series per payment would make
// the metric unusable.
type RouteResolver func(*http.Request) string

// Instrument records one metric sample per completed request.
func Instrument(recorder RequestRecorder, route RouteResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &responseRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)

			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			pattern := ""
			if route != nil {
				pattern = route(r)
			}
			recorder.RecordHTTPRequest(r.Method, pattern, status, time.Since(start))
		})
	}
}

// Timeout bounds how long a handler may run.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClientIP reports the best-known client address for logging and rate
// limiting. Proxy headers are consulted only when TrustProxyHeaders is set,
// because a spoofable header must never be able to evade a rate limit.
var TrustProxyHeaders = false

// ClientIP returns the remote address of the request.
func ClientIP(r *http.Request) string {
	if TrustProxyHeaders {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if i := strings.IndexByte(xff, ','); i > 0 {
				return strings.TrimSpace(xff[:i])
			}
			return strings.TrimSpace(xff)
		}
		if ip := r.Header.Get("X-Real-Ip"); ip != "" {
			return strings.TrimSpace(ip)
		}
	}
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i > 0 {
		// Trim the port, keeping bracketed IPv6 literals intact.
		if trimmed := strings.TrimSuffix(strings.TrimPrefix(host[:i], "["), "]"); trimmed != "" {
			return trimmed
		}
	}
	return host
}
