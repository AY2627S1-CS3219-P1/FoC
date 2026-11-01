-- Local, opt-in fixture accounts. psql -v reset_demo=false preserves manual edits.
BEGIN;
SET LOCAL lock_timeout = '5s';
SELECT pg_advisory_xact_lock(18003219, 1);
CREATE TEMP TABLE demo_options ON COMMIT DROP AS SELECT :'reset_demo'::boolean AS reset;
CREATE TEMP TABLE demo_users (
    id UUID PRIMARY KEY, email TEXT, display_name TEXT, role TEXT,
    description TEXT, telegram_handle TEXT, phone_number TEXT
) ON COMMIT DROP;
INSERT INTO demo_users VALUES
    ('f0c00001-0000-4000-8000-000000000001', 'superadmin@demo.foc.test', 'Super Admin', 'super_admin', 'Can promote/demote admins, suspend and reinstate other accounts.', NULL, NULL),
    ('f0c00001-0000-4000-8000-000000000002', 'admin@demo.foc.test', 'Admin', 'admin', 'Can manage locations, review proposals and suspend/reinstate ordinary users.', NULL, NULL),
    ('f0c00001-0000-4000-8000-000000000003', 'student@demo.foc.test', 'Student', 'user', 'Owns demo requests. Try changing these profile fields.', 'demo_student', '+6500000000'),
    ('f0c00001-0000-4000-8000-000000000004', 'suspended@demo.foc.test', 'Suspended Student', 'suspended', 'Read-only profile. Use an administrator to reinstate this account.', NULL, NULL),
    ('f0c00001-0000-4000-8000-000000000005', 'other@demo.foc.test', 'Other Student', 'user', '', NULL, NULL),
    ('f0c00001-0000-4000-8000-000000000006', 'staff@demo.foc.test', 'Other Admin', 'admin', 'Target for super-admin demotion and admin-to-admin permission tests.', NULL, NULL);

-- Never take over an unrelated account that already uses a fixture email/ID.
DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM users u JOIN demo_users d ON u.id=d.id OR (u.email=d.email::citext AND u.deleted_at IS NULL)
        WHERE u.id<>d.id OR u.email<>d.email::citext
    ) THEN RAISE EXCEPTION 'A demo account identity conflicts with an existing user; no users were seeded'; END IF;
END $$;

INSERT INTO users (id,email,display_name,role,description,telegram_handle,phone_number)
SELECT id,email,display_name,role,description,telegram_handle,phone_number FROM demo_users
ON CONFLICT(id) DO UPDATE SET
    display_name=EXCLUDED.display_name, role=EXCLUDED.role, description=EXCLUDED.description,
    telegram_handle=EXCLUDED.telegram_handle, phone_number=EXCLUDED.phone_number,
    deleted_at=NULL, updated_at=now()
WHERE (SELECT reset FROM demo_options);

-- Existing accounts can sign in without a domain whitelist. This entry also
-- allows testing registration with a new address such as newuser@demo.foc.test.
INSERT INTO allowed_email_domains (id,domain)
SELECT 'f0c00001-0000-4000-8000-000000000010', 'demo.foc.test'
WHERE NOT EXISTS (SELECT 1 FROM allowed_email_domains WHERE domain='demo.foc.test' AND deleted_at IS NULL)
ON CONFLICT(id) DO UPDATE SET deleted_at=NULL, updated_at=now();

-- Seed one consistent suspension audit, without touching admin_bootstrap or sessions.
INSERT INTO role_changes(id,user_id,from_role,to_role,reason,created_by,updated_by)
VALUES ('f0c00001-0000-4000-8000-000000000011','f0c00001-0000-4000-8000-000000000004','user','suspended','Seeded suspension for reinstatement testing','f0c00001-0000-4000-8000-000000000002','f0c00001-0000-4000-8000-000000000002')
ON CONFLICT(id) DO NOTHING;
SELECT email,role FROM users WHERE id IN (SELECT id FROM demo_users) ORDER BY email;
COMMIT;
