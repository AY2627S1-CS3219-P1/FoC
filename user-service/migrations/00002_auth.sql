-- +goose Up
-- U1.1.2: admin-editable registration domain whitelist.
-- Empty table = no restriction (lets the first admin bootstrap).
CREATE TABLE allowed_email_domains (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain      CITEXT NOT NULL UNIQUE CHECK (domain ~ '^[a-z0-9.-]+\.[a-z]{2,}$'),
    created_by  UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- U1.2 / U2.1: magic links. Only the SHA-256 of the token is stored.
-- Consume atomically:
--   UPDATE auth_tokens SET used_at = now()
--   WHERE token_hash = $1 AND purpose = $2 AND used_at IS NULL AND expires_at > now()
--   RETURNING *;
-- 0 rows => invalid / expired / used (U1.2.5-7, U2.1.3-5).
-- Older links stay valid when new ones are issued (U2.1.7).
CREATE TABLE auth_tokens (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash    BYTEA NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    purpose       VARCHAR(16) NOT NULL CHECK (purpose IN ('register', 'login')),
    email         CITEXT NOT NULL,
    user_id       UUID REFERENCES users (id) ON DELETE CASCADE,
    requested_ip  INET,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL,
    used_at       TIMESTAMPTZ,
    CHECK (expires_at > created_at),
    CHECK (purpose = 'register' OR user_id IS NOT NULL)
);
-- Per-email rate limiting + cleanup of expired rows.
CREATE INDEX idx_auth_tokens_email_created ON auth_tokens (email, created_at DESC);
CREATE INDEX idx_auth_tokens_expires_at    ON auth_tokens (expires_at);

-- U2.2: opaque long-lived session tokens (hash stored).
-- Logout = set revoked_at on one row; logout-all / suspension (NFR-05.4.1)
-- = set revoked_at on every live row for the user.
CREATE TABLE sessions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash    BYTEA NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    user_id       UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    user_agent    VARCHAR(512),
    ip            INET,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL,
    revoked_at    TIMESTAMPTZ,
    CHECK (expires_at > created_at)
);
CREATE INDEX idx_sessions_user_live  ON sessions (user_id) WHERE revoked_at IS NULL;
CREATE INDEX idx_sessions_expires_at ON sessions (expires_at);

-- +goose Down
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS auth_tokens;
DROP TABLE IF EXISTS allowed_email_domains;
