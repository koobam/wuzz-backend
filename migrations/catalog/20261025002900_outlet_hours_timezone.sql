-- catalog: opening hours per day of the week, and the time zone an outlet's clock is in.
--
-- weekday: 0 = Monday ... 6 = Sunday (ISO 8601 numbering minus one). A day is either closed or has an opening and a closing time
-- written as HH:MM on the outlet's own clock; a closing time before the opening time means it closes after midnight and the
-- hours spill into the next day. The old outlets.open_time / close_time columns stay (the customer app's discovery read them
-- and migrations never drop columns); they are no longer read, and new writes keep them at the first open day's hours.

-- +goose Up
ALTER TABLE outlets
    ADD COLUMN timezone text NOT NULL DEFAULT 'Asia/Jakarta',
    ADD CONSTRAINT outlets_timezone_allowed CHECK (timezone IN ('Asia/Jakarta', 'Asia/Makassar', 'Asia/Jayapura'));

CREATE TABLE outlet_hours (
    outlet_id  bigint   NOT NULL REFERENCES outlets (id) ON DELETE CASCADE,
    weekday    smallint NOT NULL CHECK (weekday BETWEEN 0 AND 6),
    closed     boolean  NOT NULL DEFAULT false,
    open_time  text     NOT NULL DEFAULT '',
    close_time text     NOT NULL DEFAULT '',
    PRIMARY KEY (outlet_id, weekday),
    CONSTRAINT outlet_hours_clock CHECK (
        closed
        OR (open_time ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$' AND close_time ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$' AND open_time <> close_time)
    )
);

-- Every outlet that exists keeps the hours it has: the same range on all seven days, never closed.
INSERT INTO outlet_hours (outlet_id, weekday, closed, open_time, close_time)
SELECT o.id, d.weekday, false, o.open_time, o.close_time
FROM outlets o CROSS JOIN generate_series(0, 6) AS d (weekday)
ON CONFLICT DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS outlet_hours;
ALTER TABLE outlets
    DROP CONSTRAINT IF EXISTS outlets_timezone_allowed,
    DROP COLUMN IF EXISTS timezone;
