# ADR: Location package structure — one package per capability

Date: 7 Oct 2026
Status: accepted, revised to split domain capabilities
PRs affected: #117, #118, #119

## Intent

1. Package and type names must name what the thing is. "Workflow" described a folder
   that no longer exists; nothing in the current system is called a workflow except
   leftover identifiers.
2. The transport layer should mirror the protobuf services one-to-one:
   Discovery, Admin, Disablement, AdditionRequest.
3. The domain layer should split along real seams — packages that own their own
   transaction stack — not cosmetic file grouping.
4. Land the structure through the open PR stack (#117 is the structure PR and
   yihao's review blocks on standardization anyway), not as a post-merge cleanup.

## Decision

Transport — `supplier-service/internal/rpc/location/`:

```text
discovery/          unchanged
admin/              from lifecycle/admin.go + tests        (#117)
additionrequest/    from lifecycle/additionrequest.go      (#119)
disablement/        from lifecycle/disablement.go          (#119)
shared/             principal, validation, error mapping,
                    time and page conversions, CallerInterceptor
```

`rpc/location/lifecycle/` is deleted. Each package exposes `Server` (was
`WorkflowServer` / `LocationAdminServer`); the split of `WorkflowOperations`
becomes per-package consumer interfaces; `WorkflowInterceptor` becomes
`CallerInterceptor` with per-package method-authorization rules.

Domain — `supplier-service/internal/location/`:

```text
discovery/          unchanged
shared/             common domain resources, caller, page and error values
admin/              admin.go, adminpostgres.go, input.go, patch.go + admin tests   (#117)
disablement/        own service, Repository/Tx and PostgreSQL adapter (#118)
additionrequest/    own service, Repository/Tx and PostgreSQL adapter (#118)
```

`location/lifecycle/` is deleted. Each capability owns a consumer-defined transaction interface. Shared contains no repository or service. Proto contracts, migrations and generated
code are untouched — no client regeneration, no `buf` run.

## Why

1. Transport features share nothing but dumb helpers, so the split costs nothing
   and each package owns its own authorization rules (admin and disablement are
   all-admin; addition requests mix owner and admin rules).
2. Admin has its own persistence stack (`PostgresAdminStore` with its own
   `Within`/`AdminTx` on `locationdb`) and shares zero symbols with the workflow
   code, verified by grep in both directions. It splits cleanly out of the domain
   package.
3. Approval runs `Request` + `ValidateReferences` + `CreateLocation` +
   `SaveRequest` inside one AdditionRequest transaction. It calls no Disablement
   operation and does not invoke the idempotency runner. The previously combined
   `Tx` interface was an implementation choice, not cross-feature coupling.
4. Disablement and AdditionRequest therefore split in the domain too. Each owns
   only the persistence methods it consumes. Both reuse idempotency storage,
   generated SQL queries and common resource values without importing each other.

## History

On 30 Sep 2026 the package was renamed `workflows` → `lifecycle` ("lifecycle
describes the responsibility more precisely than workflows" — session
2026-09-30T17-25-05, location-workflow-rpc worktree); `workflow.sql` became
`lifecycle.sql` and `workflowdb` became `lifecycledb`. The type names kept the
`Workflow` prefix through the move — that debt is what this decision pays off.

## Execution order

1. #117: both admin moves (transport and domain) + update
   `docs/plans/supplier-location-code-structure.md`.
2. #118: merge #117 forward and split domain lifecycle into independent
   `disablement/` and `additionrequest/` packages, retaining atomic approval.
3. #119: transport split into `disablement/` and `additionrequest/`, interceptor
   split, delete `rpc/location/lifecycle/`, update router and server wiring.

Branches are published: merge forward, never rebase. yihao re-reviews after
#117 lands its moves.
