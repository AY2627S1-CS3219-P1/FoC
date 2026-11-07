# User Service

This is the user service for [Friend on Campus (FoC)](../README.md). It
provides email magic-link authentication and ES256 access and refresh tokens.

## Setup

1. Configure the environment.

   For Compose, copy the root environment example and fill in the database
   settings:

   ```bash
   cp .env.example .env
   ```

   `APP_ENV=local` enables local development behavior. The user-service
   container creates `.local/secrets/auth/jwt-signing-private.pem` if it is
   missing, before starting Goose and Air. Compose mounts this directory so
   the key persists across container restarts. The service derives its public
   key and publishes it through `user.v1.PublicKeyService.GetPublicKeys`.

   The signing key must be an unencrypted P-256 private key in PKCS#8 PEM
   format (`-----BEGIN PRIVATE KEY-----`). The dev container generates this
   format automatically. For host development, run this from the repository
   root when no key exists:

   ```bash
   umask 077
   mkdir -p .local/secrets/auth
   openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out .local/secrets/auth/jwt-signing-private.pem
   ```

   If an existing dev key begins with `-----BEGIN EC PRIVATE KEY-----`,
   convert it to PKCS#8 before restarting. This preserves the key material:

   ```bash
   umask 077
   openssl pkcs8 -topk8 -nocrypt -in .local/secrets/auth/jwt-signing-private.pem -out .local/secrets/auth/jwt-signing-private.pkcs8.pem
   mv .local/secrets/auth/jwt-signing-private.pkcs8.pem .local/secrets/auth/jwt-signing-private.pem
   ```

   Signing key rotation and live key reload are not supported. Replacing the
   configured key invalidates tokens signed by the old key.

   `JWT_ACCESS_TOKEN_TTL` and `JWT_REFRESH_TOKEN_TTL` accept Go duration
   values. Their defaults are `10m` and `720h` (30 days).

   Magic links are sent over SMTP. `SMTP_ADDR` (`host:port`) and `SMTP_FROM`
   (an address such as `FoC <no-reply@foc.local>`) are required outside
   local mode. With `APP_ENV=local` they default to Mailpit on
   `localhost:1025`; Compose points the container at `mailpit:1025`.
   `SMTP_USERNAME` and `SMTP_PASSWORD` are optional, STARTTLS is used when
   the server offers it, and `SMTP_TIMEOUT` (default `5s`) bounds each send.

