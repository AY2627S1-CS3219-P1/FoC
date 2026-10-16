# Order/Credit conformance and creation handoff

Baseline: [proposed ADR](../adr/order-credit-orchestration.md),
[alignment #136](https://github.com/AY2627S1-CS3219-P1/FoC/issues/136),
[Order #123](https://github.com/AY2627S1-CS3219-P1/FoC/pull/123),
[Credit #129](https://github.com/AY2627S1-CS3219-P1/FoC/pull/129).

These are the integration acceptance cases. Contract tests live in
`order-service/internal/contracts/ordercredit`. Executable Order creation/recovery
coverage now lives in `order-service/tests/creation`, using PostgreSQL and mounted
Connect handlers with scripted remote outcomes. See the
[creation ADR](../adr/order-creation-recovery.md) for implemented boundaries.
Credit persistence, assignment/terminal workflows and Kafka remain follow-ups.
Run the remaining cases against their actual owning implementations when they land. Use a controllable clock,
barriers around remote/local commits and process restarts rather than sleeps.

## Contract checks implemented here

- All seven keyed Order requests require UUID keys; empty/malformed keys fail.
- Malformed Order, counteroffer, assignment, requester and courier identifiers fail.
- Existing field numbers/types remain fixed; Credit's draft `request_id` and
  `offer_id` names become `order_id` and `assignment_id` at the same numbers.
- Required timestamps, valid positive amounts/durations, settlement outcome oneof,
  delivery chronology and release reasons remain validated.
- A structurally valid creation request with a past deadline passes declarative
  validation, while a missing timestamp fails. Only the future operation
  decides whether it is an identical replay or an ineligible new creation.

## Client-key workflows (Order)

| Case | Required observation |
| --- | --- |
| Identical retry | Same caller/method/key and canonical payload reuse the workflow and original identities/result; no duplicate remote operation or local revision. |
| Equivalent encoding | UUID representation and typed timestamp/duration encoding differences normalize consistently; repeated order and optional presence follow persisted canonicalization. |
| Changed payload | Same caller/method/key with different business data returns `already_exists`, with no side effect. |
| Scope | The same UUID from another caller or method does not collide with the first mapping. |
| Concurrent duplicates | Many concurrent identical requests create exactly one workflow/Order/assignment; all attach to that outcome. Changed concurrent input conflicts. |
| Retention | Resolved mappings replay for at least 24 hours; unresolved mappings remain pinned after 24 hours and after restart. |
| Business rejection | Identical retry retains the rejection after credits increase. A new key may start a new attempt only on an eligible resource. |
| Non-business failure | Authentication, malformed input and transient infrastructure errors do not become sticky business refusals; existing durable intent remains recoverable. |

## Permanent operations (Credit)

| Case | Required observation |
| --- | --- |
| Identical/changed retries | Test every mutation: original result replays permanently; changed data for the same method/identity yields `already_exists` and `IDEMPOTENCY_CONFLICT`. |
| Concurrent duplicates | Atomic deduplication/balance/outcome commit; exactly one financial change and ledger sequence. Repeat across process restart. |
| Sticky insufficient credits | Reserve/Adjust insufficient-credit rejection is persisted. More available credits do not change a replay. New Order/assignment identity is needed for a deliberate eligible retry. Existing hold survives rejected adjustment. |
| Historical Reserve replay | Reserve succeeds, release succeeds, Reserve replays original success; `GetReservation` remains Released with zero hold, without a second reserve ledger entry. |
| Historical Adjust replay | Adjust A succeeds, revert A succeeds, replay Adjust A: original success, no reapplied hold/assignment. Current state comes from GetReservation. |
| Release before Reserve | Release unknown Order, restart, deliver a delayed Reserve: Released tombstone survives and blocks creation of a hold. Also race release against an in-flight Reserve commit; final hold is zero in either ordering. |
| Revert before Adjust | Revert unknown assignment, including before Reserve, restart, then delay Adjust: tombstone survives and blocks applying it. Other assignment IDs remain eligible when Order permits them. |
| Old compensation | Revert A, apply B, deliver duplicate/late revert A and replay success A: B remains active and its hold unchanged. |
| Unknown read | Pause Reserve before commit; GetReservation may be not_found; resume Reserve. The read must not be treated as a no-future-effect guarantee. |
| Competing terminal actions | Race settle/release, two settlement outcomes, and changed release reasons. Exactly one terminal financial effect; identical winner replays, same-method changed payload conflicts, competing method receives typed terminal-state rejection. |
| Payout calculation | Credit calculates on-time full reward, intermediate lateness with floor rounding, zero at twice the duration, full-reward requester cancellation after pickup, and exact requester refund. Order does not supply a computed payout. |
| Typed errors | Each business rejection exposes the documented ErrorInfo domain/reason/code. Transport/auth/validation failures remain distinct. |

## Creation and crash recovery (Order with Credit)

| Case | Required observation |
| --- | --- |
| New expired request | No retained mapping, deadline <= operation clock: invalid_argument, no Order/workflow/Reserve. |
| Replay after expiry | Admit creation with a future deadline, then replay identical input after that deadline within retained mapping lifetime: retrieve/resume original workflow rather than reject at validation or create another Order. Cover resolved and pending workflows. |
| Confirmed Reserve before expiry | Apply response only after re-lock/workflow check and fresh deadline check, opening once as Awaiting Courier. |
| Definitive Reserve refusal | Rejected, no release call, no settlement outcome fact; identical retry remains refused after balance changes. |
| Unknown Reserve before expiry | Remain Pending Reservation; retry/reconcile the same Order UUID and data, including after not_found. Never treat timeout as insufficient credits. |
| Expiry during Reserve | Persist abort intent first, call Release(EXPIRED) even if Reserve result is unknown. Keep Pending Reservation until release is confirmed, then reject. Never open. |
| Reserve before abort, response after abort | Remote success may arrive late; the workflow check refuses opening and continues durable release. |
| Release before delayed Reserve | Delayed Reserve is fenced by Credit's tombstone. Replayed historical success cannot reopen the aborted Order. |
| Crash before dispatch | Order, mapping and intent are committed together; recovery dispatches exactly that identity/data. |
| Crash after remote success, before local commit | Recovery queries/retries original identity, rechecks deadline/phase under lock and opens once or finishes release. No stranded hold or duplicate Order. |
| Crash after abort intent/release | Restart before dispatch and after remote release success; retry Release and reject only after confirmation. A crash after local confirmation also replays without new effects. |
| Changed retry after deadline | Existing key with changed payload conflicts before a new-operation deadline check can replace the workflow. |

## Assignment and terminal follow-ups

Persist a new assignment UUID for direct and counteroffer acceptance. Test delayed
success after a lost race, durable revert before another attempt, old compensation
arriving after reassignment and refusal without disturbing the original hold.
Withdraw/admin-unassign must block pickup while revert is pending and restore the
amount Credit returns. Inject stale responses to verify workflow identity/phase
checks after re-lock. Terminal transition, immutable observations, financial
intent, audit and lifecycle facts must roll back or commit together before remote
calls; a lifecycle fact never certifies financial completion. Recovery must retain
unresolved work through Credit downtime. Kafka relay tests belong to that relay;
Credit consuming OrderOutcome must have no financial effect.

## Service authentication prerequisite

Use the dedicated Order bearer credential for every reservation method. Verify
missing, forged, user-JWT, expired and wrong-audience credentials are rejected,
valid Order credentials pass, and rotation/revocation work. Refuse HTTP outside
explicit local development and never persist/log credentials. See
[workload identity #29](https://github.com/AY2627S1-CS3219-P1/FoC/issues/29).

## Required repository checks

Run `make buf-lint`, `make buf-generate`, generation reproducibility checks,
`make fmt`, `make lint`, isolated module tests (`GOWORK=off go test -mod=readonly
./...` per module), and frontend check/test/build. Use writable Go/lint caches in
the sandbox, as described by `LANDMINES/sandbox-go-caches.md`.
Run `buf breaking` against the actual Order/Credit draft bases as well as the PR
base before merge. Field renames preserve binary wire compatibility but change
JSON names and generated Go/TypeScript APIs, so FILE breaking checks against the
Credit draft are expected to report them. Coordinate these draft renames before
clients deploy; do not suppress the repository's global breaking policy.
