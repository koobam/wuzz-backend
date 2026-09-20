// Package run starts every long-running component of a service and shuts
// them all down in order when the process receives a signal or any one of
// them fails.
package run

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// Component is one thing a service runs for its whole lifetime: an HTTP
// server, a gRPC server, a background relay. Start blocks until the
// component stops on its own or Stop is called; Stop asks it to shut down.
type Component struct {
	Name  string
	Start func() error
	Stop  func(context.Context) error
}

// Noop is a Start that returns immediately — for components that only need a
// Stop (closing a connection pool, for instance).
func Noop() error { return nil }

// Run starts every component, blocks until an OS signal or a component
// failure, then stops them all in reverse order within shutdownTimeout.
func Run(ctx context.Context, log *slog.Logger, shutdownTimeout time.Duration, components ...Component) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Only a real failure goes on this channel. A nil return from Start is
	// not one: for a one-shot component (Noop) it is the expected, immediate
	// result, and for a long-running server it only happens once Stop has
	// already been called elsewhere — either way it must not itself trigger
	// the shutdown this select is waiting for.
	errs := make(chan error, len(components))
	for _, c := range components {
		c := c
		go func() {
			if err := c.Start(); err != nil {
				log.Error("component failed", slog.String("component", c.Name), slog.Any("error", err))
				errs <- err
			}
		}()
	}

	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case err := <-errs:
		log.Error("shutting down after component failure", slog.Any("error", err))
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	var wg sync.WaitGroup
	for i := len(components) - 1; i >= 0; i-- {
		c := components[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			if c.Stop == nil {
				return
			}
			if err := c.Stop(shutdownCtx); err != nil {
				log.Error("component stop failed", slog.String("component", c.Name), slog.Any("error", err))
			}
		}()
	}
	wg.Wait()

	return nil
}
