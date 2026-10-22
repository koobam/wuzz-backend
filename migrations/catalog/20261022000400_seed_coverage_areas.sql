-- +goose Up
-- Service areas the app resolves a position into (GET /app/v1/coverage).
-- Coarse boxes sized around the first outlets, not administrative boundaries: replace them
-- with real polygons through the backoffice or a new migration.
--
-- Applied once and never run again: a correction needs its own new migration.
-- Upserts by id, so it also applies cleanly to a database that already has these rows.

INSERT INTO coverage_areas (id, name, geometry, active)
OVERRIDING SYSTEM VALUE VALUES
  (1, 'Jakarta Barat', '{"type":"Polygon","coordinates":[[[106.69,-6.215],[106.8,-6.215],[106.8,-6.11],[106.69,-6.11],[106.69,-6.215]]]}'::jsonb, true),
  (2, 'Jakarta Pusat', '{"type":"Polygon","coordinates":[[[106.8,-6.215],[106.88,-6.215],[106.88,-6.18],[106.8,-6.18],[106.8,-6.215]]]}'::jsonb, true),
  (3, 'Jakarta Utara', '{"type":"Polygon","coordinates":[[[106.8,-6.18],[106.95,-6.18],[106.95,-6.08],[106.8,-6.08],[106.8,-6.18]]]}'::jsonb, true),
  (4, 'Jakarta Selatan', '{"type":"Polygon","coordinates":[[[106.75,-6.37],[106.88,-6.37],[106.88,-6.215],[106.75,-6.215],[106.75,-6.37]]]}'::jsonb, true),
  (5, 'Jakarta Timur', '{"type":"Polygon","coordinates":[[[106.88,-6.37],[106.98,-6.37],[106.98,-6.18],[106.88,-6.18],[106.88,-6.37]]]}'::jsonb, true),
  (6, 'Tangerang', '{"type":"Polygon","coordinates":[[[106.55,-6.3],[106.69,-6.3],[106.69,-6.15],[106.55,-6.15],[106.55,-6.3]]]}'::jsonb, true)
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, geometry = EXCLUDED.geometry, active = true, updated_at = now();
SELECT setval(pg_get_serial_sequence('coverage_areas', 'id'), (SELECT max(id) FROM coverage_areas));

-- +goose Down
DELETE FROM coverage_areas WHERE id IN (1, 2, 3, 4, 5, 6);
