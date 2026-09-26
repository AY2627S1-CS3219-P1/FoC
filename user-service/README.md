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
   key and publishes it at `/.well-known/jwks.json`.

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

The authentication routes are:

- `POST /api/auth` requests a login or registration link for an email address.
- `POST /api/auth/login` consumes a login token.
- `POST /api/auth/register` consumes a registration token and accepts a
  display name.
- `POST /api/auth/refresh` rotates the refresh token.
- `POST /api/auth/logout` revokes the refresh token.

Successful login, registration, and refresh return `data.accessToken`. Clients
hold this token in memory and send it in the `Authorization: Bearer` header.
The refresh token is only sent as a `foc-refresh-token` cookie with Secure,
HttpOnly, SameSite=Strict and Path=/api/auth. Refresh and logout read that cookie;
logout clears it. The access and refresh lifetimes come from the two JWT TTL
environment variables. Link requests return a generic acknowledgment; the
magic link is passed only to the injected email sender. The configured
`EmptyEmailSender` discards it until an email delivery adapter is connected.

The service also provides `GET /.well-known/jwks.json` for its public signing
key and `GET /api/health` for its health check. Authentication storage and
storage adapters are not configured, so stateful authentication routes return
503.

Other Go services set `USER_SERVICE_BASE_URL` and initialize one authenticator
at startup. Register its `Authenticate` method on protected routes and
read `AccessClaims` with `ClaimsFromContext`. The
authenticator fetches keys at startup, refreshes its cache every five minutes,
and fetches early when a token names an unknown key ID.

```go
import authmiddleware "github.com/AY2627S1-CS3219-P1/FoC/user-service/pkg/middleware"

authenticator, err := authmiddleware.NewUserServiceAuthenticator(ctx)
if err != nil {
    return err
}
protectedRouter.Use(authenticator.Authenticate)
```
