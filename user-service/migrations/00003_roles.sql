-- Role lookup table (seeded here because users.role defaults to and references it) and the first-admin singleton.
-- +goose Up
CREATE TABLE roles (
    name        TEXT PRIMARY KEY CHECK (name ~ '^[a-z_]{1,32}$'),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 255),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO roles (name, description) VALUES
    ('super_admin', 'Admin who can also promote users to admin'),
    ('admin',       'Manages users and suspended users'),
    ('user',        'Normal user (requester and courier)'),
    ('suspended',   'Suspended user; read-only access to history, reports and appeals');

ALTER TABLE users
    ADD COLUMN role TEXT NOT NULL DEFAULT 'user'
        REFERENCES roles (name) ON UPDATE CASCADE ON DELETE RESTRICT;

CREATE INDEX idx_users_role ON users (role);

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
