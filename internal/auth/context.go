package auth

import (
	"context"

	"github.com/anggapixa/paymux/internal/application"
)

// Principal is whoever a request is acting as.
//
// Handlers read ownership from here and never from the request body: an
// application id supplied by a caller is not evidence of anything (PRD §91
// rule 13).
type Principal struct {
	Admin       *Admin
	Session     *Session
	Application *application.Application
	APIKey      *application.APIKey
}

// IsAdmin reports whether the request is authenticated as an administrator.
func (p *Principal) IsAdmin() bool { return p != nil && p.Admin != nil }

// IsApplication reports whether the request is authenticated with an API key.
func (p *Principal) IsApplication() bool { return p != nil && p.Application != nil }

// ApplicationID returns the owning application's identifier, or "" for an
// administrator.
func (p *Principal) ApplicationID() string {
	if p == nil || p.Application == nil {
		return ""
	}
	return p.Application.ID
}

// ActorType and ActorID describe the principal for audit logging.
func (p *Principal) ActorType() string {
	switch {
	case p.IsAdmin():
		return "admin"
	case p.IsApplication():
		return "application"
	default:
		return "system"
	}
}

// ActorID returns the identifier of the acting principal.
func (p *Principal) ActorID() string {
	switch {
	case p.IsAdmin():
		return p.Admin.ID
	case p.IsApplication():
		return p.Application.ID
	default:
		return ""
	}
}

type principalKey struct{}

// WithPrincipal binds a principal to ctx.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// FromContext returns the principal bound to ctx, or nil.
func FromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}

// AdminFromContext returns the administrator bound to ctx, or nil.
func AdminFromContext(ctx context.Context) *Admin {
	return FromContext(ctx).admin()
}

func (p *Principal) admin() *Admin {
	if p == nil {
		return nil
	}
	return p.Admin
}

// ApplicationFromContext returns the application bound to ctx, or nil.
func ApplicationFromContext(ctx context.Context) *application.Application {
	p := FromContext(ctx)
	if p == nil {
		return nil
	}
	return p.Application
}

// APIKeyFromContext returns the API key that authenticated the request.
func APIKeyFromContext(ctx context.Context) *application.APIKey {
	p := FromContext(ctx)
	if p == nil {
		return nil
	}
	return p.APIKey
}
