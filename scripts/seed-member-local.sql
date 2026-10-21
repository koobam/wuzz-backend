-- Local demo customer for the app's member card (wuzz database, user service).
-- Safe to re-run. The app's "simulate login" signs in as this phone through the
-- dev-only POST /app/v1/dev/login, so it needs to exist.
INSERT INTO users (username, name, phone, points, xp)
VALUES ('janfalih', 'Jan Falih Fadhillah', '6281299900001', 1250, 820)
ON CONFLICT (phone) DO UPDATE SET points = EXCLUDED.points, xp = EXCLUDED.xp;

INSERT INTO wallets (user_id, balance)
SELECT id, 150000 FROM users WHERE phone = '6281299900001'
ON CONFLICT (user_id) DO UPDATE SET balance = EXCLUDED.balance;

INSERT INTO wallet_ledger (user_id, amount, kind, reference)
SELECT id, 150000, 'dev-seed', 'local demo' FROM users
WHERE phone = '6281299900001'
  AND NOT EXISTS (SELECT 1 FROM wallet_ledger l WHERE l.user_id = users.id AND l.kind = 'dev-seed');

INSERT INTO vouchers (user_id, code, title, expires_at)
SELECT u.id, v.code, v.title, now() + v.ttl
FROM users u, (VALUES
  ('WELCOME10',  'Diskon 10% pesanan pertama', interval '30 days'),
  ('FREEPICKUP', 'Gratis pickup',              interval '14 days')
) AS v(code, title, ttl)
WHERE u.phone = '6281299900001'
ON CONFLICT (user_id, code) DO UPDATE SET expires_at = EXCLUDED.expires_at, used_at = NULL;

-- Three friends the demo customer referred (existing local test accounts).
INSERT INTO referrals (referrer_id, referred_id)
SELECT me.id, u.id
FROM users me, users u
WHERE me.phone = '6281299900001'
  AND u.phone IN ('6281234500001', '6281234500002', '6281234500003')
ON CONFLICT DO NOTHING;

-- Local staff, so the backoffice and POS can be opened. Phones are stored without
-- the leading 0 (62...). Staff sign in with a code like anyone else; here it is
-- printed in the user service's log. Safe to re-run.
INSERT INTO users (username, name, phone, roles)
VALUES ('staff34500099', 'Admin Ani', '6281234500099', ARRAY['admin']),
       ('staff34500098', 'Kasir Budi', '6281234500098', ARRAY['outlet:1'])
ON CONFLICT (phone) DO UPDATE SET roles = EXCLUDED.roles, updated_at = now();
