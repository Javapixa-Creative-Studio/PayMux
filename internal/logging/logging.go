// Package logging configures PayMux's structured logger and carries it
// through request contexts.
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Options controls logger construction.
type Options struct {
	Level string
	JSON  bool
	// Output defaults to os.Stdout when nil.
	Output io.Writer
}

// New builds a slog.Logger. Unknown levels fall back to info rather than
// failing startup, since a bad log level should never take a service down.
func New(opts Options) *slog.Logger {
	out := opts.Output
	if out == nil {
		out = os.Stdout
	}
	handlerOpts := &slog.HandlerOptions{Level: ParseLevel(opts.Level)}
	var h slog.Handler
	if opts.JSON {
		h = slog.NewJSONHandler(out, handlerOpts)
	} else {
		h = slog.NewTextHandler(out, handlerOpts)
	}
	return slog.New(h)
}

// ParseLevel maps a level name to a slog.Level, defaulting to info.
func ParseLevel(name string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

type contextKey struct{}

// WithLogger returns a context carrying logger.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, contextKey{}, logger)
}

// FromContext returns the logger stored in ctx, or the default logger.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(contextKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

// With returns a context whose logger carries the additional attributes.
func With(ctx context.Context, args ...any) context.Context {
	return WithLogger(ctx, FromContext(ctx).With(args...))
}
