-- +goose Up
-- Outlets. Id 1 is the one the POS has always had; 2-7 are the first outlets the app lists.
--
-- Applied once and never run again: a correction needs its own new migration.
-- Upserts by id, so it also applies cleanly to a database that already has these rows.

INSERT INTO outlets (id, name, address, phone, latitude, longitude, category, images, current_status, delivery_types, open_time, close_time, active)
OVERRIDING SYSTEM VALUE VALUES
  (1, 'Wuzz Pusat', 'Jl. Contoh No. 1, Jakarta', '0211234567', -6.2088, 106.8456, 'all', ARRAY['https://images.unsplash.com/photo-1442512595331-e89e73853f31?w=800']::text[], 'moderate', ARRAY['pickup','delivery']::text[], '08:00', '22:00', true),
  (2, 'Wuzz Station Blok M', 'Jl. Melawai Raya, Blok M, Jakarta Selatan', '', -6.244, 106.7994, 'all', ARRAY['https://images.unsplash.com/photo-1509042239860-f550ce710b93?w=800']::text[], 'moderate', ARRAY['pickup','delivery']::text[], '08:00', '22:00', true),
  (3, 'Wuzz Sudirman', 'Jl. Jend. Sudirman Kav. 52, Jakarta Selatan', '', -6.225, 106.809, 'laundry', ARRAY['https://images.unsplash.com/photo-1514432324607-a09d9b4aefdd?w=800']::text[], 'busy', ARRAY['pickup']::text[], '07:00', '21:00', true),
  (4, 'Wuzz Kemang', 'Jl. Kemang Raya No. 8, Jakarta Selatan', '', -6.2615, 106.8135, 'caffe', ARRAY['https://images.unsplash.com/photo-1442512595331-e89e73853f31?w=800']::text[], 'quiet', ARRAY['pickup','delivery']::text[], '09:00', '23:00', true),
  (5, 'Wuzz Kelapa Gading', 'Jl. Boulevard Raya, Kelapa Gading, Jakarta Utara', '', -6.158, 106.908, 'all', ARRAY['https://images.unsplash.com/photo-1509042239860-f550ce710b93?w=800']::text[], 'quiet', ARRAY['pickup','delivery']::text[], '08:00', '22:00', true),
  (6, 'Wuzz Supermall Karawaci', 'Jl. Boulevard Diponegoro, Karawaci, Tangerang', '', -6.227, 106.609, 'laundry', ARRAY['https://images.unsplash.com/photo-1514432324607-a09d9b4aefdd?w=800']::text[], 'moderate', ARRAY['delivery']::text[], '10:00', '22:00', true),
  (7, 'Wuzz Puri Indah', 'Jl. Puri Indah Raya, Kembangan, Jakarta Barat', '', -6.188, 106.737, 'all', ARRAY['https://images.unsplash.com/photo-1442512595331-e89e73853f31?w=800']::text[], 'quiet', ARRAY['pickup','delivery']::text[], '09:00', '22:00', true)
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, address = EXCLUDED.address, latitude = EXCLUDED.latitude,
  longitude = EXCLUDED.longitude, category = EXCLUDED.category, images = EXCLUDED.images,
  current_status = EXCLUDED.current_status, delivery_types = EXCLUDED.delivery_types,
  open_time = EXCLUDED.open_time, close_time = EXCLUDED.close_time, active = true, updated_at = now();
SELECT setval(pg_get_serial_sequence('outlets', 'id'), (SELECT max(id) FROM outlets));

-- +goose Down
-- Removes every seeded outlet, including id 1.
DELETE FROM outlets WHERE id IN (1, 2, 3, 4, 5, 6, 7);
