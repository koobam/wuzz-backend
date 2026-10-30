-- +goose Up
-- The seeded services' add-ons as reusable modifier groups. A "single" section of the old menu is a required group of exactly
-- one (min 1, max 1); a "multiple" one is optional with no ceiling. Services that offer the same options share one group.
-- Idempotent: rows are keyed by their ids.

INSERT INTO modifier_groups (id, name, internal_notes, min, max) OVERRIDING SYSTEM VALUE VALUES
  (1, 'Penyelesaian', '', 1, 1),
  (2, 'Pewangi', '', 1, 1),
  (3, 'Layanan Tambahan', '', 0, NULL),
  (4, 'Jenis Kendaraan', '', 1, 1),
  (5, 'Layanan Tambahan (Kendaraan)', '', 0, NULL)
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, internal_notes = EXCLUDED.internal_notes, min = EXCLUDED.min, max = EXCLUDED.max, updated_at = now();
SELECT setval(pg_get_serial_sequence('modifier_groups', 'id'), (SELECT max(id) FROM modifier_groups));

INSERT INTO modifier_options (id, group_id, name, description, price, sort_index) OVERRIDING SYSTEM VALUE VALUES
  (1, 1, 'Cuci + Lipat', 'Dicuci bersih lalu dilipat rapi', 0, 1),
  (2, 1, 'Cuci + Setrika', 'Dicuci bersih, disetrika licin', 3000, 2),
  (3, 2, 'Lavender', 'Aroma tenang dan segar', 0, 1),
  (4, 2, 'Ocean Breeze', 'Aroma laut yang bersih', 2000, 2),
  (5, 3, 'Packing Plastik', 'Dikemas rapi dalam plastik', 3000, 1),
  (6, 3, 'Pisahkan Warna', 'Pakaian putih dan berwarna dicuci terpisah', 4000, 2),
  (7, 4, 'Motor', 'Semua ukuran motor', 0, 1),
  (8, 4, 'Mobil', 'Sedan, SUV, dan MPV', 35000, 2),
  (9, 5, 'Cuci Interior', 'Vakum jok, dashboard, dan karpet dasar', 30000, 1),
  (10, 5, 'Wax & Poles', 'Lapisan wax agar cat mengilap dan terlindungi', 40000, 2),
  (11, 5, 'Cuci Mesin', 'Bersihkan ruang mesin dengan hati-hati', 35000, 3)
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, description = EXCLUDED.description, price = EXCLUDED.price,
  sort_index = EXCLUDED.sort_index, updated_at = now();
SELECT setval(pg_get_serial_sequence('modifier_options', 'id'), (SELECT max(id) FROM modifier_options));

INSERT INTO service_modifier_groups (service_id, modifier_group_id, sort_index) VALUES
  (8, 1, 1),
  (8, 2, 2),
  (8, 3, 3),
  (9, 2, 1),
  (9, 3, 2),
  (10, 2, 1),
  (10, 3, 2),
  (11, 2, 1),
  (11, 3, 2),
  (12, 2, 1),
  (12, 3, 2),
  (13, 2, 1),
  (13, 3, 2),
  (14, 2, 1),
  (14, 3, 2),
  (15, 2, 1),
  (15, 3, 2),
  (17, 2, 1),
  (17, 3, 2),
  (18, 4, 1),
  (18, 5, 2),
  (20, 2, 1),
  (20, 3, 2),
  (21, 2, 1),
  (21, 3, 2)
ON CONFLICT DO NOTHING;

-- +goose Down
DELETE FROM modifier_groups WHERE id BETWEEN 1 AND 5;
