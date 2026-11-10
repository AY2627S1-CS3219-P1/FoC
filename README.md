# CS3219 — Software Design and Architecture (AY2627 Sem 1)

## Friend on Campus (FoC)

**Friend on Campus (FoC)** is a peer-to-peer campus errand platform where
students can request items to be collected from stores or facilities on
campus, and other students can fulfil (and deliver) those requests. The
platform runs on a closed credit economy — credits cannot be bought,
withdrawn, or exchanged for money, and only circulate within the platform.

The backend is a set of Go microservices that expose Connect RPC APIs
defined under `proto/` and react to each other's domain events published to
Kafka (schemas under `kafka/`). Data lives in PostgreSQL, the frontend is a
SvelteKit application, and the repo also contains the protobuf contracts,
SQL schema and build/deploy glue for all services.

---

## System Architecture

The SvelteKit frontend calls each Go service's public Connect RPC listener
straight from the browser through `PUBLIC_*_SERVICE_URL` endpoints. Services
authenticate each other against User's public keys and coordinate through
Kafka events published from transactional outboxes; one Postgres database per
service, S3-compatible object storage, and email and Telegram transports
complete the picture.

```mermaid
flowchart LR
    B(["Student browser"]):::user
    FE["SvelteKit frontend"]:::svc

    US["User Service"]:::svc
    SUP["Supplier Service"]:::svc
    ORD["Order Service"]:::svc
    CRED["Credit Service"]:::svc
    COMM["Communication Service"]:::svc

    B -->|"loads app"| FE
    B -->|"Connect RPC, PUBLIC_*_SERVICE_URL"| US & SUP & ORD & CRED & COMM

    SUP & ORD & CRED & COMM -->|"auth public keys"| US
    ORD -->|"order discovery"| SUP
    ORD -->|"credit reservation"| CRED
    US -->|"magic links"| COMM

    PG[("PostgreSQL, one DB per service")]:::pg
    US & SUP & ORD & CRED & COMM --> PG

    K[("Kafka topics")]:::kafka
    US -->|"users.create outbox"| K
    ORD -->|"order.status_changed / order.outcome outbox"| K
    ORD -->|"order.courier_warning_requested outbox"| K
    K -->|"users.create opens account"| CRED
    K -->|"users.create and order events"| COMM
    K -->|"order.courier_warning_requested"| US

    NC[("Object storage, floci local and S3 on AWS")]:::store
    ORD -.->|"storage-init bucket"| NC
    US -.->|"storage-init bucket"| NC
    B -->|"direct uploads with CORS"| NC

    MAIL["Mailpit local, SES on AWS"]:::ext
    COMM -->|"SMTP email"| MAIL
    TG["Telegram bot"]:::ext
    COMM -.->|"TELEGRAM_BOT_TOKEN"| TG

    classDef svc fill:#dbeafe,stroke:#1d4ed8,color:#111
    classDef user fill:#ede9fe,stroke:#7c3aed,color:#111
    classDef kafka fill:#fee2e2,stroke:#b91c1c,color:#111
    classDef pg fill:#dcfce7,stroke:#15803d,color:#111
    classDef store fill:#fef9c3,stroke:#a16207,color:#111
    classDef ext fill:#f3f4f6,stroke:#6b7280,color:#111
```

---

## Team Members

| Name | Role |
| ----- | ----- |
| Andrew | Supplier & Order Services, AWS production tooling |
| Hon Yi Hao | Supplier & Order Services, shared packages, Kafka infrastructure, frontend, observability; cross-service code review |
| Kevin | Communication Service, Order contracts, Frontend scaffold |
| Florian Viorel Tanely | User Service, Frontend authentication, Kubernetes bootstrap |
| XKMai | Credit Service, User Service persistence and magic-link auth |

---

## Repository Structure

This repository follows a **one-service-per-folder** structure: each
microservice lives in its own top-level folder. Shared Go HTTP utilities
live in the `pkg` module (`api` and `middleware`), and each service module
refers to the shared module as `../pkg`. Compose mounts each service at
`/app` and the shared package at `/pkg`, so the same relative path works
inside the containers, and builds resolve across the modules through the
`go.work` workspace. Each service composes its dependencies and configures
the default `slog` logger at startup.

