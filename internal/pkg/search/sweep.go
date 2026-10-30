package search

import (
	"context"
	"log/slog"
	"time"
)

// WaitReady blocks until the node answers, retrying for up to wait; a service that is configured for search starts only once it is there.
func (c *Client) WaitReady(ctx context.Context, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		err := c.Health(ctx)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// Every runs fn at each interval until ctx ends, logging (never stopping on) a failure. An interval of 0 or less runs nothing.
func Every(ctx context.Context, interval time.Duration, log *slog.Logger, name string, fn func(context.Context) error) {
	if interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := fn(ctx); err != nil {
				log.Error("search sweep failed", slog.String("sweep", name), slog.Any("error", err))
			}
		}
	}
}
