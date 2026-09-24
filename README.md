# CS3219 — Software Design and Architecture (AY2627 Sem 1)

## Friend on Campus (FoC)

**Friend on Campus (FoC)** is a peer-to-peer campus errand platform where
students can request items to be collected from stores or facilities on
campus, and other students can fulfil (and deliver) those requests. The
platform runs on a closed credit economy — credits cannot be bought,
withdrawn, or exchanged for money, and only circulate within the platform.

---

## Team Members

| Name | Role |
| ----- | ----- |
| Your Name | Your ownership |
| Your Name | Your ownership |
| Your Name | Your ownership |
| Your Name | Your ownership |
| Your Name | Your ownership |

---

## Repository Structure

This repository follows a **one-service-per-folder** structure: each
microservice (`user-service/`, `supplier-service/`, `order-service/`,
`credit-service/`) lives in its own top-level folder.

```text
.
├── user-service/
├── supplier-service/
├── order-service/
├── credit-service/
├── frontend/
├── <n2h-service>/
└── README.md
```

- Any **nice-to-have (N2H)** feature that warrants its own service should
  be added as an **additional folder** at the same level, following the
  same per-service structure.
- Files for agentic coding tools (e.g. agent configs, prompts, skills)
  may be added as needed, but must still **respect the
  one-service-per-folder skeleton** for core implementation.
- `frontend/` is the SvelteKit web application; see its README for local
  development and frontend ownership.

---

## Local Dev Setup

Prerequisites: `docker` with the `docker-compose` plugin. Host-run migrations require `goose`.

Services: `user-service` (`localhost:8081`), `supplier-service` (`localhost:8082`), shared `postgres:18` (`localhost:5432` with `user_dev` / `supplier_dev` DBs).

1. Configure env:
   ```bash
   cp .env.example .env
   # fill POSTGRES_PASSWORD, matching USER_DATABASE_URL and SUPPLIER_DATABASE_URL, and FIREBASE_CREDENTIALS_JSON
   ```

2. Start infra + services (migrations run before live reload via Air):
   ```bash
   docker compose up --build
   ```
   This is the complete Compose setup. Each service gets its `DATABASE_URL` from `.env` through `compose.yaml`; no separate migration command is needed.
   After adding a migration, restart the affected service to apply it.

3. Optional: run services on the host instead of in Compose. These commands need host-facing database URLs because `postgres` is only a hostname inside the Compose network:
   ```bash
   make postgres
   make user-migrate supplier-migrate
   make user-run # in one terminal
   make supplier-run # in another terminal
   ```
   The root Makefile builds the host database URLs from `.env` and calls each service's `migrate-up` target.

Tear down (containers + images + DB data):
```bash
docker compose down -v --rmi all
```
