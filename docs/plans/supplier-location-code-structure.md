# Supplier Location code structure

Status: #88 package restructure complete and verified locally. Workflow idempotency integration and any remaining admin gaps are separate follow-up work. Accepted main admin behavior is retained, not recreated. Planned files are not empty placeholders.

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
│   │       │   └── handler.go
│   │       ├── lifecycle/
│   │       │   ├── admin.go
│   │       │   ├── handler.go
│   │       │   ├── disablement.go
│   │       │   ├── additionrequest.go
│   │       │   └── conversion.go
│   │       └── shared/
│   │           ├── adminauthorizationinterceptor.go
│   │           ├── authorizationinterceptor.go
│   │           ├── validationinterceptor.go
│   │           ├── errors.go
│   │           ├── principal.go
│   │           └── conversion.go
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
│   │   │   ├── adminpostgres.go
│   │   │   ├── input.go
│   │   │   ├── patch.go
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
- **D2: RPC adapters.** `rpc/location/discovery` and `rpc/location/lifecycle` translate protobuf requests and responses and call business operations. Lifecycle conversion and shared read/admin conversion contain representation mapping, not business rules.
- **D3: Shared RPC behaviour.** `rpc/location/shared` contains method-level authorization, protobuf validation and error mapping. Discovery and lifecycle import it; it imports neither adapter package.
- **D4: Business operations.** `location/discovery` owns discovery rules. `location/lifecycle` owns transactional Location changes, Disablements and addition requests. Each `service.go` defines its Service and dependencies. Operation methods may live in other files in the same package.
- **D5: Business validation.** Each capability keeps its own validation. `location/shared` contains only genuinely shared domain types and validation, without protobuf, Connect or PostgreSQL dependencies. It imports neither discovery nor lifecycle.
- **D6: Persistence.** Discovery's `reader.go` defines its read interface; `postgresreader.go` implements it. Lifecycle's `repository.go` defines Repository and Tx interfaces; `postgresrepository.go` implements transactions, locks, queries and database error mapping.
- **D7: Generated queries.** The `database/*db` packages are sqlc-generated query packages, not separate databases. Keep `seeddb` for CSV import queries and `userdb` for the existing supplier user/auth routes. Rename `workflow.sql` and `workflowdb` to `lifecycle.sql` and `lifecycledb` through configuration and generation. Keep accepted `locationdb` and `idempotencydb` packages.
- **D8: Idempotency.** Reuse main's shared `idempotency` implementation. Its Postgres store uses the same transaction as lifecycle resource writes. Preserve existing request hashes, method scopes and API error semantics.

## Admin operations

`rpc/location/lifecycle/admin.go` is the intended home for Create, Update, Archive and Unarchive RPC adapters. `location/lifecycle/admin.go` is the intended home for their business operations. The protobuf file remains `admin.proto`, with service name `LocationAdminService`.

Those operations are implemented by accepted main PR #103. The restructure moves their implementation and tests without adding admin behaviour. `AdminService` and `AdminTx` remain distinct from workflow `Service` and `Tx`.

## Contract and verification constraints

- **C1:** Keep protobuf package `supplier.location.v1`, service names, message names and field numbers unchanged. Internal Go package moves do not require new protobuf packages.
- **C2:** Preserve main's working discovery handler. Mount lifecycle services alongside it, without mounting discovery through lifecycle's unimplemented stubs.
- **C3:** Resource writes and idempotency records must commit or roll back together. Use the creation callback time supplied by main's runner, sampled after the idempotency lock.
- **C4:** Preserve distinct discovery and lifecycle representations where precision or resource details differ. Do not mechanically merge them into shared types.
- **C5:** Retain behavioural tests, including concurrent retries, cancellation/deadline propagation, approval waiting behind Category edits and guarded migration rollback. Verify production routing through generated clients.
- **C6:** Main's shared idempotency migration and the following operational workflow migration are already integrated in this checkout. Do not rewrite applied migrations during the restructure.

## Decisions log for #88

