# user-service

Users, magic-link auth, sessions, roles, warnings and favourites.
Go 1.24 · chi · GORM · PostgreSQL · golang-migrate.

## Layout

```
cmd/server/main.go        process entrypoint, graceful shutdown
internal/app              wires repos → services → handlers into one chi router
internal/auth             magic links, sessions, cookie middleware, Principal + role rules
internal/user             profiles (/users, /users/me) + favourite suppliers
internal/admin            email-domain whitelist, role changes, warnings
internal/models           GORM entities, 1:1 with migrations/
internal/dto              shared JSON shapes (public vs full profile)
internal/apperr           error type: status + code + message + field errors
internal/httpx            JSON decode/encode, error rendering, param parsing
internal/validate         field normalisation + validation
internal/clock            injectable clock (real / fake)
internal/mail             Mailer interface, SMTP impl (Mailpit), in-memory recorder
internal/config, database env config, GORM connection + embedded migrations
migrations/               000001 users · 000002 auth · 000003 roles · 000004 moderation · 000005 favourites
```

Each feature is handler → service → repository, with the repository behind
an interface. Handlers do authorisation from the caller's role; services
hold business rules; repositories own SQL and transactions.

## Run

```bash
go mod tidy                     # generates go.sum
docker compose up --build       # API :8081, Mailpit UI :8025
```

Or locally: `docker compose up -d user-db mailpit`, export `.env.example`, `go run ./cmd/server`.

The **first account registered becomes `super_admin`**. The domain whitelist
starts empty (= any domain), so register the admin first, then add domains.

## API

All bodies are JSON. Protected routes need the `session` cookie (HttpOnly,
Secure, SameSite=Lax).

**Auth (public)**

| Method | Path | Body | Result |
|---|---|---|---|
| POST | /auth/register | `email` | 202, emails a 10-min link to `APP_BASE_URL/register/verify?token=…` |
| POST | /auth/register/verify | `token, display_name, description?, telegram_handle?, phone_number?` (one contact required) | 201 + cookie + profile |
| POST | /auth/login | `email` | 202 (same response whether or not the account exists) |
| POST | /auth/login/verify | `token` | 200 + cookie + profile |
| POST | /auth/logout | – | 204, revokes this session |
| POST | /auth/logout-all | – | 204, revokes every session for the user |

Verification is POST so email link scanners can't burn single-use tokens: the
frontend page reads `?token=` and posts it.

**Profiles & favourites (logged in)**

| Method | Path | Who |
|---|---|---|
| GET / PATCH | /users/me | self |
| GET | /users/me/favourites | self |
| PUT / DELETE | /users/me/favourites/{supplierID} | self (idempotent) |
| GET | /users?role=&limit=&offset= | admin, super_admin |
| GET | /users/{id} | anyone: public profile; self/admins: full |
| PATCH | /users/{id} | self, or a manager of the target's role |
| DELETE | /users/{id} | a manager of the target's role (not self) |

**Admin**

| Method | Path | Who |
|---|---|---|
| GET / POST | /admin/email-domains | admin, super_admin |
| DELETE | /admin/email-domains/{id} | admin, super_admin |
| PUT | /users/{id}/role `{role, reason?, report_id?}` | per `CanAssignRole` (below); reason required to suspend/reinstate |
| GET | /users/{id}/role-changes | self, admins |
| GET | /users/me/warnings | self |
| GET | /users/{id}/warnings | self, admins |
| POST | /users/{id}/warnings `{request_id, reason, report_id?, source_event_id?}` | manager of target's role; replaying `source_event_id` returns the original (200) |
| POST | /warnings/{id}/remove `{reason, appeal_id?}` | manager of the warned user's role |

**Roles** (`internal/auth/principal.go`)

| Caller | Manages | Can change roles |
|---|---|---|
| super_admin | admin, user, suspended | among those three, incl. user → admin |
| admin | user, suspended | user ↔ suspended |
| user, suspended | – | – |

Nobody changes their own role; `super_admin` only comes from first signup.
Role is read from the DB on every request, so suspension applies immediately.

**Errors**

```json
{"error":{"code":"validation_failed","message":"...","fields":{"email":"..."},"request_id":"..."}}
```

`validation_failed` 422 · `invalid_json` 400 · `invalid_id` 400 · `unauthenticated` 401 ·
`forbidden` 403 · `not_found` 404 · `email_registered` / `domain_exists` /
`role_conflict` / `warning_already_removed` 409 · `registration_link_invalid` /
`login_link_invalid` 400 · `too_many_requests` 429 · `mail_unavailable` 503 · `internal` 500

## Test

```bash
go test ./...                                       # unit tests
TEST_DATABASE_URL=postgres://user_svc:user_svc@localhost:5433/users?sslmode=disable \
  go test -p 1 ./...                                # + schema and end-to-end tests
```

The DB tests truncate tables and run migrations down/up, so use a throwaway
database. `-p 1` stops packages sharing it concurrently. The end-to-end test
(`internal/app`) drives every endpoint through HTTP with a fake clock and an
in-memory mailer.

## Not built yet

- **Supplier validation**: favourites accept any UUID (`user.AllowAllSuppliers`)
  until the supplier service exists.
- **Profile pictures**: `profile_picture_key` column exists; S3 presigned
  upload endpoints aren't written.
- **Events**: no outbox, so suspension/role changes aren't published and
  warnings come from the admin endpoint, not report/order events.
- **Rate limiting** counts issued links in the DB (per email/IP). Unknown-email
  login attempts don't create links, so they aren't counted; add an in-memory
  or Redis limiter in front if that matters.
- **Mail** is sent in the background without retry; failures are logged.
