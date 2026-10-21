-- The outbox. Written in the same transaction as the state change, drained to
-- Kafka by the relay inside this service. Every command service has an
-- identical copy: it is per-database by necessity, since the whole point is
-- that it commits atomically with the row next to it.

-- +goose Up
CREATE TABLE outbox (
    id             uuid PRIMARY KEY,
    topic          text        NOT NULL,
    aggregate_id   text        NOT NULL,
    aggregate_type text        NOT NULL,
    event_type     text        NOT NULL,
    payload        jsonb       NOT NULL,
    occurred_at    timestamptz NOT NULL,
    published_at   timestamptz
);

-- The relay only ever asks for unpublished rows, oldest first. A partial index
-- keeps that lookup on a handful of rows even when the table holds months of
-- published history.
CREATE INDEX outbox_unpublished_idx
    ON outbox (occurred_at)
    WHERE published_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS outbox;
