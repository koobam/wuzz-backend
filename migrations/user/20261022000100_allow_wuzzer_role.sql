-- The Wuzzer (the technician who is also the driver) signs in to the station app. Its
-- account carries the role `wuzzer`; the CHECK on users.roles only allowed `admin` and
-- `outlet:<id>`, so it has to learn the new one. The roles ride in the access token's
-- `roles` claim, which is what the /station/v1 API authorises against.

-- +goose Up
ALTER TABLE users
    DROP CONSTRAINT users_roles_valid,
    ADD CONSTRAINT users_roles_valid CHECK (
        roles = '{}' OR array_to_string(roles, ',') ~ '^(admin|wuzzer|outlet:[0-9]+)(,(admin|wuzzer|outlet:[0-9]+))*$'
    );

-- +goose Down
-- Fails while any account still has the wuzzer role: remove it from them first.
ALTER TABLE users
    DROP CONSTRAINT users_roles_valid,
    ADD CONSTRAINT users_roles_valid CHECK (
        roles = '{}' OR array_to_string(roles, ',') ~ '^(admin|outlet:[0-9]+)(,(admin|outlet:[0-9]+))*$'
    );
