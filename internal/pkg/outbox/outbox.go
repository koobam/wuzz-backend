// Package outbox implements the transactional outbox.
//
// The problem it solves: a command must both change the database and publish an
// event, and there is no transaction spanning Postgres and Kafka. Writing the
// event to an outbox table inside the same transaction as the state change
// makes the pair atomic; a relay then moves rows to Kafka afterwards.
//
// Publishing directly from the handler instead would produce, on any crash
// between the two calls, either an order nobody was told about or an event for
// an order that does not exist.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/koobam/wuzz-backend/internal/pkg/config"
	"github.com/koobam/wuzz-backend/internal/pkg/events"
	"github.com/koobam/wuzz-backend/internal/pkg/observability"
	"github.com/koobam/wuzz-backend/internal/platform/postgres"
)

// Publisher is the Kafka side, kept as an interface so the relay can be tested
// without a broker.
type Publisher interface {
	Publish(ctx context.Context, topic, key string, payload []byte) error
}

// Append writes one event inside the caller's transaction. It must be called
// with the same tx that changed the aggregate — that is the whole point.
func Append(ctx context.Context, tx pgx.Tx, envelope events.Envelope) error {
	const query = `
		INSERT INTO outbox (id, topic, aggregate_id, aggregate_type, event_type, payload, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`

	payload, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("outbox: marshal envelope: %w", err)
	}

	_, err = tx.Exec(ctx, query,
		envelope.ID,
		events.Topic(envelope.AggregateType),
		envelope.AggregateID,
		envelope.AggregateType,
		envelope.Type,
		payload,
		envelope.OccurredAt,
	)
	if err != nil {
		return fmt.Errorf("outbox: append %s: %w", envelope.Type, err)
	}

	// Wakes the relay immediately instead of leaving this row to wait for its
	// next poll (up to OutboxInterval away — the single biggest contributor to
	// "the notification/live feed arrived late" for an otherwise-healthy
	// system). Postgres only delivers a NOTIFY to listeners once this
	// transaction actually commits, so a listener never sees "something
	// changed" before it could see what changed; a rollback here means no
	// notification goes out either, same as the row itself.
	if _, err := tx.Exec(ctx, "NOTIFY outbox_new"); err != nil {
		return fmt.Errorf("outbox: notify: %w", err)
	}
	return nil
}

// Relay drains the outbox into Kafka. One runs inside every command service.
type Relay struct {
	pool      *postgres.Pool
	publisher Publisher
	log       *slog.Logger
	interval  time.Duration
	batch     int
}

func NewRelay(pool *postgres.Pool, publisher Publisher, cfg config.Kafka, log *slog.Logger) *Relay {
	return &Relay{
		pool:      pool,
		publisher: publisher,
		log:       log,
		interval:  cfg.OutboxInterval,
		batch:     cfg.OutboxBatch,
	}
}

func (r *Relay) Run(ctx context.Context) error {
	r.log.Info("outbox relay started", slog.Duration("interval", r.interval))

	// LISTEN is the fast path — woken the instant Append's own NOTIFY commits,
	// rather than waiting for the timer below. Best-effort: a pool that cannot
	// spare the one dedicated connection this needs (or a connection that later
	// drops) just means every row waits for the timer instead, the same
	// behaviour this relay already had.
	notified, stopListening, err := r.listen(ctx)
	if err != nil {
		r.log.WarnContext(ctx, "outbox: LISTEN unavailable, polling only", slog.Any("error", err))
		notified = nil
	} else {
		defer stopListening()
	}

	// A plain Ticker would have every replica of every command service sweep
	// its outbox in the same instant: pods start together during a rollout,
	// and a ticker never drifts far enough to separate them again. The result
	// is a spike of identical queries every interval instead of steady load.
	// Kept as the backstop even with LISTEN working — a row whose NOTIFY this
	// process missed (the listen connection dropped and had not reconnected
	// yet) still gets picked up, just on the next tick instead of instantly.
	//
	// The first wait is a random fraction of the interval, which spreads the
	// replicas apart at boot; each wait after that is jittered ±20% to keep
	// them apart.
	timer := time.NewTimer(time.Duration(rand.Float64() * float64(r.interval)))
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-notified:
			if err := r.drain(ctx); err != nil && ctx.Err() == nil {
				r.log.ErrorContext(ctx, "outbox drain failed", slog.Any("error", err))
			}
		case <-timer.C:
			if err := r.drain(ctx); err != nil && ctx.Err() == nil {
				r.log.ErrorContext(ctx, "outbox drain failed", slog.Any("error", err))
			}
			timer.Reset(r.nextInterval())
		}
	}
}

