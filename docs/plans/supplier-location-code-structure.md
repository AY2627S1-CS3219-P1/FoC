# Supplier Location code structure

Status: #88 package restructure in progress. Idempotency integration and admin operations are separate follow-up work. Planned files are not empty placeholders.

## Target tree

```text
supplier-service/
├── cmd/
│   ├── server/
│   │   └── main.go
│   └── seed/
│       └── main.go
│
├── internal/
│   ├── router/
│   │   ├── router.go
│   │   └── middleware/
│   │
│   ├── rpc/
│   │   ├── health/
│   │   │   └── health.go
│   │   └── location/
│   │       ├── discovery/
│   │       │   ├── handler.go
│   │       │   └── conversion.go
│   │       ├── lifecycle/
│   │       │   ├── admin.go
│   │       │   ├── disablement.go
│   │       │   ├── additionrequest.go
│   │       │   └── conversion.go
│   │       └── shared/
│   │           ├── authorizationinterceptor.go
│   │           ├── validationinterceptor.go
│   │           └── errors.go
│   │
│   ├── location/
│   │   ├── discovery/
│   │   │   ├── service.go
│   │   │   ├── types.go
│   │   │   ├── validation.go
│   │   │   ├── reader.go
│   │   │   └── postgresreader.go
│   │   ├── lifecycle/
│   │   │   ├── service.go
│   │   │   ├── admin.go
│   │   │   ├── disablement.go
│   │   │   ├── additionrequest.go
│   │   │   ├── types.go
│   │   │   ├── validation.go
│   │   │   ├── repository.go
│   │   │   └── postgresrepository.go
│   │   └── shared/
│   │       ├── types.go
│   │       └── validation.go
│   │
│   ├── idempotency/
│   │   ├── idempotency.go
│   │   └── postgres.go
│   │
│   ├── seed/
│   │   ├── seed.go
│   │   ├── load.go
│   │   ├── types.go
│   │   └── utils.go
│   │
│   └── database/
│       ├── database.go
│       ├── utils.go
│       ├── locationdb/
│       ├── lifecycledb/
│       ├── idempotencydb/
│       ├── seeddb/
│       └── userdb/
│
└── database/
    ├── sqlc.yaml
    ├── query/
    │   ├── location.sql
    │   ├── lifecycle.sql
    │   ├── idempotency.sql
    │   ├── seed.sql
    │   └── user.sql
    └── schema/

proto/supplier/
├── v1/
│   └── health.proto
└── location/v1/
    ├── discovery.proto
    ├── admin.proto
    ├── disablement.proto
    ├── addition_request.proto
    └── shared_types.proto
```

Tests stay beside their implementations. Existing REST and support packages are omitted from this graph, not removed.

## Responsibilities

- **D1: Composition and routing.** `cmd/server/main.go` constructs dependencies. `router` mounts generated Connect handlers. HTTP middleware verifies credentials and adds identity to the request context.
- **D2: RPC adapters.** `rpc/location/discovery` and `rpc/location/lifecycle` translate protobuf requests and responses and call business operations. Their conversion files contain representation mapping, not business rules.
- **D3: Shared RPC behaviour.** `rpc/location/shared` contains method-level authorization, protobuf validation and error mapping. Discovery and lifecycle import it; it imports neither adapter package.
- **D4: Business operations.** `location/discovery` owns discovery rules. `location/lifecycle` owns transactional Location changes, Disablements and addition requests. Each `service.go` defines its Service and dependencies. Operation methods may live in other files in the same package.
- **D5: Business validation.** Each capability keeps its own validation. `location/shared` contains only genuinely shared domain types and validation, without protobuf, Connect or PostgreSQL dependencies. It imports neither discovery nor lifecycle.
- **D6: Persistence.** Discovery's `reader.go` defines its read interface; `postgresreader.go` implements it. Lifecycle's `repository.go` defines Repository and Tx interfaces; `postgresrepository.go` implements transactions, locks, queries and database error mapping.
- **D7: Generated queries.** The `database/*db` packages are sqlc-generated query packages, not separate databases. Keep `seeddb` for CSV import queries and `userdb` for the existing supplier user/auth routes. Rename `workflow.sql` and `workflowdb` to `lifecycle.sql` and `lifecycledb` through configuration and generation.
- **D8: Idempotency.** Reuse main's shared `idempotency` implementation. Its Postgres store uses the same transaction as lifecycle resource writes. Preserve existing request hashes, method scopes and API error semantics.

## Admin operations

`rpc/location/lifecycle/admin.go` is the intended home for Create, Update, Archive and Unarchive RPC adapters. `location/lifecycle/admin.go` is the intended home for their business operations. The protobuf file remains `admin.proto`, with service name `LocationAdminService`.

Those operations are currently unimplemented. Including them in this target tree does not authorize implementing new admin behaviour as part of the package restructure.

## Contract and verification constraints

- **C1:** Keep protobuf package `supplier.location.v1`, service names, message names and field numbers unchanged. Internal Go package moves do not require new protobuf packages.
- **C2:** Preserve main's working discovery handler. Mount lifecycle services alongside it, without mounting discovery through lifecycle's unimplemented stubs.
- **C3:** Resource writes and idempotency records must commit or roll back together. Use the creation callback time supplied by main's runner, sampled after the idempotency lock.
- **C4:** Preserve distinct discovery and lifecycle representations where precision or resource details differ. Do not mechanically merge them into shared types.
- **C5:** Retain behavioural tests, including concurrent retries, cancellation/deadline propagation, approval waiting behind Category edits and guarded migration rollback. Verify production routing through generated clients.
- **C6:** Main's shared idempotency migration and the following operational workflow migration are already integrated in this checkout. Do not rewrite applied migrations during the restructure.
