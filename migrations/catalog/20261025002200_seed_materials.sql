-- SAMPLE materials and recipes for local development only, so the back office has something to show. The prices and the
-- quantities are illustrative, NOT measured: staging (and anything real) must review or replace every cost before use.
-- Idempotent: materials are keyed by SKU, recipe lines by (owner, material).

-- +goose Up
INSERT INTO materials (sku, name, category, unit, purchase_price, purchase_qty, notes) VALUES
  ('CHM-DET-01', 'Deterjen cair', 'chemical', 'ml', 85000, 5000, 'Jerigen 5 liter. Contoh biaya untuk pengembangan.'),
  ('CHM-SOF-01', 'Pelembut pakaian', 'chemical', 'ml', 100000, 3000, 'Contoh biaya untuk pengembangan.'),
  ('CHM-PRF-LAV', 'Parfum laundry Lavender', 'chemical', 'ml', 120000, 500, 'Contoh biaya untuk pengembangan.'),
  ('CHM-PRF-OCN', 'Parfum laundry Ocean Breeze', 'chemical', 'ml', 120000, 500, 'Contoh biaya untuk pengembangan.'),
  ('PKG-PLS-01', 'Plastik packing', 'packaging', 'pcs', 35000, 500, 'Contoh biaya untuk pengembangan.'),
  ('PKG-HNG-01', 'Hanger', 'packaging', 'pcs', 45000, 100, 'Contoh biaya untuk pengembangan.')
ON CONFLICT (lower(sku)) DO NOTHING;

-- Cuci Pakaian Kiloan (service 8), per kg: detergent with 5% waste and softener. Express adds a little more detergent.
INSERT INTO bom_lines (owner_type, owner_id, material_id, qty, per, loss_percent, sort_index)
SELECT 'service', s.id, m.id, v.qty, v.per, v.loss, v.pos
FROM (VALUES ('CHM-DET-01', 25, 'unit', 5, 1), ('CHM-SOF-01', 15, 'unit', 0, 2)) AS v(sku, qty, per, loss, pos)
JOIN materials m ON lower(m.sku) = lower(v.sku)
JOIN services s ON s.name = 'Cuci Pakaian Kiloan'
ON CONFLICT (owner_type, owner_id, material_id) DO NOTHING;

INSERT INTO bom_lines (owner_type, owner_id, material_id, qty, per, loss_percent, sort_index)
SELECT 'variant_option', o.id, m.id, 5, 'unit', 0, 1
FROM variant_options o
JOIN variant_groups g ON g.id = o.group_id
JOIN service_variant_groups l ON l.variant_group_id = g.id
JOIN services s ON s.id = l.service_id AND s.name = 'Cuci Pakaian Kiloan'
JOIN materials m ON lower(m.sku) = 'chm-det-01'
WHERE o.name = 'Express'
ON CONFLICT (owner_type, owner_id, material_id) DO NOTHING;

-- Modifier options: the scents add a little parfum per kg, the plastic packing is one bag per order line.
INSERT INTO bom_lines (owner_type, owner_id, material_id, qty, per, loss_percent, sort_index)
SELECT 'modifier_option', o.id, m.id, v.qty, v.per, 0, 1
FROM (VALUES ('Pewangi', 'Lavender', 'CHM-PRF-LAV', 2, 'unit'),
             ('Pewangi', 'Ocean Breeze', 'CHM-PRF-OCN', 2, 'unit'),
             ('Layanan Tambahan', 'Packing Plastik', 'PKG-PLS-01', 1, 'line')) AS v(grp, opt, sku, qty, per)
JOIN modifier_groups g ON g.name = v.grp
JOIN modifier_options o ON o.group_id = g.id AND o.name = v.opt
JOIN materials m ON lower(m.sku) = lower(v.sku)
ON CONFLICT (owner_type, owner_id, material_id) DO NOTHING;

-- +goose Down
DELETE FROM bom_lines WHERE material_id IN (SELECT id FROM materials WHERE sku IN ('CHM-DET-01', 'CHM-SOF-01', 'CHM-PRF-LAV', 'CHM-PRF-OCN', 'PKG-PLS-01', 'PKG-HNG-01'));
DELETE FROM materials WHERE sku IN ('CHM-DET-01', 'CHM-SOF-01', 'CHM-PRF-LAV', 'CHM-PRF-OCN', 'PKG-PLS-01', 'PKG-HNG-01');
