-- Local demo data for the catalog database (wuzz_catalog). Safe to re-run.
INSERT INTO outlets (name, address, phone, latitude, longitude)
SELECT 'Wuzz Pusat', 'Jl. Contoh No. 1, Jakarta', '0211234567', -6.2088, 106.8456
WHERE NOT EXISTS (SELECT 1 FROM outlets);

INSERT INTO services (name, unit, price) VALUES
  ('Cuci + Lipat',   'kg',  8000),
  ('Cuci + Setrika', 'kg', 10000),
  ('Setrika Saja',   'kg',  6000),
  ('Dry Clean',      'pcs', 25000),
  ('Bedcover',       'pcs', 30000)
ON CONFLICT DO NOTHING;
