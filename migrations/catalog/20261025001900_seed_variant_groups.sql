-- +goose Up
-- The seeded services' speed choice (Reguler / Express) as reusable variant groups. Express costs half again as much as
-- Reguler, so the extra differs per price: one group per distinct extra, shared by the services that have it.
-- Idempotent: rows are keyed by their ids.

INSERT INTO variant_groups (id, name, internal_notes, sort_index) OVERRIDING SYSTEM VALUE VALUES
  (1, 'Kecepatan', 'Express +Rp 3.500 per unit.', 1),
  (2, 'Kecepatan', 'Express +Rp 6.000 per unit.', 2),
  (3, 'Kecepatan', 'Express +Rp 9.000 per unit.', 3),
  (4, 'Kecepatan', 'Express +Rp 10.000 per unit.', 4),
  (5, 'Kecepatan', 'Express +Rp 12.500 per unit.', 5),
  (6, 'Kecepatan', 'Express +Rp 14.000 per unit.', 6),
  (7, 'Kecepatan', 'Express +Rp 17.500 per unit.', 7),
  (8, 'Kecepatan', 'Express +Rp 22.500 per unit.', 8)
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, internal_notes = EXCLUDED.internal_notes, sort_index = EXCLUDED.sort_index, updated_at = now();
SELECT setval(pg_get_serial_sequence('variant_groups', 'id'), (SELECT max(id) FROM variant_groups));

INSERT INTO variant_options (id, group_id, name, price_delta, is_default, sort_index, estimated_time, estimated_time_unit) OVERRIDING SYSTEM VALUE VALUES
  (1, 1, 'Reguler', 0, true, 1, 2, 'hari'),
  (2, 1, 'Express', 3500, false, 2, 1, 'hari'),
  (3, 2, 'Reguler', 0, true, 1, 2, 'hari'),
  (4, 2, 'Express', 6000, false, 2, 1, 'hari'),
  (5, 3, 'Reguler', 0, true, 1, 2, 'hari'),
  (6, 3, 'Express', 9000, false, 2, 1, 'hari'),
  (7, 4, 'Reguler', 0, true, 1, 2, 'hari'),
  (8, 4, 'Express', 10000, false, 2, 1, 'hari'),
  (9, 5, 'Reguler', 0, true, 1, 2, 'hari'),
  (10, 5, 'Express', 12500, false, 2, 1, 'hari'),
  (11, 6, 'Reguler', 0, true, 1, 2, 'hari'),
  (12, 6, 'Express', 14000, false, 2, 1, 'hari'),
  (13, 7, 'Reguler', 0, true, 1, 2, 'hari'),
  (14, 7, 'Express', 17500, false, 2, 1, 'hari'),
  (15, 8, 'Reguler', 0, true, 1, 2, 'hari'),
  (16, 8, 'Express', 22500, false, 2, 1, 'hari')
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, price_delta = EXCLUDED.price_delta, is_default = EXCLUDED.is_default,
  sort_index = EXCLUDED.sort_index, estimated_time = EXCLUDED.estimated_time, estimated_time_unit = EXCLUDED.estimated_time_unit, updated_at = now();
SELECT setval(pg_get_serial_sequence('variant_options', 'id'), (SELECT max(id) FROM variant_options));

INSERT INTO service_variant_groups (service_id, variant_group_id, sort_index) VALUES
  (8, 1, 1),
  (9, 2, 1),
  (10, 6, 1),
  (11, 7, 1),
  (12, 8, 1),
  (13, 4, 1),
  (14, 7, 1),
  (15, 5, 1),
  (17, 3, 1),
  (18, 5, 1),
  (20, 8, 1),
  (21, 2, 1)
ON CONFLICT DO NOTHING;

-- +goose Down
DELETE FROM variant_groups WHERE id BETWEEN 1 AND 8;
