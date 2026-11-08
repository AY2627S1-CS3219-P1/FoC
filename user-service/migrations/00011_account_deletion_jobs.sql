-- One pending-work job per deleting account. The job lives for the whole draining
-- period and is removed together with the users row in the final hard-delete
-- transaction (FK CASCADE). It carries the lease pair, the drain progress and the
-- bounded reasons/errors so a crash leaves the account deleting and resumable.
-- +goose Up
CREATE TABLE account_deletion_jobs (
    user_id                 UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    deletion_request_id     UUID NOT NULL UNIQUE,
    admissions_safe_after   TIMESTAMPTZ NOT NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    next_attempt_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_checked_at         TIMESTAMPTZ,
    attempt_count           INTEGER NOT NULL DEFAULT 0,
    lease_token             UUID,
    lease_until             TIMESTAMPTZ,
    waiting_reasons         TEXT[] NOT NULL DEFAULT '{}',
    last_error              TEXT,
    CONSTRAINT account_deletion_jobs_lease_pair_check
        CHECK ((lease_token IS NULL) = (lease_until IS NULL))
);

-- Due jobs ordered by age so a replica drains the oldest pending work first. The
-- leading column matches the claim's next_attempt_at <= now() range and the sort;
-- the lease expiry is applied in the query, not the predicate, because a time
-- function is not immutable and cannot appear in an index predicate.
CREATE INDEX idx_account_deletion_jobs_due
    ON account_deletion_jobs (next_attempt_at, user_id);

-- +goose Down
DROP TABLE IF EXISTS account_deletion_jobs;
