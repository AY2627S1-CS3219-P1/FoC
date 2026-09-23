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
Shared Go HTTP utilities live in the `pkg` module (`api` and `middleware`),
imported by services through a local `replace` directive. Each service keeps
its own `internal/deps/deps.go` for database and Firebase dependencies and
configures the default `slog` logger at startup.
The root `go.work` lets Go commands resolve all three modules together during
local development. The `replace` directives also support the current Docker
layout, which builds each service without the root workspace. From the
repository root, run
`go test ./pkg/... ./supplier-service/... ./user-service/...`.

```text
.
├── user-service/
├── supplier-service/
├── order-service/
├── credit-service/
├── <n2h-service>/
└── README.md
```

- Any **nice-to-have (N2H)** feature that warrants its own service should
  be added as an **additional folder** at the same level, following the
  same per-service structure.
- Files for agentic coding tools (e.g. agent configs, prompts, skills)
  may be added as needed, but must still **respect the
  one-service-per-folder skeleton** for core implementation.

---

## Local Dev Setup

Prerequisites: `docker` with the `docker-compose` plugin, `goose` for migrations.

Services: `user-service` (`localhost:8081`), `supplier-service` (`localhost:8082`), shared `postgres:18` (`localhost:5432` with `user_dev` / `supplier_dev` DBs).

1. Configure env:
   ```bash
   cp .env.example .env
   # fill POSTGRES_PASSWORD and FIREBASE_CREDENTIALS_JSON
   ```

2. Start infra + services (live reload via Air):
   ```bash
   docker compose up --build
   ```

3. Run migrations from the host (not compose):
   ```bash
   DATABASE_URL=postgresql://foc:changeme@localhost:5432/user_dev?sslmode=disable make -C user-service migrate-up
   DATABASE_URL=postgresql://foc:changeme@localhost:5432/supplier_dev?sslmode=disable make -C supplier-service migrate-up
   ```

4. Host-run alternative (without compose services):
   ```bash
   docker compose up -d postgres
   PORT=8081 DATABASE_URL=postgresql://foc:changeme@localhost:5432/user_dev?sslmode=disable make -C user-service run
   PORT=8082 DATABASE_URL=postgresql://foc:changeme@localhost:5432/supplier_dev?sslmode=disable make -C supplier-service run
   ```

Tear down (containers + images + DB data):
```bash
docker compose down -v --rmi all
```