```text
.
├── user-service/            # accounts, authentication, admin roles
├── supplier-service/        # store and goods discovery, locations
├── order-service/           # errand orders, offers, delivery workflow
├── credit-service/          # closed credit economy and reservation holds
├── communication-service/   # chat, email and Telegram notifications
├── moderation-service/      # reports, appeals and moderation outcomes
├── frontend/                # SvelteKit web application
├── pkg/                     # shared Go modules (api, auth, kafka, gen, ...)
├── proto/                   # Connect RPC API contracts (Buf)
├── kafka/                   # Kafka event contracts (Buf), see kafka/README.md
├── infra/                   # Kafka topic provisioning, Kubernetes, AWS (Terraform)
├── docker/                  # Compose helpers (Postgres init script, k8s images)
├── scripts/                 # agents' tooling (seeding, k8s helpers, AWS release)
├── data/                    # assignment resources and seed data
├── docs/                    # ADRs, runbooks, plans, testing notes
└── README.md
```

- Any **nice-to-have (N2H)** feature that warrants its own service should
  be added as an **additional folder** at the same level, following the
  same per-service structure.
- Files for agentic coding tools (e.g. agent configs, prompts, skills)
  may be added as needed, but must still **respect the
  one-service-per-folder skeleton** for core implementation.
- `frontend/` is the SvelteKit web application; see its README for local
  development instructions.
- API contracts are protobuf files under `proto/<service>/v1`; after
  changing a contract run `make buf-lint` and `make buf-generate` (generated
  Go code lands in `pkg/gen`, TypeScript in
  `frontend/src/lib/gen`). Kafka event contracts follow the same pattern
  under `kafka/<service>/events/v1` and generate into `pkg/kafkagen`.

---

## Local Dev Setup

Prerequisites: `docker` with the `docker-compose` plugin.

### Services and ports

Compose starts the full stack:

| Component | Local URL | Port env var |
| ----- | ----- | ----- |
| Frontend (SvelteKit/Vite) | http://localhost:5173 | `FRONTEND_PORT` |
| User Service (`user-service`) | http://localhost:8081 | `USER_PORT` |
| Supplier Service (`supplier-service`) | http://localhost:8082 | `SUPPLIER_PORT` |
| Order Service (`order-service`) | http://localhost:8083 | `ORDER_PORT` |
| Communication Service (`communication-service`) | http://localhost:8084 | `COMMUNICATION_PORT` |
| Credit Service (`credit-service`) | http://localhost:8085 | `CREDIT_PORT` |
| Postgres (one DB per service) | `localhost:5432` (`user_dev`, `supplier_dev`, `order_dev`, `credit_dev`, `communication_dev`) | `POSTGRES_PORT` |
| Kafka broker (KRaft) | `localhost:9092` | `KAFKA_PORT` |
| Kafka UI (Kafbat) | http://localhost:8080 | `KAFKA_UI_PORT` |
| floci (local S3 for image uploads) | `localhost:4566` | `FLOCI_PORT` |
| Mailpit (outgoing mail catch-all) | http://localhost:8025 | `MAILPIT_UI_PORT` |

The shared Postgres image is `postgis/postgis:18-3.6`, so it also serves the
PostGIS extension used by Supplier's location features. Outgoing email is
caught by Mailpit (read magic links at http://localhost:8025) and the
Telegram bot is optional (`TELEGRAM_BOT_TOKEN` in `.env`); the
Communication Service runs with Telegram disabled without a token.

### Running the stack

1. Configure env:
   ```bash
   cp .env.example .env
   # fill POSTGRES_PASSWORD and matching USER_DATABASE_URL and SUPPLIER_DATABASE_URL
   ```

2. Start infra + services (Go services apply their migrations on startup):
   ```bash
   docker compose up --build
   ```

   On first run `kafka-init` creates every topic declared in
   `infra/kafka/topics.conf` before the services that publish or consume
   events start, `order-storage-init` and `user-storage-init` provision the
   two bucket policies in floci, and `postgres` creates the five dev
   databases from `docker/postgres/init-dbs.sh`. Go services apply their
   migrations on startup; Air live-reloads source changes and Vite reloads
   the frontend (npm dependencies are installed from the lockfile when the
   frontend starts). This is the complete Compose setup — no separate
   migration command is needed; after adding a migration, restart the
   affected service to apply it.

   To run just one service with its dependencies:
   ```bash
   docker compose up --build user-service          # adds Postgres, Kafka topics, storage init, ...
   docker compose up --build communication-service # adds Mailpit and Kafka topics
   ```

### Upgrading an existing Compose setup

The User Service listener split requires a one-time change to existing
`.env` files. Older `.env` files set
`USER_SERVICE_BASE_URL=http://user-service:8080`. Inside the Compose
network, User's port **8080** serves public APIs; port **8081** is User
Service's *internal* listener (published only inside the Compose network,
not to the host) and serves public-key retrieval, so
`USER_SERVICE_BASE_URL` must point at the internal listener:

