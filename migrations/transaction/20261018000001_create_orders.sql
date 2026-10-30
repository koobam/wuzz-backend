-- transaction: orders. Owned exclusively by the transaction service.
--
-- `id` is a uuid chosen by the client: the POS retries until it hears back, so
-- the id is the idempotency key. Lines are a
-- jsonb snapshot: an order keeps the names and prices of the moment it was sold.
--
-- `code` is the name people use for an order: TRX-DDMMYYYY-XXXX, e.g. TRX-25102026-K7M2, something a cashier and a customer can read
-- out and type. The date is the day (Asia/Jakarta) the order was created; the four characters come from an alphabet without 0, O, 1
-- and I. The id stays the key everywhere. The service makes the code when it stores the order (a client may suggest one), so a row never exists without it.

-- +goose Up
CREATE TABLE orders (
    id             uuid PRIMARY KEY,
    code           text        NOT NULL,
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
    CONSTRAINT orders_customer_not_blank CHECK (btrim(customer_name) <> ''),
    CONSTRAINT orders_code_valid CHECK (code ~ '^TRX-[0-9]{8}-[A-HJ-NP-Z2-9]{4}$')
);

CREATE UNIQUE INDEX orders_code_key ON orders (code);

CREATE INDEX orders_outlet_created_idx ON orders (outlet_id, created_at DESC);
CREATE INDEX orders_status_idx ON orders (status, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS orders;
