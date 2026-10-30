-- catalog: a day an outlet is open 24 hours.
--
-- open_24h marks a day that is open round the clock (00:00 to 24:00, the whole local day). It carries no times of its own: its
-- open_time and close_time are stored as 00:00 / 00:00, which is how it is read back (the flag, not the times, says "all day").
-- A day is exactly one of: closed (no times), open 24 hours (no times), or a range with an opening and a closing time that differ
-- (a closing time before the opening time means it closes after midnight).

-- +goose Up
ALTER TABLE outlet_hours ADD COLUMN open_24h boolean NOT NULL DEFAULT false;

ALTER TABLE outlet_hours DROP CONSTRAINT outlet_hours_clock;
ALTER TABLE outlet_hours ADD CONSTRAINT outlet_hours_clock CHECK (
    NOT (closed AND open_24h)
    AND (
        closed
        OR open_24h
        OR (open_time ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$' AND close_time ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$' AND open_time <> close_time)
    )
);

-- +goose Down
DELETE FROM outlet_hours WHERE open_24h;
ALTER TABLE outlet_hours DROP CONSTRAINT outlet_hours_clock;
ALTER TABLE outlet_hours ADD CONSTRAINT outlet_hours_clock CHECK (
    closed
    OR (open_time ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$' AND close_time ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$' AND open_time <> close_time)
);
ALTER TABLE outlet_hours DROP COLUMN open_24h;