```dotenv
USER_SERVICE_BASE_URL=http://user-service:8081
```

`USER_PORT` (8081 on the host) is the separate host-side mapping for
public APIs; do not use it for service-to-service calls. With the old
value, Supplier, Order, Credit and Communication cannot fetch public keys
and fail authentication startup after retrying. Updating `.env.example`
does not update your existing `.env`, and its explicit value overrides
Compose's new default. If Compose keeps recreating the old value, delete
the line from `.env` so the default from `compose.yaml` wins. After
editing `.env`, run:

```sh
docker compose up -d user-service supplier-service order-service credit-service communication-service moderation-service
```

Use `up` so Compose recreates containers with the updated environment;
`docker compose restart` alone does not apply it. Keep existing
credentials and database volumes. See the
[listener migration landmine](LANDMINES/compose-user-internal-listener.md)
for diagnosis.

Tear down (containers + images + DB data):
```bash
docker compose down -v --rmi all
```

---

## Kubernetes Deployment (Optional)

For the configurable AWS load and fault-test tools, see the
[performance and resilience runbook](docs/testing/aws-performance.md).

Besides Docker Compose, the full stack can run on a local Kubernetes cluster
(kind) with an Istio service mesh. Compose remains the default for
development; the two stacks share default host ports, so run only one at a
time.

```bash
cp .env.example .env
K8S_BOOTSTRAP_SUPERADMIN_EMAIL=admin@example.test make k8s-up
```

The first run creates the local `kind` cluster, installs Istio and the Gateway
API, builds and deploys every service, and serves the app at
`https://localhost:5173` (self-signed local certificate). Prerequisites,
day-to-day commands (`make k8s-status` / `k8s-smoke` / `k8s-deploy`), and
teardown are documented in [`infra/k8s/README.md`](infra/k8s/README.md).
Operations, security boundaries, and production guidance live in
[`docs/runbooks/kubernetes.md`](docs/runbooks/kubernetes.md); the design
decision is recorded in
[`docs/adr/kubernetes-istio.md`](docs/adr/kubernetes-istio.md).

The local cluster packs one Deployment per application and every platform
dependency into a single `kind` cluster. Each Go service keeps its
deployment-agnostic listener split (`PORT` public, `INTERNAL_PORT`
internal): the Istio Gateway routes only the public Services, while User's
public-key listener and Credit's reservation listener stay internal-only.
Initialization Jobs run in a separate stage before the applications start,
and the developer reaches the stack through port-forwards only.

