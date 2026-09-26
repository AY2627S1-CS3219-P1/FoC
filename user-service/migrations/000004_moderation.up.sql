-- U4 / U6: append-only history of every role change (promote, demote,
-- suspend, reinstate). users.role holds the current role.
-- Suspending or reinstating requires a reason (U6.2, U6.9.1).
CREATE TABLE role_changes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    from_role   VARCHAR(32) NOT NULL REFERENCES roles (name) ON UPDATE CASCADE,
    to_role     VARCHAR(32) NOT NULL REFERENCES roles (name) ON UPDATE CASCADE,
    reason      VARCHAR(2000),
    actor_id    UUID REFERENCES users (id) ON DELETE SET NULL,   -- NULL = system
    report_id   UUID,           -- report service; no FK across services
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (from_role <> to_role),
    CHECK (
        (from_role <> 'suspended' AND to_role <> 'suspended')
        OR length(btrim(coalesce(reason, ''))) > 0
    )
);
CREATE INDEX idx_role_changes_user ON role_changes (user_id, created_at DESC);

-- U7: account warnings. Created from report outcomes / order events,
-- removed when an appeal is Overturned (U7.6).
CREATE TABLE account_warnings (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    request_id      UUID NOT NULL,          -- U7.2 originating request (order service)
    report_id       UUID,                   -- set when it came from a report
    reason          VARCHAR(2000) NOT NULL CHECK (length(btrim(reason)) > 0),
    status          VARCHAR(16) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'removed')),
    source_event_id UUID UNIQUE,            -- idempotent consumption (NFR-04)
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    removed_at      TIMESTAMPTZ,
    removed_reason  VARCHAR(2000),
    appeal_id       UUID,                   -- appeal that overturned it
    CHECK ((status = 'removed') = (removed_at IS NOT NULL))
);
CREATE INDEX idx_warnings_user ON account_warnings (user_id, created_at DESC);
