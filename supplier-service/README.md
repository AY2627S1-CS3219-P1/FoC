# Reminding

This is the backend for [RemindMe](github.com/yihao03/remindme), an app made to
support dementia caregivers through this difficult journey.

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
