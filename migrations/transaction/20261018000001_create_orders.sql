-- transaction: orders. Owned exclusively by the transaction service.
--
-- `id` is a uuid chosen by the client: the POS creates orders offline and
-- retries until it hears back, so the id is the idempotency key. Lines are a
-- jsonb snapshot: an order keeps the names and prices of the moment it was sold.

-- +goose Up
CREATE TABLE orders (
    id             uuid PRIMARY KEY,
    outlet_id      bigint      NOT NULL,
    customer_name  text        NOT NULL,
    customer_phone text        NOT NULL DEFAULT '',
    lines          jsonb       NOT NULL,
    total          bigint      NOT NULL,
    status         text        NOT NULL DEFAULT 'created',
    created_by     text        NOT NULL,
    version        bigint      NOT NULL DEFAULT 1,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT orders_status_valid CHECK (status IN ('created', 'processing', 'ready', 'fulfilled', 'cancelled')),
    CONSTRAINT orders_total_not_negative CHECK (total >= 0),
    CONSTRAINT orders_customer_not_blank CHECK (btrim(customer_name) <> '')
);

CREATE INDEX orders_outlet_created_idx ON orders (outlet_id, created_at DESC);
CREATE INDEX orders_status_idx ON orders (status, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS orders;
