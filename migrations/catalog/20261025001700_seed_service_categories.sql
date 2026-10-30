-- +goose Up
-- The categories services are listed under, and the icon each is drawn with.
INSERT INTO service_categories (name, icon, sort_order) VALUES
  ('Pakaian', 'shirt', 1),
  ('Self Service', 'washing-machine', 2),
  ('Rumah Tangga', 'house', 3),
  ('Sepatu & Tas', 'footprints', 4),
  ('Kendaraan', 'car', 5)
ON CONFLICT (lower(name)) DO NOTHING;

-- +goose Down
DELETE FROM service_categories WHERE name IN ('Pakaian', 'Self Service', 'Rumah Tangga', 'Sepatu & Tas', 'Kendaraan');
