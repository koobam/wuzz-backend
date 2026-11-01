-- Customers sign in with a 6-digit PIN instead of an SMS code. The PIN is kept only as a bcrypt hash; NULL means none set yet.
-- reverify_required marks a customer who must prove the number with a code again before the PIN is trusted.

-- +goose Up
ALTER TABLE users
    ADD COLUMN pin_hash          text NULL,
    ADD COLUMN reverify_required boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE users
    DROP COLUMN reverify_required,
    DROP COLUMN pin_hash;
