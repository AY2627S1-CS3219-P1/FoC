# Proximity sorting and pickup coordinate maintenance

Order LocationSnapshot now has an optional `coordinates` object with WGS 84
`latitude` and `longitude` numbers. Creation copies Supplier coordinates into
both pickup and delivery JSON snapshots. Subsequent Supplier changes do not
modify those snapshots. Public projections remove the complete exact delivery
snapshot, including its coordinates, as before. No schema migration is needed.

Open orders use a spherical great-circle distance (Earth radius 6,371,000 m);
Supplier uses PostGIS geography distance. Both sort in SQL before pagination.
Unlocated legacy pickups follow all located orders. Equal order distances use
created_at DESC, id DESC; Supplier uses name and ID with the selected direction.
Unspecified sorts retain newest orders and alphabetical locations.

Browser coordinates live only in page memory and RPC bodies. Home requests a
shared origin after sign-in once on each visit, with explicit retry on failure.
Full lists keep their normal defaults; `?sort=proximity` requests the viewer's
location. Sort, filtering, paging and realtime reloads reuse the page's origin.
Coordinates never belong in URLs or browser storage.

## Rollout

1. Deploy **every** updated Order and Supplier backend instance before applying
   the backfill. An old Order instance can discard the new snapshot field while
   rewriting JSON. Verify instance versions and drain old instances.
2. Run a dry-run against Order's database and the authenticated Supplier discovery
   endpoint, then apply. GetLocation resolves archived locations too.
3. Enable/deploy the frontend after the backfill. Unresolved pickups remain
   visible at the end of proximity results and have no distance or pickup marker.

## Backfill

Run from `order-service` with these environment variables supplied by your
normal secret mechanism (do not put credentials in shell arguments or logs):

- `DATABASE_URL`: the Order database.
- `SUPPLIER_SERVICE_BASE_URL`: Supplier's authenticated public RPC listener.
- `SUPPLIER_BEARER_TOKEN`: a current user's raw access token, without "Bearer ".

```sh
go run ./cmd/backfill-pickup-coordinates -dry-run
go run ./cmd/backfill-pickup-coordinates -apply
```

The default is dry-run. Output reports candidate, resolved, updated and unresolved
counts, plus order/location IDs for unresolved lookups. Refresh expired credentials
before retrying unresolved rows. A failed lookup never writes coordinates.
Archived locations are supported; missing/unavailable locations remain absent.

The command scans bounded batches, releases database reads before network calls,
then conditionally merges only missing pickup coordinate fields in current JSON.
Existing snapshot names, floors, unknown fields, and existing coordinates are
preserved. No long transaction or database lock spans Supplier calls. Reruns skip
complete coordinates. Stop with Ctrl-C and rerun safely.

Historical coordinates are unavailable: legacy orders use the location's
**current** coordinates at backfill time. The maintenance update intentionally
does not change order revision, lifecycle timestamps or emit business events.
Reload active clients after maintenance to refresh their snapshots.
