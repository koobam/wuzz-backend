-- Two staff to try the apps with: a cashier at outlet 1 (Wuzz Pusat, see the catalog seeds) for
-- the POS, and a Wuzzer for the station app. Test numbers: replace them, or add real people in a
-- new migration like this one, before a real production launch.

-- +goose Up
INSERT INTO staff (name, phone, role, outlet_id) VALUES
    ('Kasir Pusat', '6281234500098', 'front', 1),
    ('Wuzzer Satu', '6281234500097', 'wuzzer', NULL)
ON CONFLICT (phone) DO NOTHING;

-- +goose Down
DELETE FROM staff WHERE (phone, name) IN (('6281234500098', 'Kasir Pusat'), ('6281234500097', 'Wuzzer Satu'));
