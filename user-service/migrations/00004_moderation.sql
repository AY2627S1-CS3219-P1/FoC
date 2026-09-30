-- Role change history and account warnings.
-- +goose Up
CREATE TABLE role_changes (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    from_role   TEXT NOT NULL REFERENCES roles (name) ON UPDATE CASCADE,
    to_role     TEXT NOT NULL REFERENCES roles (name) ON UPDATE CASCADE,
    reason      TEXT CHECK (char_length(reason) <= 2000),
    report_id   UUID,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ,
    created_by  BIGINT REFERENCES users (id) ON DELETE SET NULL,
    updated_by  BIGINT REFERENCES users (id) ON DELETE SET NULL,
    CHECK (from_role <> to_role),
    CHECK (
        (from_role <> 'suspended' AND to_role <> 'suspended')
        OR length(btrim(coalesce(reason, ''))) > 0
    )
);
CREATE INDEX idx_role_changes_user ON role_changes (user_id, created_at DESC);

CREATE TABLE account_warnings (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    request_id      UUID NOT NULL,
    report_id       UUID,
    reason          TEXT NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 2000),
    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'removed')),
    source_event_id UUID UNIQUE,
    removed_at      TIMESTAMPTZ,
    removed_reason  TEXT CHECK (char_length(removed_reason) <= 2000),
    appeal_id       UUID,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ,
    created_by      BIGINT REFERENCES users (id) ON DELETE SET NULL,
    updated_by      BIGINT REFERENCES users (id) ON DELETE SET NULL,
    CHECK ((status = 'removed') = (removed_at IS NOT NULL))
);
CREATE INDEX idx_warnings_user ON account_warnings (user_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS account_warnings;
DROP TABLE IF EXISTS role_changes;
