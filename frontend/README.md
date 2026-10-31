# Friend on Campus frontend

SvelteKit 2, Svelte 5, TypeScript, Svelte-M3 and Superforms. Authentication,
profiles, user-role administration and supplier/location discovery and
administration use generated Connect RPC clients.

## Run locally

Requires Node.js >=22.12.0 and npm. From this directory:

```sh
npm ci
npm run dev
```

From the repository root, `docker compose up --build` starts the frontend at
`http://localhost:5173` and the backend services. Set `FRONTEND_PORT` and
`FRONTEND_BASE_URL` consistently if changing the port. Compose uses named volumes
for frontend dependencies and build output; restart the frontend after lockfile
changes. `docker compose down -v` also deletes database data.

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

Backend service images (User, Supplier, Order, Credit, Communication), the
migration Jobs and the Kafka initialization Job use the shared Dockerfiles
under `docker/k8s/`; see [`infra/k8s/README.md`](../infra/k8s/README.md#container-images).

## Ownership handoff

Browser-reachable service URLs:

- `PUBLIC_USER_SERVICE_URL`: defaults to `http://localhost:8081`.
- `PUBLIC_SUPPLIER_SERVICE_URL`: defaults to `http://localhost:8082`.

## Routes

- `/suppliers` and `/suppliers/[id]`: authenticated location discovery.
- `/admin`: administration home; admin and super-admin roles only.
- `/admin/users`: account-role management.
- `/admin/suppliers`: location search, filters and archived views.
- `/admin/suppliers/new`: create a supplier or facility.
- `/admin/suppliers/[id]`: edit, archive/restore and manage closure history.
- `/admin/supplier-requests`: status-filtered addition-request review queue.
- `/admin/supplier-requests/[id]`: proposal details and approval/rejection.

Location deletion is archival. Buildings and categories are read-only reference
data. Opening hours and closure dates use Asia/Singapore regardless of the
browser timezone. Temporary closures do not prevent location selection.

Creates retain retry keys within the mounted editor after uncertain outcomes.
Retry the same details; check the list/history before explicitly starting another
creation. Update conflicts preserve drafts and require explicit reconciliation.
Leaving an editor loses its in-memory retry key, so verify the list before
resubmitting after a reload. Review decisions are final. Approval and rejection require the reviewed revision;
the server checks it atomically before deciding. A stale review must be reloaded.

## Development

```sh
npm run check
npm test
npm run build
```

Services and API adapters live by capability under `src/lib/services/`; the
composition root injects transports and authentication. Generated protobuf types
are under `src/lib/gen/` and must not be edited. Client singleton services are
used only in browser lifecycle and event code. Authorization is enforced by the
backend as well as guarded UI rendering.

## Ownership

Coordinate changes to root layout, shared styles, shared API code and deployment
configuration. Supplier administration intentionally extends the shared navigation
and accessibility styles while preserving the existing Material theme.
