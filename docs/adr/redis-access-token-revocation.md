# ADR: Redis-backed access-token revocation

Date: 3 Nov 2026
Status: implemented; closes the revocation half of #57 (NFR-05.4.1)

## Problem

Access JWTs carry `sub`, `sid` (session UUID), `jti`, and `role`, and live up to
the configured access-token lifetime (default 10 minutes). Only the refresh
session was revocable: logout, logout-all, and refresh rotation revoked the
`sessions` row, but no middleware ever consulted session state, so a
signature-valid access token stayed accepted for up to its full lifetime after
logout, suspension, or a role change. NFR-05.4.1 requires tokens to become
unusable within one minute.

## Decision

A shared Redis dictionary holds one entry per revoked session:
`revoked_access_tokens:<sid>` with the revocation timestamp as value and a
per-entry TTL equal to the maximum access-token lifetime.

- **Keyer is the session ID (`sid`), not the token ID (`jti`).** At revocation
  time nobody holds the outstanding access tokens: logout presents only the
  refresh cookie, and logout-all must kill tokens on other devices whose
  `jti`s were never stored. `sid` is in every access claim, so one entry covers
  all outstanding tokens of the session and no client protocol changes.
- **TTL is exactly the access-token lifetime.** A revoked session can never be
  revived and refresh always mints a new session ID, so every token an entry
  could cover is guaranteed expired when the entry lapses. Steady-state size is
  bounded at roughly one entry per active session because rotation frequency is
  bounded by the access-token lifetime; Redis evicts expired keys itself.
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
  dictionary after signature verification. A revoked `sid` is 401; an
  unreachable Redis is 503 (`Unavailable`), never a silent pass-through. A
  missing revocation checker is a startup error, not a disabled check. The
  User Service rejects `REDIS_ADDR` at startup; other services construct the
  checker in `NewUserServiceAuthenticator` or their composition root.

## Consequences

- Revocation is prompt (one Redis read per request) and survives the
  access-token gap without a token blacklist per token or a database hit per
  request.
- Role changes now sign the target out of every device; the fresh login or
  refresh after reinstatement issues tokens with the new role claim. This
  replaces the earlier guidance that stale token claims are tolerated until
  expiry.
- A Redis outage blocks authenticated calls in all services (fail-closed).
  Compose therefore health-checks Redis, orders services after it, and
  persists the dictionary with append-only files: a wiped dictionary would let
  already-revoked tokens pass again until their expiry. The client uses short
  dial/read/write timeouts so failures fail fast. Redis fsyncs every
  acknowledged append (`appendfsync always`), so a crash cannot drop
  acknowledged revocations and revive their tokens; deployments that relax
  this accept a bounded window where the same tokens pass after a crash.
- Local development gains one Redis container (`compose.yaml`); all services
  require `REDIS_ADDR`.
- Credit's internal reservation listener remains unauthenticated (issue #29);
  it performs no revocation check because it performs no authentication.

## Testing

Unit tests fake the `Revocations`/`RevocationChecker` interfaces (consumer
declared) for both the middleware and the User Service; `pkg/auth/revocation` is
tested against miniredis, including entry expiry. `user-service/tests/revocation`
runs the full flow — login, logout, rotation, logout-all, suspension — against
throwaway PostgreSQL and Redis containers started by testcontainers
(`make test-integration` in the User Service directory, tagged `integration`).
