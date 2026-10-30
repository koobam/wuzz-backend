-- The first admin, so the backoffice can be signed in to. It is assigned to outlet 1 (Wuzz Pusat), which is also what
-- lets it sign in to that outlet's POS: an admin with no outlet cannot use a till. To add another admin (in any
-- environment, production included) write a new migration like this one; role is a row of
-- admin_roles: super, finance or support. The number below is a test number: replace it, or add
-- the real admins next to it, before a real production launch.

-- +goose Up
INSERT INTO admins (name, phone, role, outlet_id)
VALUES ('Admin Staging', '6281234500099', 'super', 1)
ON CONFLICT (phone) DO NOTHING;

-- +goose Down
DELETE FROM admins WHERE phone = '6281234500099' AND name = 'Admin Staging';
