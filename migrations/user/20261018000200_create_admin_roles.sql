-- The kinds of admin. A row here, not a CHECK: a new kind is an INSERT, with no change to a table.
-- Back-office accounts live in `admins` (customers are `users`, outlet and field people are `staff`).

-- +goose Up
CREATE TABLE admin_roles (name text PRIMARY KEY);
INSERT INTO admin_roles (name) VALUES ('super'), ('finance'), ('support');

-- +goose Down
DROP TABLE IF EXISTS admin_roles;