// listen holds one dedicated connection LISTENing for Append's own NOTIFY,
// for the lifetime of ctx. A pooled connection cannot be used for this: LISTEN
// only has an effect on the connection that issued it, and the pool would
// hand that same connection to some unrelated query the moment this returned
// it — so this one is held out of the pool entirely until ctx ends.
func (r *Relay) listen(ctx context.Context) (<-chan struct{}, func(), error) {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("outbox: acquire listen connection: %w", err)
	}
	if _, err := conn.Exec(ctx, "LISTEN outbox_new"); err != nil {
		conn.Release()
		return nil, nil, fmt.Errorf("outbox: listen: %w", err)
	}

	out := make(chan struct{}, 1)
	listenCtx, cancel := context.WithCancel(ctx)
	go func() {
		defer conn.Release()
		for {
			if _, err := conn.Conn().WaitForNotification(listenCtx); err != nil {
				if listenCtx.Err() == nil {
					r.log.WarnContext(ctx, "outbox: LISTEN connection lost, falling back to polling only",
						slog.Any("error", err))
				}
				return
			}
			select {
			case out <- struct{}{}:
			default:
				// A drain is already queued for this wake-up; another row
				// notifying before it runs changes nothing that drain's own
				// batch loop (see drain's comment) would not already catch.
			}
		}
	}()
	return out, cancel, nil
}

// nextInterval returns the configured interval scattered by ±20%.
func (r *Relay) nextInterval() time.Duration {
	return time.Duration(float64(r.interval) * (0.8 + 0.4*rand.Float64()))
}

// drain publishes one batch. It keeps going until a pass returns fewer rows
// than the batch size, so a backlog is cleared at full speed rather than at one
// batch per tick.
func (r *Relay) drain(ctx context.Context) error {
	for {
		published, err := r.drainOnce(ctx)
		if err != nil {
			return err
		}
		if published < r.batch {
			return nil
		}
	}
}

func (r *Relay) drainOnce(ctx context.Context) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("outbox: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// FOR UPDATE SKIP LOCKED is what lets every replica run a relay: each pass
	// grabs a disjoint set of rows instead of all of them fighting over the
	// oldest one.
	const query = `
		SELECT id, topic, aggregate_id, payload
		FROM outbox
		WHERE published_at IS NULL
		ORDER BY occurred_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED`

	rows, err := tx.Query(ctx, query, r.batch)
	if err != nil {
		return 0, fmt.Errorf("outbox: select: %w", err)
	}

	type pending struct {
		id, topic, aggregateID string
		payload                []byte
	}
	var batch []pending

	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.topic, &p.aggregateID, &p.payload); err != nil {
			rows.Close()
			return 0, fmt.Errorf("outbox: scan: %w", err)
		}
		batch = append(batch, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("outbox: iterate: %w", err)
	}
	if len(batch) == 0 {
		observability.OutboxPending.Set(0)
		return 0, nil
	}

	ids := make([]string, 0, len(batch))
	for _, p := range batch {
		// Keyed by aggregate id so every event about one order lands on one
		// partition and is consumed in the order it was written.
		if err := r.publisher.Publish(ctx, p.topic, p.aggregateID, p.payload); err != nil {
			// Stop at the first failure and commit what already went out.
			// Continuing would publish later events before earlier ones.
			//
			// Logged, not returned: the rows stay unpublished and the next tick
			// retries them, which is the designed behaviour. Swallowing it
			// silently is not — a relay failing every pass then looks exactly
			// like an idle one, and the backlog is invisible until someone
			// notices a projection has stopped moving.
			r.log.ErrorContext(ctx, "outbox publish failed",
				slog.String("topic", p.topic),
				slog.String("event_id", p.id),
				slog.Any("error", err))
			break
		}
		ids = append(ids, p.id)
	}

	if len(ids) > 0 {
		// Marking rather than deleting: the row is the audit trail of what was
		// published and when. A retention job trims it, not the hot path.
		if _, err := tx.Exec(ctx, `UPDATE outbox SET published_at = now() WHERE id = ANY($1)`, ids); err != nil {
			return 0, fmt.Errorf("outbox: mark published: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		// Nothing is lost: the rows stay unpublished and are retried. The cost
		// is a duplicate delivery, which the projections already handle.
		return 0, fmt.Errorf("outbox: commit: %w", err)
	}

	observability.OutboxPending.Set(float64(len(batch) - len(ids)))
	return len(ids), nil
}
