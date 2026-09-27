# Supplier data and seed handoff

## Persistence surface

Migration `00009_expand_location_classification.sql` adds:

- nullable `locations.floor TEXT`, limited to 50 trimmed characters;
- `locations.revision BIGINT NOT NULL DEFAULT 1`, constrained to positive values;
- `location_categories(location_id, category_id, created_at)` with a composite primary key;
- `location_categories_category_idx` for Category-filtered discovery;
- `locations_active_supplier_idx` for active Supplier-classification discovery.

The migration copies every non-null `locations.category_id` value into `location_categories` before dropping the old column. It preserves legacy relationships even if an old row does not meet the new cross-row classification invariant; application operations enforce that invariant for subsequent writes. Existing Location IDs, Buildings, coordinates, archive state, hours, contact, details, and timestamps are unchanged. Downgrade refuses to collapse a Location with multiple Categories into the old single-Category column.

Downstream work in #49 and #51 should use `locations`, `buildings`, `categories`, and `location_categories`. Supplier-classified Locations require one or more `location_categories` rows. Ordinary Locations require none. Those cross-row rules remain application invariants. #52 still owns the equivalent addition-request and disablement schema changes.

The generated sqlc seed surface is in `internal/database/sqlc/seed.sql.go`:

- `GetSeedBuilding` and `UpsertSeedBuilding`;
- `GetSeedCategory` and `UpsertSeedCategory`;
- `SeedLocationExists` and `UpsertSeedLocation`;
- `ListSeedLocationCategoryIDs`, `AddSeedLocationCategory`, `DeleteSeedLocationCategory`, and `DeleteAllSeedLocationCategories`.

## Seed command

From `supplier-service/`:

```sh
DATABASE_URL='postgresql://...' make migrate-up
DATABASE_URL='postgresql://...' make seed
```

`make seed` runs `go run ./cmd/seed`. Optional flags override the three default inputs:

```text
-buildings ../data/csv/building-seed-data.csv
-suppliers ../data/csv/supplier-seed-data.csv
-locations ../data/csv/location-seed-data.csv
```

Every CSV must use its exact committed header. The importer validates and normalizes all files before opening one serializable write transaction. It upserts Buildings, Categories, Locations, and Category relationships, then prints inserted and updated counts by resource and Location classification.

## Stable-key convention

Committed source keys use `<resource>:<stable-kebab-slug>`:

- `building:central-library`;
- `supplier:annas-soup-union`;
- `location:com2-foyer`;
- generated Category keys such as `category:food`.

The importer derives UUIDv5 values from the source key and a fixed Supplier Service seed namespace. Row order and display-name edits do not change Location identity. Existing Building or Category rows with the same canonical name keep their existing UUID.

Building aliases are explicit `|`-separated values in `building-seed-data.csv`. Normalization only trims whitespace, folds case, collapses repeated spaces, and treats straight and curly apostrophes as equivalent. It does not guess unlisted aliases.

## Data assumptions

- Supplier `Type` values split on `/`, so `Food/Coffee` creates two Category relationships.
- Opening hours are Asia/Singapore wall-clock values in `HHMMhrs` format. A closing time before opening time is an overnight range.
- Supplier image URLs are accepted as input columns and ignored.
- Floor values are strings so labels such as `B1` survive round trips.
- COM2 Foyer and Yusof Ishak House Foyer use the official NUS Map building pins because NUS does not publish distinct foyer pins.
- Central Library Main Entrance uses a georeferenced entrance recommendation derived from the official Central Library building pin and the Level 1 floor plan's labelled main entrance facing Central Library Forum. NUS does not publish a separate entrance GPS point.

Coordinate sources:

- NUS Map search data for Central Library, COM2, and Yusof Ishak House: https://map.nus.edu.sg/index.php/search/ajax_auto
- NUS Libraries Central Library Level 1 map: https://lib.nus.edu.sg/learning/gen_pub/outreach/cl/CLMap_2024.pdf
- Central Library page: https://nus.edu.sg/nuslibraries/spaces/our-libraries/central-library
- COM2 foyer context: https://uci.nus.edu.sg/notice-closure-of-car-park-13-and-amendment-of-isb-services-for-development-of-executive-centre/
- Yusof Ishak House context: https://osa.nus.edu.sg/about-osa/contact-us/

## Verification

Fast tests:

```sh
make test
```

PostgreSQL/PostGIS migration and importer tests:

```sh
make test-integration
```

On Apple Silicon, the Testcontainers setup requests `linux/amd64` for `postgis/postgis:18-3.6` in line with `LANDMINES/postgis-apple-silicon.md`.
