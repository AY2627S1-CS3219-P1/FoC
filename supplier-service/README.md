# Supplier Service

This is the supplier service for [Friend on Campus (FoC)](../README.md).

## Setup

1. Setup a Firebase project, this should be the same project used for the frontend.
   1. Enable Authentication with Email/Password and Google Sign-In.
   1. Create a service account and copy its JSON key into `.env` as
      `FIREBASE_CREDENTIALS_JSON='<json>'`. The server writes it to a temp
      file and points `GOOGLE_APPLICATION_CREDENTIALS` at it on startup.

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

Run fast and PostgreSQL/PostGIS-backed tests separately:

```sh
make test
make test-integration
```

See `.pi/docs/01-supplier-data-foundations/handoff.md` for the schema names, generated sqlc surface, source-key convention, and fixture assumptions used by downstream Location work.
