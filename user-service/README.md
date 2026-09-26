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

Successful login and registration set `__Host-foc-access-token` and
`__Host-foc-refresh-token` as Secure, HttpOnly, SameSite=Lax cookies with the
root path. Refresh and logout read the refresh cookie. The access and refresh
lifetimes come from the two JWT TTL environment variables. In local mode,
generated magic-link URLs are written to the service log as one line; keep those
logs private because the URLs contain sign-in tokens.

The service also provides `GET /.well-known/jwks.json` for its public signing
key and `GET /api/health` for its health check. Authentication storage and
email delivery adapters are not configured, so stateful authentication routes
return 503.
