# Supplier Location code structure

## Decision

Each Location transport package owns one protobuf service. Domain packages split along independent transaction stacks. Discovery is unchanged. Admin owns its own store and transaction. Disablement and addition requests remain together in domain lifecycle because approval uses their shared transaction.

## Execution

1. **A1, #117:** Extract existing domain and RPC admin code into admin packages. Update imports, server wiring and the shared-domain dependency boundary. Keep authorization and public behavior unchanged.
2. **A2, #118:** Merge #117 forward after publication. Do not rebase published branches or duplicate the extraction here.
3. **A3, #119:** Split RPC lifecycle into disablement and additionrequest packages, each with Server and a consumer-owned operations interface. Rename WorkflowInterceptor to CallerInterceptor and keep method authorization rules owned by the corresponding capability. Delete RPC lifecycle after the split.

## #117 structure

Paths are relative to supplier-service/internal/.

```text
location/
  admin/
    service.go
    service_test.go
    postgresstore.go
    postgres_integration_test.go
    input.go
    patch.go
  discovery/                 unchanged
  shared/                    domain resources and validation
rpc/location/
  admin/
    handler.go
    handler_test.go
    handler_integration_test.go
  discovery/                 unchanged
  shared/                    conversion and error mapping
```

The RPC admin package exposes Server and NewServer. Its LocationAdmin interface lists only the operations the adapter consumes. The admin domain package exposes Service, Store, Tx and PostgresStore; the package name supplies the admin context.

## Decisions log

- **D1:** Apply the extraction on #117, not #118, so the structure originates at the bottom of the published PR stack.
- **D2:** Use service.go for domain operations, postgresstore.go for the PostgreSQL store and handler.go for the RPC adapter. This refines the ADR's original filenames to address the two naming review comments. Filenames identify the implementation's role. Discovery filenames stay unchanged as required by the ADR.
- **D3:** The plan file did not exist on #117. Create it at the requested path rather than modifying another worktree's document.

## Verification

From supplier-service/, with Docker available:

```sh
GOWORK=off go test -p 1 -race ./...
GOWORK=off go vet ./...
GOWORK=off go test -p 1 -race -tags=integration ./internal/location/... ./internal/rpc/... ./internal/idempotency ./cmd/server -count=1 -timeout=15m
```

The signed RPC and database tests exercise admin create, replay, update, archive, unarchive and role restrictions. Domain integration tests cover rollback, concurrent revision checks and relationship freshness after a lock wait. Existing RPC error-response tests protect client-facing codes and messages.

Proto contracts, migrations, SQL queries and generated code are untouched. No client regeneration or Buf run is required.
