# ADR: Redis-backed access-token revocation (superseded by PostgreSQL storage)

Date: 3 Nov 2026
Status: implemented; closes the revocation half of #57 (NFR-05.4.1).
Updated 8 Nov 2026: the storage backend moved from Redis to a shared PostgreSQL
table; the decision rationale below is unchanged except where noted.

## Problem

Access JWTs carry `sub`, `sid` (session UUID), `jti`, and `role`, and live up to
the configured access-token lifetime (default 10 minutes). Only the refresh
session was revocable: logout, logout-all, and refresh rotation revoked the
`sessions` row, but no middleware ever consulted session state, so a
signature-valid access token stayed accepted for up to its full lifetime after
logout, suspension, or a role change. NFR-05.4.1 requires tokens to become
unusable within one minute.

## Decision

A shared revocation store holds one entry per revoked session, keyed by the
session ID, with a lifetime equal to the maximum access-token lifetime. The
backend is PostgreSQL: table `revoked_access_tokens(sid TEXT PRIMARY KEY,
revoked_at TIMESTAMPTZ, expires_at TIMESTAMPTZ)`, accessed directly by every
service through `pkg/auth/revocation` (pgx connection pool). The entry address
is configured with `TOKEN_REVOCATION_BASE_URL`, a `postgresql://` DSN.

- **The key is the session ID (`sid`), not the token ID (`jti`).** At revocation
  time nobody holds the outstanding access tokens: logout presents only the
  refresh cookie, and logout-all must kill tokens on other devices whose
  `jti`s were never stored. `sid` is in every access claim, so one entry covers
  all outstanding tokens of the session and no client protocol changes.
- **The lifetime is exactly the access-token lifetime.** A revoked session can
  never be revived and refresh always mints a new session ID, so every token an
  entry could cover is guaranteed expired when the entry lapses. Expiry is a
  column (`expires_at`), not an eviction: reads filter with
  `expires_at > now()`, and the User Service periodically deletes expired rows
  (`DeleteExpired`) to bound table and index size.
- **The User Service is the only writer.** After a database session revocation
  commits — logout, refresh rotation, logout-all (which returns the revoked
  session IDs from `RevokeAllForUser`), every role change including suspension,
  and a bootstrap promotion of an existing user — it records the revoked
  session IDs. Write failures are logged and swallowed: the session is already
  revoked, and a missed entry only leaves tokens valid until their own expiry.
- **Token issuance serializes against role changes.** Login and refresh read
  the user with `FOR UPDATE` inside the transaction that issues tokens, so a
  concurrent role change either committed first (the new tokens carry the new
  role) or commits later (its revocation sweep then covers the new session).
  Without the lock, a login racing a suspension could mint a session that
  escapes revocation and keeps the old role until expiry.
- **Every service is a reader and fails closed.** The shared authenticators
  (`pkg/auth/httpauth.Authenticator` for supplier, order, communication, and
  credit; `AuthenticateLocal` for the issuing User Service) check the
  revocation store after signature verification. A revoked `sid` is 401; an
  unreachable store is 503 (`Unavailable`), never a silent pass-through. A
  missing revocation checker is a startup error, not a disabled check. All
  services require `TOKEN_REVOCATION_BASE_URL` at startup; the connection pool
  uses short connect/statement timeouts so failures fail fast.

## Consequences

- Revocation is prompt (one indexed primary-key lookup per request) and
  survives the access-token gap without a token blacklist per token.
- Role changes now sign the target out of every device; the fresh login or
  refresh after reinstatement issues tokens with the new role claim. This
  replaces the earlier guidance that stale token claims are tolerated until
  expiry.
- A revocation-store outage blocks authenticated calls in all services
  (fail-closed). Deployments must provision the `revoked_access_tokens`
  database durably like any other service database: a wiped table would let
  already-revoked tokens pass again until their expiry. Acknowledged writes are
  committed transactions, so a database crash cannot silently drop them beyond
  what PostgreSQL durability already guarantees.
- Local development gains one `revocation_dev` database on the shared Postgres
  (`docker/postgres/init-dbs.sh`, `compose.yaml`); all services require
  `TOKEN_REVOCATION_BASE_URL`. Kubernetes overlays provision a dedicated
  `revocation` role/database and inject the same variable as a secret.
- Credit's internal reservation listener remains unauthenticated (issue #29);
  it performs no revocation check because it performs no authentication.

## Testing

Unit tests fake the `Revocations`/`RevocationChecker` interfaces (consumer
declared) for both the middleware and the User Service; `pkg/auth/revocation`
is tested against a throwaway PostgreSQL container (testcontainers), including
entry expiry and cleanup. `user-service/tests/revocation` runs the full flow —
login, logout, rotation, logout-all, suspension — against a throwaway
PostgreSQL container (`make test-integration` in the User Service directory,
tagged `integration`).
