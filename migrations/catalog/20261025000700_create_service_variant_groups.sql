-- Which variant groups a service offers, in the order they are shown. Many to many: a group is shared across services.

-- +goose Up
CREATE TABLE service_variant_groups (
    service_id       bigint  NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    variant_group_id bigint  NOT NULL REFERENCES variant_groups (id) ON DELETE CASCADE,
    sort_index       integer NOT NULL DEFAULT 0,
    PRIMARY KEY (service_id, variant_group_id)
);

-- Every service a group is on, for the page that edits the group.
CREATE INDEX service_variant_groups_group_idx ON service_variant_groups (variant_group_id);

-- +goose Down
DROP TABLE IF EXISTS service_variant_groups;
