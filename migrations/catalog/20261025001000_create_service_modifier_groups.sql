-- Which modifier groups a service carries, in the order they are shown.

-- +goose Up
CREATE TABLE service_modifier_groups (
    service_id        bigint  NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    modifier_group_id bigint  NOT NULL REFERENCES modifier_groups (id) ON DELETE CASCADE,
    sort_index        integer NOT NULL DEFAULT 0,
    PRIMARY KEY (service_id, modifier_group_id)
);

CREATE INDEX service_modifier_groups_group_idx ON service_modifier_groups (modifier_group_id);

-- +goose Down
DROP TABLE IF EXISTS service_modifier_groups;
