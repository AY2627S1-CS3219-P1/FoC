-- +goose Up
-- U4 / U6: single roles table. Each user has exactly one role; handlers read
-- the caller's role and decide themselves whether the action is allowed.
--   super_admin : everything admin can + promote/demote admins
--   admin       : manage users & suspended users (suspend / reinstate)
--   user        : normal user, both requester and courier (U5)
--   suspended   : blocked from creating/accepting requests, reviews, etc. (U6)
-- Suspension IS a role, so there is no separate users.status column.
CREATE TABLE roles (
    name        VARCHAR(32) PRIMARY KEY CHECK (name ~ '^[a-z_]+$'),
    description VARCHAR(255) NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO roles (name, description) VALUES
    ('super_admin', 'Admin who can also promote users to admin'),
    ('admin',       'Manages users and suspended users'),
    ('user',        'Normal user (requester and courier)'),
    ('suspended',   'Suspended user; read-only access to history, reports and appeals');

-- Natural key, so handlers can compare users.role directly without a join.
ALTER TABLE users
    ADD COLUMN role VARCHAR(32) NOT NULL DEFAULT 'user'
        REFERENCES roles (name) ON UPDATE CASCADE ON DELETE RESTRICT;

-- Admin lookups / dashboard counts (A1.4 active, A1.5 suspended).
CREATE INDEX idx_users_role ON users (role);

-- U4.2: singleton row. The first signup on a fresh system does
--   INSERT INTO admin_bootstrap (user_id) VALUES ($1) ON CONFLICT DO NOTHING
-- in the same tx as creating the user; 1 row => set role = 'super_admin',
-- 0 rows => someone beat you, stay 'user'.
CREATE TABLE admin_bootstrap (
    singleton       BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    user_id         UUID NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    bootstrapped_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS admin_bootstrap;
DROP INDEX IF EXISTS idx_users_role;
ALTER TABLE users DROP COLUMN IF EXISTS role;
DROP TABLE IF EXISTS roles;
