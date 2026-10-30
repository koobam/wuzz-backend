-- catalog: a note for staff on a service, the way variant and modifier groups have one ("which of two similar services is
-- this?"). Never shown to customers.

-- +goose Up
ALTER TABLE services
    ADD COLUMN internal_notes text NOT NULL DEFAULT '',
    ADD CONSTRAINT services_internal_notes_len CHECK (char_length(internal_notes) <= 500);

-- +goose Down
ALTER TABLE services
    DROP CONSTRAINT IF EXISTS services_internal_notes_len,
    DROP COLUMN IF EXISTS internal_notes;
