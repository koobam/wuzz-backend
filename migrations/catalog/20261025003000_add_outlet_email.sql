-- +goose Up
-- The outlet's contact email: optional (empty = none), kept lower-case by the application. The CHECK is the database's own
-- guard against a malformed or oversize address: one "@", no spaces, a dot in the domain, at most 254 characters.
ALTER TABLE outlets
    ADD COLUMN email text NOT NULL DEFAULT '',
    ADD CONSTRAINT outlets_email_format CHECK (email = '' OR (length(email) <= 254 AND email ~* '^[^@\s]+@[^@\s]+\.[^@\s]+$'));

-- +goose Down
ALTER TABLE outlets
    DROP CONSTRAINT outlets_email_format,
    DROP COLUMN email;
