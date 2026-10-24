-- A first admin for staging and local runs, so the backoffice can be signed in to. It is only
-- created in a database that asks for it (ALTER DATABASE ... SET wuzz.seed_admin = 'on'); production
-- never sets that, so this does nothing there. The number is a test number that cannot receive an SMS:
-- staging and local print the code in the user service's log.

-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
    IF current_setting('wuzz.seed_admin', true) = 'on' THEN
        INSERT INTO admins (name, phone, role)
        VALUES ('Admin Staging', '6281234500099', 'super')
        ON CONFLICT (phone) DO NOTHING;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DELETE FROM admins WHERE phone = '6281234500099' AND name = 'Admin Staging';
