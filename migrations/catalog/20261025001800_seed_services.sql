-- +goose Up
-- The service menu the app sells: 14 services, each listed under its category. Their variants and modifiers are the reusable groups of the
-- next two seeds.
--
-- Applied once and never run again: a correction needs its own new migration.
-- Upserts by id, so it also applies cleanly to a database that already has these rows.

INSERT INTO services (id, name, unit, price, description, service_type, service_category, min_kg, max_kg, discount, discount_type, icon, image, sort_order, active)
OVERRIDING SYSTEM VALUE VALUES
  (5, 'Mesin Cuci', 'sesi', 15000, 'Cuci sendiri pakai mesin cuci di outlet', 'single', 'alacarte', 0, 0, 0, '', 'wash', 'https://images.unsplash.com/photo-1604335399105-a0c585fd81a1?w=400&h=400&fit=crop', 1, true),
  (6, 'Dryer', 'sesi', 10000, 'Keringkan sendiri pakai dryer di outlet', 'single', 'alacarte', 0, 0, 0, '', 'dry', '', 2, true),
  (8, 'Cuci Pakaian Kiloan', 'kg', 7000, 'Cuci pakaian sehari-hari, dihitung per kilo', 'multiple', 'regular', 3, 20, 0, '', 'kiloan', 'https://images.unsplash.com/photo-1582735689369-4fe89db7114c?w=400&h=400&fit=crop', 1, true),
  (9, 'Cuci Pakaian Satuan', 'pcs', 12000, 'Cuci per potong untuk pakaian pilihan', 'single', 'regular', 0, 0, 0, '', 'fold', '', 2, true),
  (10, 'Perlengkapan Tidur & Mandi', 'pcs', 28000, 'Sprei, selimut, bantal, guling, handuk, dan jubah mandi bersih, kering, dan wangi', 'single', 'regular', 0, 0, 0, '', 'bed', '', 5, true),
  (11, 'Sepatu & Sandal', 'pcs', 35000, 'Sepatu dan sandal bersih dan wangi, dikerjakan dengan hati-hati', 'single', 'regular', 0, 0, 0, '', 'wash', 'https://images.unsplash.com/photo-1491553895911-0055eca6402d?w=400&h=400&fit=crop', 3, true),
  (12, 'Karpet & Gorden', 'pcs', 45000, 'Karpet, permadani, dan gorden bersih sampai ke serat', 'single', 'regular', 0, 0, 0, '', 'fold', '', 6, true),
  (13, 'Boneka & Mainan', 'pcs', 20000, 'Cuci boneka dan mainan kain dengan lembut', 'single', 'regular', 0, 0, 0, '', 'bag', '', 7, true),
  (14, 'Tas & Aksesoris', 'pcs', 35000, 'Tas, clutch, dompet, ransel, dan ikat pinggang bersih tanpa merusak bahan', 'single', 'regular', 0, 0, 0, '', 'bag', '', 4, true),
  (15, 'Jaket & Topi', 'pcs', 25000, 'Jaket, hoodie, dan topi bersih tanpa merusak bentuk', 'single', 'regular', 0, 0, 0, '', 'wash', '', 8, true),
  (17, 'Mukena & Sajadah', 'pcs', 18000, 'Mukena dan sajadah dicuci hati-hati, rapi dan wangi', 'single', 'regular', 0, 0, 0, '', 'fold', '', 10, true),
  (18, 'Mobil & Motor', 'unit', 25000, 'Cuci mobil atau motor, dikerjakan di tempatmu atau diantar ke gerai', 'single', 'regular', 0, 0, 0, '', 'wash', '', 13, true),
  (20, 'Pakaian Formal', 'pcs', 45000, 'Jas, kebaya, dan dress dicuci lembut dengan tangan', 'single', 'regular', 0, 0, 0, '', 'dry', '', 11, true),
  (21, 'Perlengkapan Bayi', 'kg', 12000, 'Baju, bedong, selimut, dan perlengkapan bayi dengan deterjen khusus yang lembut', 'multiple', 'regular', 2, 10, 0, '', 'wash', '', 12, true)
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, unit = EXCLUDED.unit, price = EXCLUDED.price,
  description = EXCLUDED.description, service_type = EXCLUDED.service_type, service_category = EXCLUDED.service_category,
  min_kg = EXCLUDED.min_kg, max_kg = EXCLUDED.max_kg, discount = EXCLUDED.discount, discount_type = EXCLUDED.discount_type,
  icon = EXCLUDED.icon, image = EXCLUDED.image, sort_order = EXCLUDED.sort_order,
  active = true, updated_at = now();
SELECT setval(pg_get_serial_sequence('services', 'id'), (SELECT max(id) FROM services));

-- Which category each service is listed under.
UPDATE services s SET category_id = c.id
  FROM service_categories c
 WHERE (c.name, s.id) IN (
   ('Pakaian', 8), ('Pakaian', 9), ('Pakaian', 15), ('Pakaian', 17), ('Pakaian', 20), ('Pakaian', 21),
   ('Self Service', 5), ('Self Service', 6),
   ('Rumah Tangga', 10), ('Rumah Tangga', 12), ('Rumah Tangga', 13),
   ('Sepatu & Tas', 11), ('Sepatu & Tas', 14),
   ('Kendaraan', 18)
 );

-- +goose Down
-- Removes every seeded service.
DELETE FROM services WHERE id IN (5, 6, 8, 9, 10, 11, 12, 13, 14, 15, 17, 18, 20, 21);
