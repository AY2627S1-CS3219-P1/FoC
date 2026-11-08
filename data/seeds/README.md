# Project-defined local seed data

This directory contains all project-defined seed records: `users.json` for
accounts, roles and the registration domain; `suppliers.sql` for buildings,
categories, locations, requests and closures. Assignment-provided CSVs live in
[`../assignment/`](../assignment/README.md) and retain their existing Go importer.

These files populate existing tables. They are explicit development fixtures,
not Goose schema migrations, and Compose startup does not execute them.

Run after the Compose services have applied their migrations:

```sh
docker compose up -d postgres mailpit user-service supplier-service credit-service frontend
make seed-demo
```

Wait for User, Supplier and Credit migrations before seeding. New users emit
`users.create` through the registration provisioning logic; User, Kafka and Credit
must be running for their 100-credit accounts to appear asynchronously.

Open http://localhost:5173/login. Enter one of the emails below and open its
magic link in [Mailpit](http://localhost:8025). No passwords or authentication
bypasses are seeded. Use separate browser profiles for simultaneous roles.

| Email | Role / use |
| --- | --- |
| `superadmin@demo.foc.test` | Promote/demote admins and test protected super-admin actions |
| `admin@demo.foc.test` | Supplier CRUD, request reviews, ordinary-user suspension/reinstatement |
| `staff@demo.foc.test` | Another admin; target for promotion/demotion and permission checks |
| `student@demo.foc.test` | Owns odd-numbered requests; populated optional profile fields |
| `other@demo.foc.test` | Owns even-numbered requests; blank optional profile fields |
| `suspended@demo.foc.test` | Suspended user; test read access and blocked mutations, then reinstate |

`demo.foc.test` is also allowed for new-account registration, for example
`newuser@demo.foc.test`. Delivery relies on local Mailpit.

## Real venues and sources

Venue names and representative opening hours were checked on 15 October 2026:

- [NUS UCI Kent Ridge dining directory](https://uci.nus.edu.sg/campus-life/campus-services/retail-dining/kent-ridge/): HSSML TOMORO, Supersnacks PGP, CHAGEE, Cool Spot, The Coffee Roaster, The Terrace, Bakehaus / Aurea, Central Square, Arise and Shine, Pasta Express, Nami, Good Day Cafe, He by He Brews, Li Ji Coffeehouse, Mixue, Mini Wok, Coffee Bean, Uncle Penyet and Smooy.
- [NUS UCI University Town dining directory](https://uci.nus.edu.sg/campus-life/campus-services/retail-dining/university-town/): NUS College TOMORO, ERC Starbucks, Fine Food, Flavours, Hwang’s, Jollibee, Subway and The Royals Bistro.
- [NUS Foodies opening announcement](https://t.me/s/nusfoodies?before=718): the new TOMORO outside NUS Law Library at **20 College Avenue West, #02-402, Singapore 138529**, with an opening promotion on 10–14 August 2026. The same feed lists the NUS College outlet at 16 College Avenue West. These are separate outlets. No opening hours were verified for the new Law Library outlet, so they are blank.
- [HSSML](https://www.nus.edu.sg/nuslibraries/spaces/our-libraries/hon-sui-sen-memorial-library) and [C J Koh Law Library](https://www.nus.edu.sg/nuslibraries/spaces/visiting-c-j-koh-law-library): library identities/contact/access context.
- Existing `data/assignment/building-seed-data.csv` supplies most building-centre coordinates. Added UTown centres are coarse campus-map approximations, rather than surveyed storefront coordinates; see the [NUS Law UTown map](https://law.nus.edu.sg/trail/wp-content/uploads/sites/9/2026/03/Directions-To-Around-UTown-251204.pdf).

Fixtures use ordinary names and are identified by fixed UUIDs. Real venue
identities are used for realistic
search and map testing; all approvals, rejections, archives, closure reasons and
closure dates are **fictional**. Extra multilingual/long text is explicitly
sample UI content. These records do not assert that an outlet is actually closed.

The model holds one daily time pair. Stored times are representative term-time
weekday hours, with exceptions in the details. They are not a complete weekly
schedule. ERC Starbucks has unspecified time fields because equal opening and
closing times cannot represent 24/7 in the current model. Missing contact, floor
or hours means unspecified. Pins use approximate building centres.

## Scenarios

Visit `/admin/suppliers`, `/admin/supplier-requests`, `/admin/users` and `/profile`.
There are 34 locations (28 suppliers, six ordinary locations), 34 requests and
32 closure records. Default listings have enough entries to exercise pagination.

Fixture UUIDs have the form `f0c0000K-0000-4000-8000-NNNNNNNNNNNN`:
`K=1` accounts, `2` buildings, `3` categories, `4` locations, `5` requests,
`6` closures. The final decimal number identifies a scenario below.

| Scenario | Records / action |
| --- | --- |
| Create/edit supplier | HSSML TOMORO (location 1) has Food + Coffee categories; change fields/categories and save |
| Ordinary location | Central Library (3), COM2 (14), Stephen Riady Centre (30): no supplier categories |
| Overnight hours | Supersnacks PGP (2), 11:00–02:00; verify closing time is next day |
| Missing fields / 24-hour limitation | New Law TOMORO (11) and ERC Starbucks (12) have unspecified time fields |
| Duplicate name | Locations 10 and 11 both named TOMORO COFFEE, with different buildings |
| Unicode / wrapping | Hwang’s (26) has a Unicode name; Bakehaus (9) includes multiline, long sample details |
| Archive/restore | CHAGEE (4), YIH (5), Law Library (33) start archived; restore, archive again and filter |
| Pending review | Requests 1–24; approve supplier request 1 or ordinary-location request 3; reject another with a reason |
| Ownership / withdrawal | Student owns odd-numbered requests, Other Student even-numbered ones; edit/withdraw an owned pending proposal through RPC |
| Approved | Requests 25–28 point to locations 31–34; includes archived ordinary location 33 and temporarily closed supplier 34 |
| Rejected / withdrawn | Requests 29–31 have rejection reasons; requests 32–34 are withdrawn |
| Stale revision / competing decisions | Open pending request 4 in two admin sessions; decide in one, then confirm in the other; the second must conflict |
| Active indefinite closure | Cool Spot (6), closure 1; end it |
| Scheduled finite closure | Coffee Roaster (7), closure 2 starts in two days; edit/cancel it |
| Active finite closure | Terrace (8), closure 3 ends in 12 hours; end it or test overlapping interval validation |
| Cancelled / ended early / expired | Terrace (8), closures 4 / 5 / 6–30; verify history and pagination |
| Scheduled indefinite closure | Terrace (8), closure 31 starts in seven days; edit/cancel it |
| User permissions | Compare ordinary user, suspended user, admin and super admin; test self-change and admin-to-admin restrictions |

Invalid input, blank required fields, missing supplier categories, equal opening
and closing times, overlapping closures, stale revisions and permission failures
must be exercised through forms/RPC. They are not seeded as invalid persisted
rows. Use a temporary browser network block/service stop to test loading errors,
and a search with no matches to test empty results. The seed provides records
for workflows; some mutations (such as owner request edits) currently require RPC.

## Start with a fresh database

Compose startup applies schema migrations; it does **not** automatically seed.
For a complete local Compose reset:

```sh
# Deletes local database contents AND other Compose volumes/caches.
docker compose down --volumes
docker compose up -d
# Wait until User, Supplier and Credit have finished migrations/startup.
make seed-demo
```

Service recreation rebuilds Go dependencies and may take several minutes; see
[the build-cache landmine](../../LANDMINES/go-cache.md). If seeding reports missing
tables during startup, wait for migrations to finish and rerun it. You can inspect startup
with `docker compose logs -f user-service supplier-service`.

## Re-run and reset

`make seed-demo` inserts missing fixtures and preserves existing fixture edits,
revisions, category links and lifecycle dates. It does not import/modify the
separate campus CSV dataset. Buildings and categories reuse matching records by
name when the CSV importer has already created them; otherwise they receive fixed
fixture IDs. Reset only updates reference records with fixture IDs.

Only newly created users emit creation events. Rerun/reset preserves Credit
balances and event history. Reset restores soft-deleted fixture users.

To refresh labels in databases seeded with the old names, run
`make seed-demo-reset`. This also restores the fixture states described below.

`make seed-demo-reset` restores the fixed fixture IDs, roles, categories, requests
and relative closure dates. Existing revisions increase so open stale forms
remain stale. It does **not** delete newly created records, including locations
created when you approve a pending fixture proposal, or erase existing audit
history. Sign out/in after resetting roles to refresh the browser session.

Manually created closure history on a fixture location may overlap the reset
intervals. In that case the supplier transaction refuses to reset: resolve the
conflicting intervals first. Cancelled intervals do not block a reset.

The Make targets pass `users.json` over stdin to `user-service/cmd/seed` first.
It requires `APP_ENV=local` and applies the fixtures in one transaction; conflicting
user identities abort it.

The targets then invoke the standalone SQL runner at `pkg/cmd/seed`.
It reads `manifest.json` in array order and validates the entire SQL manifest and
all referenced SQL files before starting. Paths must stay within this directory;
unknown fields, unsupported versions, empty files and duplicate file/database
entries are rejected.

```json
{
  "version": 1,
  "files": [
    { "file": "suppliers.sql", "database": "SUPPLIER_DB" }
  ]
}
```

`database` names an environment variable in the Compose PostgreSQL container;
its value is the target database name. The runner invokes `psql` sequentially
through `docker compose exec`, with `ON_ERROR_STOP=1` and `reset_demo=false`
(or `true` for `--reset`). It stops on the first failure and reports the file
and target. Each SQL file owns its transaction. An earlier file may already
have committed when a later one fails; fix the error and rerun.

There is no cross-database transaction or schema migration step. Wait for
service migrations before seeding. The assignment CSV importer remains separate.
For another SQL fixture, put its file here and add an entry to `manifest.json`;
the SQL runner handles it without a Makefile or Go code change. User fixtures
remain in `users.json` and use the User command described above.

Run the SQL runner directly from the repository root for Supplier fixtures only:

```sh
go run ./pkg/cmd/seed --manifest data/seeds/manifest.json
go run ./pkg/cmd/seed --manifest data/seeds/manifest.json --reset
```

The `--project-dir` option selects a Compose project directory (default `.`).
Manifest file paths are relative to the manifest, independent of this option.

The SQL runner's unit tests and Docker-backed PostgreSQL integration test can be
run as follows:

```sh
(cd pkg && GOWORK=off go test -mod=readonly ./cmd/seed)
(cd pkg && GOWORK=off go test -mod=readonly -tags=integration ./cmd/seed -count=1 -timeout=5m)
```

To run outside Compose, apply migrations and supply host-accessible connection
URLs. From the repository root:

```sh
(cd user-service && APP_ENV=local DATABASE_URL="$USER_DATABASE_URL" go run ./cmd/seed --fixtures ../data/seeds/users.json)
psql "$SUPPLIER_DATABASE_URL" -X -v ON_ERROR_STOP=1 -v reset_demo=false -f data/seeds/suppliers.sql
```

Pass `--reset` to the User command and use `reset_demo=true` for Supplier SQL to
restore the fixed fixtures. Fixture definitions,
including all simulated workflow states and relative dates, stay in this
directory. The assignment CSV importer remains independent. Only use these
fixtures with development databases; they are never startup migrations.
