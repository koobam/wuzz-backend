-- Staff roles. A customer has none. `admin` is back-office support staff;
-- `outlet:<id>` is staff of one outlet. The roles ride in the access token's
-- `roles` claim, which is what wuzz-support and the /backoffice and /pos APIs
-- authorise against.

-- +goose Up
ALTER TABLE users
    ADD COLUMN roles text[] NOT NULL DEFAULT '{}',
    ADD CONSTRAINT users_roles_valid CHECK (
        roles = '{}' OR array_to_string(roles, ',') ~ '^(admin|outlet:[0-9]+)(,(admin|outlet:[0-9]+))*$'
    );

-- +goose Down
ALTER TABLE users DROP CONSTRAINT users_roles_valid, DROP COLUMN roles;
