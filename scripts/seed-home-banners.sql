-- Demo content for the home carousel (banners whose placement is home), for dev and staging. NOT a migration: a migration runs in
-- every environment, and production's carousel is for staff to fill from the back office. The login screen's own list is seeded by
-- seed-login-banners.sql.
--
-- The pictures are the same Unsplash photos the service menu seed uses (square crops, the shape the app draws), so nothing has to be
-- uploaded first: staging has no media bucket yet. Safe to run again: a banner is added only when the home list has none with its title.
--
--   docker compose exec -T postgres psql -U wuzz -d wuzz_catalog -v ON_ERROR_STOP=1 < scripts/seed-home-banners.sql

INSERT INTO banners (title, internal_notes, image_url, target_type, target_value, status, sort_index, placement)
SELECT v.title, 'Seed: scripts/seed-home-banners.sql', v.image_url, v.target_type, v.target_value, 'active',
       (SELECT COALESCE(max(sort_index), 0) FROM banners) + v.n, 'home'
FROM (VALUES
  (1, 'Cuci kiloan mulai Rp7.000 per kg',
      'https://images.unsplash.com/photo-1582735689369-4fe89db7114c?w=1200&h=1200&fit=crop', 'service', '8'),
  (2, 'Mesin cuci dan dryer siap pakai',
      'https://images.unsplash.com/photo-1604335399105-a0c585fd81a1?w=1200&h=1200&fit=crop', 'service', '5'),
  (3, 'Cuci sepatu dan sandal dengan hati-hati',
      'https://images.unsplash.com/photo-1491553895911-0055eca6402d?w=1200&h=1200&fit=crop', 'service', '11')
) AS v(n, title, image_url, target_type, target_value)
WHERE NOT EXISTS (SELECT 1 FROM banners b WHERE b.placement = 'home' AND b.title = v.title)
  AND EXISTS (SELECT 1 FROM services s WHERE s.id = v.target_value::bigint);