2. Set up the database.

   The service uses PostgreSQL 18 and
   [Goose](https://github.com/pressly/goose) migrations. Compose waits for
   PostgreSQL and applies migrations before
   [Air](https://github.com/air-verse/air) starts. To run the service
   directly, copy `.env.template` to `.env`, set `DATABASE_URL` and
   `JWT_PRIVATE_KEY_FILE`, then run `make migrate-up` from this directory.

3. Start the service.

   From the repository root, run:

   ```bash
   docker compose up -d kafka-init
   docker compose up --build user-service
   ```

   Compose also starts [Mailpit](https://mailpit.axllent.org/), which
   catches every email the service sends. Open http://localhost:8025 to
   read login and registration links. For host development, run
   `docker compose up -d mailpit kafka-init`, set
   `KAFKA_BROKERS=localhost:9092` in `.env`, then run `make run` from this
   directory after setting up the environment and database.

## Authentication

The generated `user.v1.AuthService` provides `RequestLink`, `Login`,
`Register`, `Refresh`, `Logout`, and `LogoutAll`. `user.v1.PublicKeyService.GetPublicKeys`
publishes the signing key set, and `user.v1.HealthService.Check` reports
service health. These services use ConnectRPC; the former REST auth, health,
and JWKS routes are no longer served.

Login, registration, and refresh return an access token in their typed
response. Clients hold it in memory and send it in the `Authorization: Bearer`
header. The refresh token is only sent as a `foc-refresh-token` cookie with
Secure, HttpOnly, SameSite=Strict and Path=/user.v1.AuthService/. Refresh and
both logouts read that cookie and the logouts clear it. `LogoutAll` revokes
every session of the cookie's user (sign out of all devices) and, unlike
`Logout`, returns `Unauthenticated` when the cookie is not a live session.
Browser clients must send credentials
so the cookie can be stored and sent. Requests with an `Origin` must match the
configured frontend origin; service-to-service requests without an `Origin`
are permitted. The access and refresh lifetimes come from the two JWT TTL
environment variables. Link requests return an empty typed response; the magic
link is only emailed, through `pkg/email.SMTPSender`. If delivery fails,
`RequestLink` returns `Unavailable` and logs the SMTP error. An existing
account receives a `/login?token=` link and a new address a
`/register?token=` link; both expire after ten minutes and work once.

## Access-token revocation

Revoking a session also revokes its outstanding access tokens within one
request, instead of letting them live out their remaining lifetime (NFR-05.4.1,
issue #57; see `docs/adr/redis-access-token-revocation.md`). After a logout,
refresh rotation, logout-all, or role change commits, the User Service records
the revoked session IDs in a shared PostgreSQL table `revoked_access_tokens`
(one row per session, keyed by session ID), with an `expires_at` equal to the
maximum access-token lifetime. Every service's authentication middleware
consults that table after verifying the signature: a revoked session's access
token is rejected with `Unauthenticated`, even though it is still
cryptographically valid. If the revocation store is unreachable, verification
fails closed with `Unavailable` rather than accepting tokens of unknown
revocation state.

`TOKEN_REVOCATION_BASE_URL` (a `postgresql://` DSN) is required in every Go
service. Compose points every service at the shared `revocation_dev` database
on the dev Postgres; host development uses the published Postgres port. The
table holds no user data beyond session UUIDs and revocation timestamps, the
read path ignores expired rows, and the User Service periodically deletes
expired entries to keep the table small. Token issuance reads the
user row under a lock, so a session created while an admin changes that user's
role either carries the new role or is swept into the change's revocation.

Registration only accepts addresses whose domain is in
`allowed_email_domains`; an empty table rejects every registration. For local
testing, add a domain, for example
`INSERT INTO allowed_email_domains (domain) VALUES ('u.nus.edu');`. The
bootstrapped admin can log in without one.

`RegisterRequest` requires a display name and a phone number. Display names are
trimmed and must be 1 to 100 characters. Phone numbers are validated with
libphonenumber (`nyaruka/phonenumbers`), reading numbers without a country code
as Singapore numbers, and stored in E.164 form such as `+6591234567`. The phone
number is the contact fallback for users who haven't linked Telegram; Telegram
is linked through the Communication Service, so User Service stores no Telegram
details. Registration, login, and refresh return these saved fields in the
`User` message. The bootstrapped admin has no phone number until they add one.


## First super admin

Set `BOOTSTRAP_SUPERADMIN_EMAIL` (and optionally
`BOOTSTRAP_SUPERADMIN_DISPLAY_NAME`, default `Admin`) before starting the
service. After migrations, startup creates that user as `super_admin`, or
promotes an existing user with that email and records the role change. The
admin then signs in with the normal magic-link login. The registration domain
whitelist does not apply.

The bootstrap runs once. It records the admin in the `admin_bootstrap` table,
and later starts log that it was already done and ignore the variables, even if
the email changes or the admin was demoted. Concurrent instances are serialised
by a table lock, so only one bootstrap succeeds. An invalid email or display
name stops startup. To bootstrap again, delete the `admin_bootstrap` row by
hand. When the bootstrap promotes an existing user to `super_admin`, that
user's existing sessions are revoked (and recorded in the revocation
dictionary), because the promotion changed the role stored in their
outstanding access tokens.

Other Go services set `USER_SERVICE_BASE_URL` to the Connect server base URL
and initialize one authenticator at startup. The authenticator fetches keys
through `PublicKeyService.GetPublicKeys`. Register its `Authenticate` method on protected routes and
read the verified caller with `auth.CallerFromContext`. The
authenticator fetches keys at startup, refreshes its cache every hour,
and fetches early when a token names an unknown key ID. The base URL must be a
bare origin (`http` or `https`); transport security is the deployment
environment's responsibility (the Istio mesh provides mTLS in Kubernetes, and
the Compose network isolates local traffic).
The authenticator also consults the shared revocation store and requires
`TOKEN_REVOCATION_BASE_URL`; see "Access-token revocation" above.

```go
import authmiddleware "github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"

authenticator, err := authmiddleware.NewUserServiceAuthenticator(ctx)
if err != nil {
    return err
}
protectedRouter.Use(authenticator.Authenticate)
```

## Profile and role APIs

The generated Connect services below require an access token in the
`Authorization: Bearer` header. The User Service verifies it with its loaded
signing key and checks the caller's current role in the database.

| RPC | Request | Response | Access |
| --- | --- | --- | --- |
| `ProfileService.GetMyProfile` | Empty | Profile with ID, email, display name, description, phone number, and role | Every authenticated user |
| `ProfileService.UpdateMyProfile` | Complete editable profile: `display_name`, `description`, `phone_number` | Updated profile | All authenticated roles except `suspended_user` |
| `UserAdminService.GetUserByEmail` | `email` | User ID, email, display name, role | `admin`, `super_admin` |
| `UserAdminService.ChangeUserRole` | `user_id`, `to_role`, optional `reason` | Updated user | `admin`, `super_admin`, subject to the policy below |

`UpdateMyProfile` replaces all three editable fields. An omitted description
becomes empty. A display name must be nonblank after trimming and at most 100
characters, and a phone number is required and validated as at registration.
The description limit is 500 characters. The request cannot update email, ID,
role, or account status.

`super_admin` may change another user's role among `admin`, `user`, and
`suspended_user`. `admin` may change another user's role only between `user`
and `suspended_user`. Neither may change its own role or a `super_admin` role.
If either the previous or new role is `suspended_user`, a nonblank reason of at
most 2,000 characters is required. The role update, audit entry, and the
target's session revocations commit together. Because the role lives in
outstanding access tokens, the change signs the target out of every device;
they must sign in again (or refresh, where their session allows) to obtain the
new role claim.

The `admin_bootstrap` table exists, but registration does not yet claim it.
First-admin provisioning and account deletion APIs are separate work.

## Credit account provisioning

Successful registration writes a `UserCreated` fact to the transactional
outbox in the same database transaction as the user and session. The relay
publishes it to `users.create` using the user UUID as the Kafka key. Delivery
is at least once; `event_id` remains stable across retries so consumers can
deduplicate. A Kafka outage does not fail registration; pending facts are
retried when the broker is available.

`KAFKA_BROKERS` is required. Compose sets it to `kafka:9092`; host development
uses `localhost:9092`. The `kafka-init` service provisions the topic. Credit
owns the 100-credit starting grant. Existing users are not backfilled, and
bootstrap-created administrators do not emit registration events.
