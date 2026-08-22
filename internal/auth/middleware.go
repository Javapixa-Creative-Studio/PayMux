package auth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/anggapixa/paymux/internal/application"
	"github.com/anggapixa/paymux/internal/httpx"
	"github.com/anggapixa/paymux/internal/logging"
)

// SessionCookieName is the dashboard's session cookie.
const SessionCookieName = "paymux_session"

// Middleware authenticates requests for both principal kinds.
type Middleware struct {
	auth *Service
	apps *application.Service
	// SecureCookies marks session cookies Secure. It is on in production and
	// off in development, where the dashboard is served over plain HTTP.
	SecureCookies bool
}

// NewMiddleware builds a Middleware.
func NewMiddleware(authService *Service, apps *application.Service, secureCookies bool) *Middleware {
	return &Middleware{auth: authService, apps: apps, SecureCookies: secureCookies}
}

// RequireAdmin rejects requests without a valid dashboard session.
func (m *Middleware) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(SessionCookieName)
		if err != nil || cookie.Value == "" {
			httpx.Fail(w, r, httpx.ErrUnauthorized("Authentication is required."))
			return
		}
		admin, session, err := m.auth.Authenticate(r.Context(), cookie.Value)
		if err != nil {
			if errors.Is(err, ErrSessionInvalid) {
				m.ClearSessionCookie(w)
				httpx.Fail(w, r, httpx.ErrUnauthorized("Your session has expired. Please sign in again."))
				return
			}
			httpx.Fail(w, r, httpx.ErrInternal(err))
			return
		}

		ctx := WithPrincipal(r.Context(), &Principal{Admin: admin, Session: session})
		ctx = logging.With(ctx, "admin_id", admin.ID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireApplication rejects requests without a usable API key.
//
// The key alone determines which application the request acts as, so no
// handler downstream needs to trust a caller-supplied application id.
func (m *Middleware) RequireApplication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, ok := bearerToken(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="paymux"`)
			httpx.Fail(w, r, httpx.ErrUnauthorized(
				"An API key is required. Send it as: Authorization: Bearer pmx_live_..."))
			return
		}
		auth, err := m.apps.Authenticate(r.Context(), key)
		if err != nil {
			switch {
			case errors.Is(err, application.ErrKeyNotUsable):
				httpx.Fail(w, r, httpx.ErrUnauthorized("The API key is invalid, revoked or expired."))
			case errors.Is(err, application.ErrDisabled):
				httpx.Fail(w, r, httpx.ErrForbidden("This application is disabled."))
			default:
				httpx.Fail(w, r, httpx.ErrInternal(err))
			}
			return
		}

		ctx := WithPrincipal(r.Context(), &Principal{
			Application: auth.Application,
			APIKey:      auth.APIKey,
		})
		ctx = logging.With(ctx,
			"application_id", auth.Application.ID,
			"api_key_id", auth.APIKey.ID,
			"key_mode", string(auth.APIKey.Mode),
		)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// bearerToken extracts a bearer credential from the Authorization header.
func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", false
	}
	scheme, value, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "bearer") {
		return "", false
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	return value, true
}

// SetSessionCookie writes the dashboard session cookie.
//
// HttpOnly keeps the token away from page scripts, and SameSite=Lax stops a
// third-party site from driving the admin API with the operator's session.
func (m *Middleware) SetSessionCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
		HttpOnly: true,
		Secure:   m.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearSessionCookie removes the session cookie.
func (m *Middleware) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   m.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	})
}