```mermaid
flowchart TB
    B(["Developer host and kubectl"]):::ext

    subgraph CL["kind cluster"]
        ISTIO{{"Istio CNI and native sidecars"}}:::gw
        G{{"Istio Gateway, GatewayClass istio, HTTPS"}}:::gw

        subgraph APPS["applications: one Deployment per service"]
            FE["Frontend Deployment"]:::svc
            US["User Deployment"]:::svc
            SUP["Supplier Deployment"]:::svc
            ORD["Order Deployment"]:::svc
            COMM["Communication Deployment"]:::svc
            CRED["Credit Deployment"]:::svc
        end

        subgraph INTERNAL["internal-only Services"]
            USI["User public-key listener"]:::int
            CRDINT["Credit reservation listener"]:::int
        end

        subgraph DEPS["dependencies"]
            PG[("PostGIS Postgres, one DB per service")]:::pg
            K[("Kafka broker, KRaft single")]:::kafka
            KUI["Kafka UI"]:::dep
            FLOCI[("floci, S3-compatible image store")]:::store
            MP["Mailpit"]:::dep
        end

        subgraph OBS["observability"]
            GRAF["Grafana"]:::obs
            LOKI["Loki"]:::obs
            ALLOY["Alloy"]:::obs
        end

        subgraph JOBS["initialization Jobs"]
            MIG["per-service migrate Jobs<br/>user, supplier, order, credit, communication"]:::job
            DBPROV["db-provision Job"]:::job
            KINIT["kafka-init Job, topics.conf"]:::job
            SINIT["storage-init Jobs"]:::job
        end
    end

    B -->|"K8S_HTTPS_PORT forward, localhost 5173"| G
    B -->|"K8S_GRAFANA_PORT forward, 3000"| GRAF
    B -->|"K8S_KAFKA_UI_PORT forward, 8080"| KUI
    B -->|"K8S_MAILPIT_PORT forward, 8025"| MP

    G -->|"HTTPRoute root path"| FE
    G -->|"public RPC routes"| US & SUP & ORD & CRED & COMM
    G -.->|"signed image paths"| FLOCI

    ISTIO -.->|"native sidecar injection"| APPS

    SUP & ORD & CRED & COMM -.->|"public-key RPCs"| USI
    ORD -.->|"reservation RPCs"| CRDINT

    US & SUP & ORD & CRED & COMM -->|"per-service database"| PG
    US & ORD -->|"outbox events"| K
    K -->|"users.create"| CRED
    K -->|"users.create and order events"| COMM
    K -->|"courier warning"| US

    MIG -.->|"goose migrations"| PG
    DBPROV -.->|"database roles and secrets"| PG
    KINIT -.->|"create topics"| K
    SINIT -.->|"bucket policies"| FLOCI

    ALLOY -->|"collects pod logs"| LOKI
    GRAF -->|"queries"| LOKI

    classDef svc fill:#dbeafe,stroke:#1d4ed8,color:#111
    classDef gw fill:#e0f2fe,stroke:#0369a1,color:#111
    classDef int fill:#ffedd5,stroke:#ea580c,color:#111
    classDef pg fill:#dcfce7,stroke:#15803d,color:#111
    classDef kafka fill:#fee2e2,stroke:#b91c1c,color:#111
    classDef store fill:#fef9c3,stroke:#a16207,color:#111
    classDef obs fill:#ede9fe,stroke:#7c3aed,color:#111
    classDef job fill:#fce7f3,stroke:#be185d,color:#111
    classDef dep fill:#f3f4f6,stroke:#6b7280,color:#111
    classDef ext fill:#f3f4f6,stroke:#6b7280,color:#111
```

---

## AWS Production Deployment (Optional)

The production environment is defined in Terraform under `infra/aws`; its
README covers prerequisites, state and planning. The release pipeline and
day-to-day rollout/verification steps are documented in
[`docs/runbooks/aws-releases.md`](docs/runbooks/aws-releases.md) and
[`docs/adr/aws-release-configuration.md`](docs/adr/aws-release-configuration.md).

Terraform under `infra/aws` provisions the demo topology below: API Gateway
and a VPC Link in front of an internal NLB and Istio gateway pods, an EKS
cluster with two On-Demand `t3.xlarge` workers in private subnets across two
AZs behind a single NAT gateway, single-AZ data stores, and IRSA workload
roles instead of static AWS access keys.

