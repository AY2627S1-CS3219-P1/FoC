-- Registration domain whitelist, magic-link tokens and sessions. Only token hashes are stored.
-- +goose Up
CREATE TABLE allowed_email_domains (
    id          BIGSERIAL PRIMARY KEY,
    domain      CITEXT NOT NULL CHECK (domain ~ '^[a-z0-9.-]+\.[a-z]{2,}$'),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ,
    created_by  BIGINT REFERENCES users (id) ON DELETE SET NULL,
    updated_by  BIGINT REFERENCES users (id) ON DELETE SET NULL
);
CREATE UNIQUE INDEX idx_allowed_email_domains_live ON allowed_email_domains (domain) WHERE deleted_at IS NULL;

CREATE TABLE auth_tokens (
    id            BIGSERIAL PRIMARY KEY,
    token_hash    BYTEA NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    purpose       TEXT NOT NULL CHECK (purpose IN ('register', 'login')),
    email         CITEXT NOT NULL,
    user_id       BIGINT REFERENCES users (id) ON DELETE CASCADE,
    requested_ip  INET,
    expires_at    TIMESTAMPTZ NOT NULL,
    used_at       TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ,
    created_by    BIGINT REFERENCES users (id) ON DELETE SET NULL,
    updated_by    BIGINT REFERENCES users (id) ON DELETE SET NULL,
    CHECK (expires_at > created_at),
    CHECK (purpose = 'register' OR user_id IS NOT NULL)
);
CREATE INDEX idx_auth_tokens_email_created ON auth_tokens (email, created_at DESC);
CREATE INDEX idx_auth_tokens_expires_at    ON auth_tokens (expires_at);

CREATE TABLE sessions (
    id            BIGSERIAL PRIMARY KEY,
    token_hash    BYTEA NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    user_id       BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    user_agent    TEXT CHECK (char_length(user_agent) <= 512),
    ip            INET,
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL,
    revoked_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ,
    created_by    BIGINT REFERENCES users (id) ON DELETE SET NULL,
    updated_by    BIGINT REFERENCES users (id) ON DELETE SET NULL,
    CHECK (expires_at > created_at)
);
CREATE INDEX idx_sessions_user_live  ON sessions (user_id) WHERE revoked_at IS NULL AND deleted_at IS NULL;
CREATE INDEX idx_sessions_expires_at ON sessions (expires_at);

-- +goose Down
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS auth_tokens;
DROP TABLE IF EXISTS allowed_email_domains;
