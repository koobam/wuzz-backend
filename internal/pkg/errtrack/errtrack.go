// Package errtrack reports errors to Sentry. It is off unless SENTRY_DSN is set, and
// then every record logged at ERROR becomes a Sentry issue: the services already log
// each unexpected failure that way (a recovered panic, a failed dependency, an
// internal error), so nothing else has to be wired through the handlers.
//
// Only what the code logs is sent. Request bodies, headers and user data never are.
package errtrack

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/koobam/wuzz-backend/internal/pkg/config"
)

// Init starts the Sentry client when a DSN is configured and returns the function
// that flushes pending events; call it on shutdown or the last errors are lost.
func Init(cfg *config.Config) (flush func(context.Context) error, err error) {
	if cfg.Sentry.DSN == "" {
		return func(context.Context) error { return nil }, nil
	}
	err = sentry.Init(sentry.ClientOptions{
		Dsn:              cfg.Sentry.DSN,
		Environment:      cfg.Env,
		Release:          cfg.ServiceName + "@" + cfg.Version,
		ServerName:       cfg.ServiceName,
		AttachStacktrace: true,
		SendDefaultPII:   false,
	})
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) error {
		timeout := 2 * time.Second
		if d, ok := ctx.Deadline(); ok {
			timeout = time.Until(d)
		}
		sentry.Flush(timeout)
		return nil
	}, nil
}

// Wrap sends ERROR records to Sentry as well as to the wrapped logger.
func Wrap(log *slog.Logger) *slog.Logger {
	return slog.New(&handler{next: log.Handler()})
}

type handler struct {
	next  slog.Handler
	attrs []slog.Attr
}

func (h *handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError {
		h.report(r)
	}
	return h.next.Handle(ctx, r)
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &handler{next: h.next.WithAttrs(attrs), attrs: append(append([]slog.Attr(nil), h.attrs...), attrs...)}
}

func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{next: h.next.WithGroup(name), attrs: h.attrs}
}

func (h *handler) report(r slog.Record) {
	var cause error
	extra := map[string]any{}
	collect := func(a slog.Attr) bool {
		if err, ok := a.Value.Any().(error); ok && cause == nil {
			cause = err
			return true
		}
		extra[a.Key] = a.Value.String()
		return true
	}
	for _, a := range h.attrs {
		collect(a)
	}
	r.Attrs(collect)

	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetLevel(sentry.LevelError)
		scope.SetContext("log", sentry.Context{"message": r.Message, "attributes": extra})
		if cause != nil {
			sentry.CaptureException(cause)
			return
		}
		sentry.CaptureException(errors.New(r.Message))
	})
}