```mermaid
flowchart TB
    P(["Practitioner"]):::ext

    subgraph NET["network and ingress"]
        AGW{{"API Gateway HTTPS hostname"}}:::gw
        VL["VPC Link"]:::net
        NLB["internal NLB"]:::net
        NAT["single NAT gateway"]:::net
    end

    subgraph EKS["EKS cluster, private subnets, 2 AZs"]
        CP{{"EKS control plane"}}:::gw
        W["two On-Demand t3.xlarge workers"]:::node
        IGW{{"Istio gateway pods"}}:::gw

        subgraph APPS["application pods from ECR"]
            FEP["Frontend"]:::svc
            USP["User"]:::svc
            SUPP["Supplier"]:::svc
            ORDP["Order"]:::svc
            COMMP["Communication"]:::svc
            CREP["Credit"]:::svc
        end

        LPOD["Loki pod, IRSA"]:::obs
    end

    subgraph DATA["data"]
        IRDS[("RDS PostgreSQL, db.t4g.small, single AZ")]:::pg
        IK[("Kafka single broker, 5 GiB encrypted EBS")]:::kafka
        OB[("app orders bucket")]:::store
        SB[("Terraform state bucket")]:::store
        LB[("Loki log bucket")]:::store
    end

    subgraph AUTHA["auth and secrets"]
        SM["Secrets Manager"]:::awsbox
        IAM["IRSA workload roles"]:::awsbox
    end

    subgraph CI["CI"]
        REL["Release pipeline, verified releases"]:::job
    end

    ECR["ECR"]:::awsbox
    SES["SES, verified sender"]:::ext

    P -->|"HTTPS"| AGW
    AGW --> VL
    VL --> NLB
    NLB -->|"TargetGroupBinding"| IGW
    IGW -->|"public RPC routes"| USP & SUPP & ORDP & CREP & COMMP
    IGW -->|"frontend route"| FEP

    W -.->|"private subnet egress"| NAT
    APPS --> IRDS
    USP & ORDP -->|"outbox events"| IK
    IK -->|"users.create"| CREP
    IK -->|"users.create and order events"| COMMP
    ORDP -->|"image uploads"| OB

    ORDP -.->|"IRSA"| IAM
    IAM -.->|"bucket access"| OB
    LPOD -.->|"IRSA"| LB

    IRDS -->|"admin password"| SM
    COMMP -.->|"optional IRSA"| SES

    REL -->|"publishes images"| ECR
    ECR -->|"pod images"| APPS

    classDef svc fill:#dbeafe,stroke:#1d4ed8,color:#111
    classDef gw fill:#e0f2fe,stroke:#0369a1,color:#111
    classDef net fill:#ecfeff,stroke:#0891b2,color:#111
    classDef node fill:#f1f5f9,stroke:#475569,color:#111
    classDef pg fill:#dcfce7,stroke:#15803d,color:#111
    classDef kafka fill:#fee2e2,stroke:#b91c1c,color:#111
    classDef store fill:#fef9c3,stroke:#a16207,color:#111
    classDef awsbox fill:#fff7ed,stroke:#c2410c,color:#111
    classDef obs fill:#ede9fe,stroke:#7c3aed,color:#111
    classDef job fill:#fce7f3,stroke:#be185d,color:#111
    classDef ext fill:#f3f4f6,stroke:#6b7280,color:#111
```

---

## Docs and Glossary

- Architecture decisions and their rationale: `docs/adr/`
- Operational runbooks: `docs/runbooks/`
- Project glossary: [`glossary.md`](glossary.md)
- Non-obvious failure notes (update when a problem bites): `LANDMINES/`

---

## AI Use Summary
This project is done with AI assistance.

**Tools:** AI coding assistants (chat-based and agentic coding
tools such as Opencode, Codex using models such as DeepSeek and GPT 6.1).

**Prohibited phases avoided:** requirements elicitation and prioritization;
architecture and design decisions (component boundaries, design patterns,
data schemas, interfaces); and drafting of decision rationales.

**Used for:**

- Learning support to understand and compare candidate tools, technologies,
  architectures, and design approaches, and to evaluate the trade-offs
  between them. All final decisions, their rationale, and the records in
  `docs/adr/` were made, written, and validated by the team.
- Generating scaffolding and boilerplate when creating a new service (folder
  structure, configuration, build and container definitions, generated-code
  stubs).
- Writing implementation code (functions, classes, unit tests) after the
  team finalized the relevant requirements and design.
- Debugging assistance (error explanations, test suggestions) and
  refactoring and documentation improvements (docstrings, comments, README
  content).

**Verification:** All AI outputs were reviewed, tested, verified, and edited
  as necessary by the team.
