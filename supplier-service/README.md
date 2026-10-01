# Supplier Service

This is the supplier service for [Friend on Campus (FoC)](../README.md).

## Setup

1. This project uses [Air](github.com/air-verse/air) for live reloading.
   Install it with instructions on their GitHub page.

1. Database setup

   1. This project uses PostgreSQL. Postgres 18 is recommended.
   1. Use [goose](https://github.com/pressly/goose) for database migration.
      Downloading goose: `https://github.com/pressly/goose`
   1. Create a database and populate .env with `DATABASE_URL=<connection string>`
      e.g. `DATABASE_URL=postgresql://username:password@remotehost:5433/anotherdb`
   1. Run `make migrate-up` in the project root to migrate database.

1. Start the server: `make run`.

Protected Location RPCs accept User Service access tokens. Set
`USER_SERVICE_BASE_URL` to the User Service URL so Supplier Service can fetch
its public signing keys at startup. Authentication and user profiles live in
User Service; the old Supplier Service REST auth and user creation endpoints
have been removed.

## Seed data

After applying migrations, run the repeatable Go importer from this directory:

```sh
DATABASE_URL='postgresql://username:password@host:5432/supplier_dev' make seed
```

It validates the committed Building, Supplier, and ordinary-Location CSV files before writing, imports them in one transaction, and reports inserted and updated counts. Rerunning it updates the same deterministic resources without creating duplicates.

Some notes on rerun behavior:

- `SourceKey` is the stable identity.
- Changing fields under the same `SourceKey` updates the row.
- Changing a `SourceKey` creates a new row and leaves the old row.
- Supplier Category links are synchronized, including removing obsolete links.
- Removing an entire Building, Category, or Location from CSV does not remove it from the database.

Run fast and PostgreSQL/PostGIS-backed tests separately:

```sh
make test
make test-integration
```
