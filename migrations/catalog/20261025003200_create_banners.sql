-- catalog: the promotional banners of the customer app.
--
-- A banner is a picture with a title, an optional schedule and an optional place to go when it is tapped. Whether it is on a
-- customer's phone right now is never stored: it is derived from the status and the clock (status = 'active' and now inside
-- [starts_at, ends_at), an absent end being open). sort_index is the carousel's own order, written only by the reorder call.
--
-- target_value depends on target_type: a service id or an outlet id (digits), a screen key (the closed set the app routes), an
-- https address for 'external', and empty for 'none'. It has no foreign key (it is one column for four kinds of target), so the
-- service and outlet deletes check it themselves.
--
-- banner_outlets limits a banner to some outlets; no rows means every outlet. The reference to outlets is RESTRICT on purpose:
-- if the last outlet of a banner went away the banner would silently turn into one for every outlet.

-- +goose Up
CREATE TABLE banners (
    id             bigserial PRIMARY KEY,
    title          text        NOT NULL,
    internal_notes text        NOT NULL DEFAULT '',
    image_url      text        NOT NULL DEFAULT '',
    target_type    text        NOT NULL DEFAULT 'none',
    target_value   text        NOT NULL DEFAULT '',
    status         text        NOT NULL DEFAULT 'inactive',
    sort_index     integer     NOT NULL DEFAULT 0,
    starts_at      timestamptz,
    ends_at        timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT banners_title CHECK (char_length(title) BETWEEN 1 AND 120),
    CONSTRAINT banners_notes CHECK (char_length(internal_notes) <= 500),
    CONSTRAINT banners_target_type CHECK (target_type IN ('none', 'service', 'outlet', 'screen', 'external')),
    CONSTRAINT banners_target_value CHECK ((target_type = 'none') = (target_value = '')),
    CONSTRAINT banners_status CHECK (status IN ('active', 'inactive')),
    CONSTRAINT banners_active_has_image CHECK (status <> 'active' OR image_url <> ''),
    CONSTRAINT banners_window CHECK (starts_at IS NULL OR ends_at IS NULL OR ends_at > starts_at)
);
CREATE INDEX banners_order ON banners (sort_index, id);

CREATE TABLE banner_outlets (
    banner_id bigint NOT NULL REFERENCES banners (id) ON DELETE CASCADE,
    outlet_id bigint NOT NULL REFERENCES outlets (id) ON DELETE RESTRICT,
    PRIMARY KEY (banner_id, outlet_id)
);
CREATE INDEX banner_outlets_outlet ON banner_outlets (outlet_id);

-- +goose Down
DROP TABLE IF EXISTS banner_outlets;
DROP TABLE IF EXISTS banners;
