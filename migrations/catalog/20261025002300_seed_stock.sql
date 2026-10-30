-- SAMPLE opening stock for local development: a receipt per sample material at outlet 1, so the stock pages have something to
-- show. The quantities and the costs are illustrative, NOT counted or paid: staging (and anything real) must not keep them.
-- Idempotent: the movements carry the key seed:<sku>, and a balance is only written for a movement that was just recorded.

-- +goose Up
WITH seed(sku, qty, cost, min_qty) AS (VALUES
    ('CHM-DET-01', 10000, 170000, 2000),
    ('CHM-SOF-01', 6000, 200000, 1500),
    ('CHM-PRF-LAV', 1000, 240000, 200),
    ('CHM-PRF-OCN', 1000, 240000, 200),
    ('PKG-PLS-01', 500, 35000, 100),
    ('PKG-HNG-01', 100, 45000, 20)),
moved AS (
    INSERT INTO stock_movements (outlet_id, material_id, kind, qty_delta, value_delta, balance_qty, balance_value, supplier, doc_no, note, created_by, idempotency_key)
    SELECT 1, m.id, 'receipt', s.qty, s.cost, s.qty, s.cost, 'Supplier contoh', 'SEED-' || s.sku, 'Stok awal contoh untuk pengembangan', 'seed', 'seed:' || s.sku
    FROM seed s JOIN materials m ON lower(m.sku) = lower(s.sku) JOIN outlets o ON o.id = 1
    ON CONFLICT (outlet_id, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
    RETURNING material_id, qty_delta, value_delta)
INSERT INTO stock_balances (outlet_id, material_id, qty, value, min_qty)
SELECT 1, mv.material_id, mv.qty_delta, mv.value_delta, s.min_qty
FROM moved mv JOIN materials m ON m.id = mv.material_id JOIN seed s ON lower(s.sku) = lower(m.sku)
ON CONFLICT (outlet_id, material_id) DO NOTHING;

-- +goose Down
-- The ledger refuses deletes, so it is switched off for the length of this one statement pair.
ALTER TABLE stock_movements DISABLE TRIGGER stock_movements_immutable;
DELETE FROM stock_movements WHERE idempotency_key LIKE 'seed:%';
ALTER TABLE stock_movements ENABLE TRIGGER stock_movements_immutable;
DELETE FROM stock_balances WHERE outlet_id = 1 AND material_id IN (SELECT id FROM materials WHERE sku IN ('CHM-DET-01', 'CHM-SOF-01', 'CHM-PRF-LAV', 'CHM-PRF-OCN', 'PKG-PLS-01', 'PKG-HNG-01'))
  AND NOT EXISTS (SELECT 1 FROM stock_movements WHERE stock_movements.outlet_id = stock_balances.outlet_id AND stock_movements.material_id = stock_balances.material_id);
