# Existing Compose environments use User's old public-key address

**Applies when:** an existing `.env` sets
`USER_SERVICE_BASE_URL=http://user-service:8080` after upgrading to the User
Service listener split.

**Symptom:** Supplier, Order, Credit and Communication repeatedly report that
User Service is not ready or authentication keys are unavailable, then fail
startup. Public-key retrieval returns HTTP 404 even though User's public
health RPC responds.

**Cause:** Compose now enables User's `INTERNAL_PORT` (8081 by default).
Public APIs remain on container port 8080, while `PublicKeyService` is mounted
only on the internal listener. Older `.env` files explicitly select port 8080
and override Compose's updated default. Editing `.env.example` cannot migrate
an existing `.env`. This is an intentional breaking configuration change;
keep the listener split.

**Reproduced:** rendering the current Compose configuration with the base of
PR #167's `.env.example` sets User's internal port to 8081 but leaves all four
consumers' `USER_SERVICE_BASE_URL` at `http://user-service:8080`. The public
router omits `PublicKeyService`; consumers fetch public keys before completing
authentication initialization. This was verified through configuration rendering
and code inspection, without launching the full Compose stack.

**Migration:** change the existing `.env` entry to:

```dotenv
USER_SERVICE_BASE_URL=http://user-service:8081
```

Then recreate the affected containers with the updated environment:

```sh
docker compose up -d user-service supplier-service order-service credit-service communication-service
```

`docker compose restart` does not apply changed environment variables. Do not
publish the internal listener, replace existing secrets, or delete database
volumes. `USER_PORT` controls the host mapping for public APIs and is not the
internal service-to-service port. If intentionally overriding `INTERNAL_PORT`,
use that container port in `USER_SERVICE_BASE_URL` instead of 8081.

See the [Compose upgrade instructions](../README.md#upgrading-an-existing-compose-setup).

**Remove this entry when:** existing developer environments have completed the
migration and old port-8080 `.env` files are no longer expected.
