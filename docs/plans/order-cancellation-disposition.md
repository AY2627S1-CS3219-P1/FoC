# Order cancellation and disposition

## Scope

B3 owns participant cancellation, durable pre-pickup withdrawal, final item location,
and pickup/receipt frontend integration. Suspension implementation is a separate
PR. No new service, automatic suspension, deadline worker or timer clock reset is
part of B3.

## Accepted decisions and acceptance sources

- Q1: withdrawal retains the original reward reservation. Revert the accepted
  assignment adjustment and confirm a matching Held baseline before reopening.
- Q2: cancellation finishes without waiting for final location. The assigned
  courier sees a persistent location task; requester sees location pending.
  Do not invent a deadline or additional penalty.
- Q3: allow audited final-location corrections. Identical normalized submissions
  write no new revision, audit, outcome or warning.
- Q4: audit explicitly records whether pickup occurred and the courier holds the
  item. No-custody text is not stored as a final location.
- Q5: cancellation is bound to the confirmed revision. Pickup between confirmation
  and execution rejects that old intent rather than changing its credit effects.
- Q7: courier cancellation after pickup and delivery timeout request warnings.
  Credit owns settlement. Publication is not evidence of User Service consumption.
- Q8: location recording also applies to timeout failures after pickup.
- Q15: retained matching intent replays before current revision/participant checks.
  A withdrawal replay cannot remove a replacement courier.
- Q9-Q14: immediate suspension, pre-pickup removal, continued post-pickup handling,
  continuing requester orders, admin-only suspension decisions and first-effective
  pickup/suspension ordering are for the separate suspension PR, not implemented here.

Additional acceptance sources: R1.3-R1.5, C1.2.11-C1.2.13, existing RPC trimmed
Unicode limits, NFR-07 viewport widths, NFR-08 labels/focus, NFR-13 retained input
and mutation feedback, and the status-event previous-courier field.

## Prior art and integration

PR171 at `59dd1b9` implements durable cancellation on B1 `de1968e`.
Reuse that workflow rather than build a competing financial coordinator. Reuse
pickup/receipt application commits `40d4ad3` and `f14e6a2` from PR148, preserving
F0 privacy projection and B1/cancellation composition during integration.

B2 PR177 at `e867613` is explicitly partial/unmounted. B3 does not recreate its
proof operations or claim its persistence/RPC integration is complete. B3 E2E
preconditions may seed a delivery accepted at that external seam.

## B3 interfaces for later features

- `CancelOrderRequest.expected_revision = 4` is a positive int64: the revision the
  participant confirmed. `idempotency_key = 3` remains optional for compatibility;
  B3 UI always supplies a stable UUID and never changes its revision/reason/key
  on uncertain retries. Keyless callers must not automatically retry uncertainty.
- `Canceller.CancelConfirmed` compares retained payload before current revision.
  A new mismatched revision returns conflict without admission. Already-retained
  pre-upgrade keys may match the original order/reason hash, but must still load
  an operation matching caller, order and reason. Never rewrite their hash or
  admit a new legacy intent through this compatibility path. New withdrawals
  are refused if Credit is unconfigured; requester terminal cancellation remains
  available. This fixes the verified PR171 stranded-withdrawal review finding.
- Durable `Cancellation` retains immutable original assignment identity. Pending
  withdrawal keeps the current courier and blocks pickup. Recovery leases and
  generation checks fence late replies. Only acknowledged Revert plus matching
  Held reservation can clear courier identity and restore the original reward.
- B3 never modifies `expires_at`. It consumes the stored deadline without inventing
  a reset policy or implementing B4. Later timer work owns its approved checks.
- `Tx.Apply` remains the sole aggregate/history/audit/outbox writer. Do not add a
  second assignment-history writer or settle an opened Order through Credit RPC.
- `Disposition.RecordItemLocation` uses `Mutator` then same-status `NewChange`,
  not terminal `Transition`. Authorization is current assigned courier only.
- `NewServerWithCancellation(...).WithParticipantActions(progress, disposition)`
  composes adapters. Every successful mutation reloads current caller-authorized
  state and uses `shared.OrderView`; history access grants no sensitive data.
- Timeout producers call `NewChange` with System/Timeout at the approved cutoff.
  It emits one warning encoded as `ZERO_PAYABLE_REWARD = 2`. Courier cancellation
  remains `CANCELLED_AFTER_PICKUP = 1`. This is a fact contract, not a timer worker.
- Feature-local `ParticipantActions` uses `participantActionsApi` on the existing
  protected Order transport and F0's detail action snippet. Shared composition
  preserves B1 offer controls. Always refresh current Order after a mutation.

## Verification plan

- Run repository formatting/lint, schema lint/generation, full Order race tests,
  frontend type checks, unit tests and production build.
- Use disposable PostgreSQL18 and mounted Connect to verify lost withdrawal
  replies, restart recovery, pickup fencing, stale cancellation rejection,
  replacement-courier privacy, terminal warnings and audited location corrections.
- Exercise concurrent location retries and rollback on audit insertion failure.
- Exercise mounted browser controls at explicit Connect fixtures: saved retry after
  reload, stale confirmation, requester cancellation during withdrawal, definitive
  refusal, retained location input, pickup and receipt, labels/focus and 360/768/1440px.
- Attach repeatable evidence and review disposition to the PR. Scripted seams do
  not prove live Credit, workload authorization, User warning consumption, B2 proof
  persistence or Telegram delivery.