- **D9: Baseline.** Merge current `origin/main` at `63c8884` non-destructively. Accepted #103 adds working admin operations. The only merge conflict was the contract test import block, resolved by retaining both `context` and `strings`. No applied migration was edited by the restructure.
- **D10: Representations.** Shared read/admin `Location`, `Clock`, `Coordinates`, `Building`, `Category`, `Disablement` and `Caller` preserve the existing discovery/admin fields. Discovery aliases these types. Workflow representations remain in lifecycle, including microsecond opening times, full Disablement state, and role-based Caller. Admin request/error names gain `Admin` prefixes where package colocation would collide.
- **D11: Common rules.** Only the identical finite/range coordinate rule moves into domain shared validation. Admin classification validation and proposal classification normalization retain their different behavior. No clock precision or resource representation is merged.
- **D12: Persistence.** Lifecycle PostgreSQL storage is beside its consuming interfaces. Accepted admin PostgreSQL storage reuses discovery's reader to hydrate the unchanged read/admin projection after locking. Its `AdminTx` and shared idempotency Runner remain unchanged in behavior.
- **D13: Mounting.** Main constructs all services, principal accessors and workflow interceptors. Router mounts discovery, accepted admin, Disablement and addition-request handlers once each. Workflow handlers no longer embed or mount discovery/admin stubs. Explicit generated stubs remain only in contract-test fixtures.
- **D14: Tests.** Move behavioral tests beside implementations. Extract the existing shared PostGIS fixture for discovery/admin tests and the signed-auth fixture for RPC/production tests. Rename colliding unit/persistence fixture identifiers without changing data. Production-composition E2E exercises signed JWTs, real PostGIS, generated clients and both supported server transports.
- **D15: Scope.** Preserve protobuf contracts, seeddb/userdb and applied migrations. Workflow retry consolidation is a following PR. Admin follow-up is a gap audit against #103, not a second implementation.
- **D16: Test-host contention.** Concurrent emulated PostGIS fixtures exceeded their existing 60-second startup wait. Serialized package runs reached readiness without changing production or fixture timeouts. `make test-integration` uses `-p 1`; avoid overlapping DB suites on this host.


## Verification for #88

- **V1: Units.** All Supplier Service packages passed `go test -p 1 ./... -count=1 -timeout=5m`.
- **V2: Database and RPC regressions.** Domain discovery/lifecycle, RPC health/discovery/lifecycle and shared idempotency passed real PostGIS integration tests with the race detector. This includes approval waiting behind committed Category edits, admin relationship/revision races, retries, cancellation/deadline errors and migration rollback guards. The original combined process exited 1 only because the new production fixture omitted the legacy REST mount-time Firebase dependency. The fixture correction changes no production code.
- **V3: Production composition.** `TestLocationProductionComposition` passed with real PostGIS, production dependency construction and router mounting, generated clients, signed JWTs verified by User Service middleware, admin retry, discovery after writes, scheduled cancellation, request approval, archive visibility, denied workflow access, Connect HTTP/1.1 and native gRPC h2c. Its public-key endpoint is a test fixture, not a running User Service. A credential-free Firebase emulator client satisfies unrelated REST mount-time construction; the test makes no Firebase calls.
- **V4: Generation and contracts.** sqlc v1.30.0 regeneration produces no diff. Protobuf contracts, generated protobuf clients, seeddb/userdb and applied migrations are unchanged by the restructure.

Source: supplier-service/cmd/server/location_integration_test.go. Reproduce from supplier-service/ with Docker available and no competing PostGIS suite:

```sh
go test -p 1 ./... -count=1 -timeout=5m
go test -p 1 -race -tags=integration ./internal/location/... ./internal/rpc/... ./internal/idempotency -count=1 -timeout=15m
go test -p 1 -race -tags=integration ./cmd/server -count=1 -timeout=5m -v
```

Local raw evidence is retained in /tmp/foc-location-orchestration/restructure-final-e2e.log and /tmp/foc-location-orchestration/restructure-production-proof.log. The latter command exited 0. The final source test is the durable repeatable proof.
