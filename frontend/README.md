# Friend on Campus frontend

This is the SvelteKit and TypeScript frontend for Friend on Campus. The current
routes are a minimal scaffold; authentication and supplier data are not yet
connected to the Go services.

## Run locally

From the repository root, `docker compose up --build` starts the frontend at
`http://localhost:5173` along with the backend services. Source edits reload through
Vite. Compose runs `npm ci` on startup and keeps container dependencies and SvelteKit
output in separate named volumes, leaving host dependencies separate. Restart the
frontend after changing `package-lock.json`.

Set `FRONTEND_PORT` in the root `.env` to change the host port, and update
`FRONTEND_BASE_URL` to the same origin for authentication. `PUBLIC_USER_SERVICE_URL`
must be reachable from the browser; it defaults to `http://localhost:${USER_PORT}`
in Compose (port 8081 by default). `docker compose down` retains frontend volumes;
`docker compose down -v` removes them along with the database data.

To run the frontend outside Docker:

Requires Node.js `>=22.12.0` and npm.

```sh
cd frontend
npm ci
npm run dev
```

Open the local URL printed by Vite (normally `http://localhost:5173`). Run
`npm run check` for Svelte and TypeScript checks, and `npm run build` to verify
the production build. No backend is required to view the scaffold.

## Production container

Build from the repository root:

```sh
docker build -f docker/k8s/frontend.Dockerfile -t foc-frontend:production .
```

The image runs the SvelteKit Node server with Node.js 24 as UID/GID 1000.
`npm run build` writes the server and browser assets to `frontend/build`; the
container starts it with `node build`. The adapter runs only during builds,
so `npm run dev` continues to start Vite as before.

Set these environment variables on the Kubernetes frontend container:

```text
NODE_ENV=production
HOST=127.0.0.1
PORT=3000
ORIGIN=https://foc.example.edu
PUBLIC_USER_SERVICE_URL=https://foc.example.edu
PUBLIC_SUPPLIER_SERVICE_URL=https://foc.example.edu
```

Replace the example origin with the deployment's one public HTTPS origin. The
gateway must route the existing Connect RPC paths to their owning Go services
and page/asset requests to the frontend. Both `PUBLIC_*` values are read through
`$env/dynamic/public` at runtime and sent to the browser with the rendered shell;
they are public configuration, not secrets. No build arguments or image rebuilds
are needed to change them. Data fetching and authentication still happen in
the browser after hydration.

`HOST=127.0.0.1` assumes the Kubernetes service mesh sidecar reaches the frontend
over the pod's loopback interface. Set `HOST=0.0.0.0` explicitly to access the
container directly without that sidecar. `PORT`, `HOST`, and `ORIGIN` are runtime
settings; `.env` files are excluded from the image and are not loaded in
production.

The Go services share `docker/k8s/service.Dockerfile`, also built with the
repository root as context:

```sh
docker build -f docker/k8s/service.Dockerfile --build-arg SERVICE=user-service -t foc-user-service:production .
```

`SERVICE` must be `user-service`, `supplier-service`, or `order-service`. Each
image builds with Go 1.27 and runs `/app/server` as UID/GID 65532 with system CA
certificates. Local module replacements are included in the build context.
Credit remains a placeholder and is not a supported image target.

The Kafka initialization Job uses `docker/k8s/kafka-init.Dockerfile`:

```sh
docker build -f docker/k8s/kafka-init.Dockerfile -t foc/kafka-init:local .
```

This image packages the authoritative `infra/kafka/topics.conf`, topic creation
script, and `kafka/` schemas under `/setup` and `/schemas`. Set `KAFKA_BROKERS`
at runtime; topic configuration and schemas do not need separate ConfigMaps.

## Ownership handoff

- Florian: root layout/navigation, shared styles/components, auth and session
  handling, common API client, `/login`, and user-domain pages.
- Kevin: `/suppliers`, supplier detail and administration routes,
  supplier-domain components/types/API calls, and later the supplier map.
- Coordinate before changing root layout, shared API code or deployment config.

The supplier API paths and query parameters are in the team's Google Doc:
`GET /api/suppliers` and `GET /api/suppliers/{supplierID}`. Agree on example
response bodies before adding frontend types. The existing Go services use a
`{ status, data }` JSON response envelope.

The `/login` and `/suppliers` pages are placeholders. Replace each inside its
owner's feature work; they do not imply an auth or supplier API implementation.
