# An old `.env` points Compose services at User Service's public port

**Applies when:** your root `.env` sets `USER_SERVICE_BASE_URL=http://user-service:8080` and you run the stack with Docker Compose. `.env` files copied from `.env.example` before #167 (4 November 2026) have this value.

**Symptom:** after their containers are recreated, services that verify access tokens can't start. Communication logs:

```text
User Service not ready; retrying ... load authentication keys: unimplemented: 404 Not Found
```

User Service logs `POST /user.v1.PublicKeyService/GetPublicKeys` with status 404. Order, Credit and Supplier fail the same way.

**Cause:** #167 split each service into a public listener (`PORT`, 8080) and an internal one (`INTERNAL_PORT`, 8081). User Service serves `PublicKeyService` only on the internal listener. In Compose, `user-service:8080` reaches the public listener.

Kubernetes is different: `http://user-service-internal:8080` is correct there, because port 8080 on the `user-service-internal` Service forwards to the pod's port 8081.

**Fix:** set `USER_SERVICE_BASE_URL=http://user-service:8081` in `.env`, as in `.env.example`, or remove the line so Compose uses its default. Then recreate the services:

```sh
docker compose up -d
```

**Remove this entry when:** no one still runs Compose with an `.env` from before 4 November 2026.
