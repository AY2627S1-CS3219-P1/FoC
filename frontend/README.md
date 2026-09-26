# Friend on Campus frontend

This is the SvelteKit and TypeScript frontend for Friend on Campus. The current
routes are a minimal scaffold; authentication and supplier data are not yet
connected to the Go services.

## Run locally

Requires Node.js `>=22.12.0` and npm.

```sh
cd frontend
npm ci
npm run dev
```

Open the local URL printed by Vite (normally `http://localhost:5173`). Run
`npm run check` for Svelte and TypeScript checks, and `npm run build` to verify
the production build. No backend is required to view the scaffold.

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
