# Supplier Service

This is the supplier service for [Friend on Campus (FoC)](../README.md).

## Setup

1. Setup a Firebase project, this should be the same project used for the frontend.

   1. Enable Authentication with Email/Password and Google Sign-In.
   1. Create a service account and copy its JSON key into `.env` as
      `FIREBASE_CREDENTIALS_JSON='<json>'`. The server passes it directly
      to the Firebase SDK.

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

## API demo

`scripts/api-demo.sh` shows the Location APIs working without the frontend. It
checks discovery, role checks, and the admin create, update, archive, and
restore lifecycle against a running, seeded service, and exits non-zero on the
first unexpected response. It needs `curl`, `jq`, and `uuidgen`, plus User
Service access tokens for a `user` and an `admin`:

```sh
USER_TOKEN='<token>' ADMIN_TOKEN='<token>' scripts/api-demo.sh
```

Set `SUPPLIER_BASE_URL` if the service is not on `http://localhost:8082`, and
`PAUSE=1` to step through it while presenting. Each run archives the Location it
creates, so it can be rerun.

## Tests

Run fast and PostgreSQL/PostGIS-backed tests separately:

```sh
make test
make test-integration
```
