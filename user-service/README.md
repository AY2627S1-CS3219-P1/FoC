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
   docker compose up --build user-service
   ```

   For host development, run `make run` from this directory after setting up
   the environment and database.

## Authentication

The generated `user.v1.AuthService` provides `RequestLink`, `Login`,
`Register`, `Refresh`, and `Logout`. `user.v1.PublicKeyService.GetPublicKeys`
publishes the signing key set, and `user.v1.HealthService.Check` reports
service health. These services use ConnectRPC; the former REST auth, health,
and JWKS routes are no longer served.

Login, registration, and refresh return an access token in their typed
response. Clients hold it in memory and send it in the `Authorization: Bearer`
header. The refresh token is only sent as a `foc-refresh-token` cookie with
Secure, HttpOnly, SameSite=Strict and Path=/user.v1.AuthService/. Refresh and
logout read that cookie; logout clears it. Browser clients must send credentials
so the cookie can be stored and sent. Requests with an `Origin` must match the
configured frontend origin; service-to-service requests without an `Origin`
are permitted. The access and refresh lifetimes come from the two JWT TTL
environment variables. Link requests return an empty typed response; the magic
link is passed only to the injected email sender. The configured
`EmptyEmailSender` discards it until an email delivery adapter is connected.

Authentication persistence is wired to the user-service store. Email delivery
is not configured yet, so `RequestLink` currently stores the challenge but the
configured `EmptyEmailSender` discards the link instead of delivering it.

Other Go services set `USER_SERVICE_BASE_URL` to the Connect server base URL
and initialize one authenticator at startup. The authenticator fetches keys
through `PublicKeyService.GetPublicKeys`. Register its `Authenticate` method on protected routes and
read `AccessClaims` with `ClaimsFromContext`. The
authenticator fetches keys at startup, refreshes its cache every hour,
and fetches early when a token names an unknown key ID. The base URL must use
HTTPS unless `APP_ENV=local`; local mode permits the Compose network's HTTP URL.

```go
import authmiddleware "github.com/AY2627S1-CS3219-P1/FoC/user-service/pkg/middleware"

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
| `ProfileService.GetMyProfile` | Empty | Profile with ID, email, display name, description, optional Telegram handle and phone number, and role | Every authenticated user |
| `ProfileService.UpdateMyProfile` | Complete editable profile: `display_name`, `description`, optional `telegram_handle`, optional `phone_number` | Updated profile | All authenticated roles except `suspended_user` |
| `UserAdminService.GetUserByEmail` | `email` | User ID, email, display name, role | `admin`, `super_admin` |
| `UserAdminService.ChangeUserRole` | `user_id`, `to_role`, optional `reason` | Updated user | `admin`, `super_admin`, subject to the policy below |

`UpdateMyProfile` replaces all four editable fields. An omitted description
becomes empty and omitted contact fields are cleared. A display name must be
nonblank after trimming and at most 50 characters. The other database limits
are 500 characters for description, 32 for Telegram handle, and 20 for phone
number. The request cannot update email, ID, role, or account status.

`super_admin` may change another user's role among `admin`, `user`, and
`suspended_user`. `admin` may change another user's role only between `user`
and `suspended_user`. Neither may change its own role or a `super_admin` role.
If either the previous or new role is `suspended_user`, a nonblank reason of at
most 2,000 characters is required. The role update and audit entry commit
together. Clients should refresh affected users' access tokens to obtain the
new role claim; previously issued access tokens remain valid until expiry.

The `admin_bootstrap` table exists, but registration does not yet claim it.
First-admin provisioning and account deletion APIs are separate work.
