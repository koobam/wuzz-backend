-- +goose Up
-- The seeded groups carried "Dipasang di: <services>." in their internal note. Where a group is used is shown by the groups
-- themselves (the services that link to them), and the sentence goes stale as soon as a service changes, so it is removed.
UPDATE variant_groups SET internal_notes = btrim(regexp_replace(internal_notes, '\s*Dipasang di:.*$', '')) WHERE internal_notes LIKE '%Dipasang di:%';
UPDATE modifier_groups SET internal_notes = btrim(regexp_replace(internal_notes, '\s*Dipasang di:.*$', '')) WHERE internal_notes LIKE '%Dipasang di:%';

-- +goose Down
-- The sentence is derived data that is not restored.
SELECT 1;
