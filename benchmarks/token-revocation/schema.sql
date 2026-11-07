-- Revoked-access-token lookup table for the token-revocation benchmark.
-- The benchmark owns this schema: it TRUNCATEs and repopulates the table on
-- every run, so no migration framework is involved. The primary key on `sid`
-- is the lookup path a check exercises (a hash-index membership test), mirroring
-- the one Redis entry per revoked session in the production dictionary.
--
-- `expires_at` is the Postgres analogue of Redis's per-entry TTL: a check reads
-- `WHERE sid=$1 AND expires_at > now()`, so an expired row is invisible exactly
-- as a lapsed Redis key would be. This is a deliberate, documented asymmetry —
-- Redis expires entries implicitly while Postgres needs an explicit predicate
-- (and a reaper to reclaim space). Values seeded for the benchmark are far in
-- the future so entries stay valid for the whole sweep.
CREATE TABLE IF NOT EXISTS revoked_access_tokens (
    sid         TEXT PRIMARY KEY,       -- session id (`sid`) from the access claim
    revoked_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS revoked_access_tokens_expires_at_idx
    ON revoked_access_tokens (expires_at);
