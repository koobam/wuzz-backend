-- The kinds of staff: `front` runs the POS of one outlet, `wuzzer` is the technician who is
-- also the driver (station app). A row here, not a CHECK: a new kind is an INSERT.
-- scoped_to_outlet says whether the role belongs to one outlet (front) or to none (wuzzer).

-- +goose Up
CREATE TABLE staff_roles (
    name             text    PRIMARY KEY,
    scoped_to_outlet boolean NOT NULL
);
INSERT INTO staff_roles (name, scoped_to_outlet) VALUES ('front', true), ('wuzzer', false);

-- +goose Down
DROP TABLE IF EXISTS staff_roles;
