-- catalog: vouchers, the money-off coupons of the customer app, and who holds them.
--
-- A voucher takes a percentage or a fixed rupiah amount off an order. With a code it must be claimed (typed in the app) or pushed into
-- a wallet by the back office; with no code it is public and is in every wallet while it is live. Whether it is live is never stored:
-- it is derived from the status and the clock (status = 'active' and now inside [starts_at, ends_at), an absent end being open).
-- All money is whole rupiah in a bigint; value is 1..100 for a percentage and rupiah for an amount.
--
-- voucher_outlets and voucher_services limit where a voucher can be spent; no rows means every outlet / every service. Both references
-- are RESTRICT on purpose: if the last outlet of a voucher went away it would silently turn into one for every outlet, so the outlet and
-- service deletes check these tables themselves and say which vouchers still need them.
--
-- voucher_grants is a customer's copy of a voucher: put there by a claim or an injection, and the customer's remaining uses. The customer
-- belongs to the user service's database, so customer_id has no foreign key; name and phone are kept as they were when it was granted,
-- which is what lets the back office search who holds a voucher without asking the user service.
--
-- voucher_injections is the audit of one bulk send. It keeps the voucher's title and code as they were and has no foreign key, so the
-- record outlives the voucher.

-- +goose Up
CREATE TABLE vouchers (
    id             bigserial PRIMARY KEY,
    code           text        NOT NULL DEFAULT '',
    title          text        NOT NULL,
    description    text        NOT NULL DEFAULT '',
    internal_notes text        NOT NULL DEFAULT '',
    discount_type  text        NOT NULL,
    value          bigint      NOT NULL,
    max_discount   bigint      NOT NULL DEFAULT 0,
    min_spend      bigint      NOT NULL DEFAULT 0,
    max_claims     integer     NOT NULL DEFAULT 0,
    usage_limit    integer     NOT NULL DEFAULT 1,
    status         text        NOT NULL DEFAULT 'inactive',
    color          text        NOT NULL,
    image_url      text        NOT NULL DEFAULT '',
    starts_at      timestamptz,
    ends_at        timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT vouchers_code CHECK (code = '' OR code ~ '^[A-Z0-9]{4,20}$'),
    CONSTRAINT vouchers_title CHECK (char_length(title) BETWEEN 1 AND 120),
    CONSTRAINT vouchers_description CHECK (char_length(description) <= 500),
    CONSTRAINT vouchers_notes CHECK (char_length(internal_notes) <= 500),
    CONSTRAINT vouchers_discount_type CHECK (discount_type IN ('percentage', 'amount')),
    CONSTRAINT vouchers_value CHECK (value >= 1 AND (discount_type <> 'percentage' OR value <= 100) AND value <= 1000000000),
    CONSTRAINT vouchers_max_discount CHECK (max_discount >= 0 AND (discount_type = 'percentage' OR max_discount = 0)),
    CONSTRAINT vouchers_min_spend CHECK (min_spend >= 0),
    CONSTRAINT vouchers_max_claims CHECK (max_claims >= 0 AND (code <> '' OR max_claims = 0)),
    CONSTRAINT vouchers_usage_limit CHECK (usage_limit >= 1),
    CONSTRAINT vouchers_status CHECK (status IN ('active', 'inactive')),
    CONSTRAINT vouchers_color CHECK (color ~ '^#[0-9A-F]{6}$'),
    CONSTRAINT vouchers_window CHECK (starts_at IS NULL OR ends_at IS NULL OR ends_at > starts_at)
);
CREATE UNIQUE INDEX vouchers_code_key ON vouchers (code) WHERE code <> '';

CREATE TABLE voucher_outlets (
    voucher_id bigint NOT NULL REFERENCES vouchers (id) ON DELETE CASCADE,
    outlet_id  bigint NOT NULL REFERENCES outlets (id) ON DELETE RESTRICT,
    PRIMARY KEY (voucher_id, outlet_id)
);
CREATE INDEX voucher_outlets_outlet ON voucher_outlets (outlet_id);

CREATE TABLE voucher_services (
    voucher_id bigint NOT NULL REFERENCES vouchers (id) ON DELETE CASCADE,
    service_id bigint NOT NULL REFERENCES services (id) ON DELETE RESTRICT,
    PRIMARY KEY (voucher_id, service_id)
);
CREATE INDEX voucher_services_service ON voucher_services (service_id);

CREATE TABLE voucher_grants (
    id             bigserial PRIMARY KEY,
    voucher_id     bigint      NOT NULL REFERENCES vouchers (id) ON DELETE RESTRICT,
    customer_id    bigint      NOT NULL,
    customer_name  text        NOT NULL DEFAULT '',
    customer_phone text        NOT NULL DEFAULT '',
    source         text        NOT NULL,
    granted_by     text        NOT NULL DEFAULT '',
    granted_at     timestamptz NOT NULL DEFAULT now(),
    expires_at     timestamptz,
    uses_remaining integer     NOT NULL,
    CONSTRAINT voucher_grants_source CHECK (source IN ('claim', 'inject')),
    CONSTRAINT voucher_grants_uses CHECK (uses_remaining >= 0),
    CONSTRAINT voucher_grants_once UNIQUE (voucher_id, customer_id)
);
CREATE INDEX voucher_grants_customer ON voucher_grants (customer_id, granted_at DESC);

CREATE TABLE voucher_injections (
    id                  bigserial PRIMARY KEY,
    voucher_id          bigint      NOT NULL,
    voucher_title       text        NOT NULL,
    voucher_code        text        NOT NULL DEFAULT '',
    actor_id            text        NOT NULL,
    actor_name          text        NOT NULL DEFAULT '',
    target_count        integer     NOT NULL,
    injected_count      integer     NOT NULL,
    skipped_already_held integer    NOT NULL DEFAULT 0,
    skipped_over_limit  integer     NOT NULL DEFAULT 0,
    skipped_not_found   integer     NOT NULL DEFAULT 0,
    skipped_inactive    integer     NOT NULL DEFAULT 0,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT voucher_injections_counts CHECK (
        injected_count >= 0 AND target_count = injected_count + skipped_already_held + skipped_over_limit + skipped_not_found + skipped_inactive)
);
CREATE INDEX voucher_injections_voucher ON voucher_injections (voucher_id, id DESC);
CREATE INDEX voucher_injections_recent ON voucher_injections (id DESC);

-- +goose Down
DROP TABLE IF EXISTS voucher_injections;
DROP TABLE IF EXISTS voucher_grants;
DROP TABLE IF EXISTS voucher_services;
DROP TABLE IF EXISTS voucher_outlets;
DROP TABLE IF EXISTS vouchers;
