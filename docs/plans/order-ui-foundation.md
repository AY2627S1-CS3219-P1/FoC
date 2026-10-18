# Order UI foundation execution

Base: `e056db71ca814eb96aa45aea96cb82e5aeefedd2` (#150).
Draft: #158. Policy: [Order read privacy and assignment history](../adr/order-read-privacy-and-assignment-history.md).
User confirmed the complete scope before implementation.

## Delivery increments

1. Backend-enforced safe destination projection/filtering and durable assignment
   history, shared authenticated Order services, browse/detail/My Orders routes,
   and stable exports for feature-local controls.
2. CreateOrder with Location pickers, signed direct-to-S3 item uploads, advisory
   Credit account read, stable retry identity and same-tab submitted recovery.
3. Targeted checks, exact independent Opus 5.5 review, coordinated medium-to-hard
   repeatable E2E, atomic commits and stack-aware PR updates.

## Acceptance sources

- R1.2.3: browse eligible open requests, with pickup and coarse destination filters.
- R1.8.1–3 and U6.7: retained participant history, requester/courier/status filters,
  status/participant/details display, including suspended-user reads.
- NFR-06.2 and user clarification: backend redaction of exact destination ID/name/
  floor/instructions outside requester/current courier/authoritatively assigned
  open-case admin. History is not sensitive access or mutation authority.
- R1.1.1–9/12: creation fields, image attachment, confirmed reservation before open,
  distinct Location IDs. R1.1.10–11 rating features remain deferred externally.
- NFR-07: no horizontal page scrolling at 360, 768 and 1440 pixels. Other browser
  engines and touch claims require actual evidence, not Chromium inference.
- NFR-08.2/3: visible focus and labelled controls.
- NFR-13.1–7.1: field correction guidance, retained input, retry and distinct Not
  Submitted/Pending/Succeeded/Rejected/Failed feedback. Network uncertainty is not
  definitive rejection or permission to change a submitted payload/retry key.
- NFR-18.1/2/5: five images, 10,000,000 bytes each, pre-upload validation.

## Decisions log

- D1: deliver shared reads before creation so feature owners can integrate early.
- D2: enforce destination privacy and coarse filtering in backend responses, not
  just frontend rendering. Fail closed for case-admin exact access until B5's
  authoritative dependency exists. Do not implement Reports/Appeals here.
- D3: retain former assignment history separately from current mutation authority.
  A former pre-pickup courier does not see later proof/exact location/instructions.
- D4: one account can act in both roles. No global requester/courier mode.
- D5: server-authoritative detail refresh after actions, tab return, 15-second
  visible active-page polling and one refresh at countdown completion. Never
  synthesize a terminal state from a browser clock.
- D6: no upload-status RPC. Explicit CreateOrder verifies images after an uncertain
  upload. No hidden creation request is used merely to probe image existence.
- D7: caller-scoped same-tab storage retains submitted values/image IDs/retry key,
  not unfinished drafts, image files, signed URLs or credentials. Clear on logout
  and definitive resolution. Refuse unsafe automatic replay beyond retry lifetime.
- D8: never silently omit selected failed images. Retain successful image IDs and
  form input; require explicit retry or removal after a definitive failure.
- D9: display advisory Credit available balance using the existing account RPC.
  Backend reservation is authoritative. An unavailable balance is not zero.
- D10: proposed routine UI defaults are delivery within 30 minutes of pickup and
  expiry 60 minutes from form opening, both editable and revalidated at submit.
- D11: central transaction detection of courier assignment/removal is the agreed
  writer seam. Keep episode history in the same local transaction as Order/audit/
  outbox, rather than requiring three feature writers to duplicate SQL. Migration 00004 records evidenced assignment/removal and current snapshots.
  Missing historical assignment times/revisions remain unknown. The existing
  `Tx.Apply(ctx, change)` is the only writer seam; feature writers add no calls.
- D12: use snapshot building names for the initial coarse filter because stored
  Order snapshots do not contain stable Building IDs. Do not resolve an exact
  destination ID into a coarse filter on a caller's behalf.

## Ownership and handoff

F0 retains shared frontend services/routes/composition/detail shell and the safe
read/history design. B1 owns acceptance/offers, B2 delivery proof, B3 cancellation/
disposition and existing pickup/receipt frontend controls, B4 timers and B5 admin
operations/case authorization. No peer backend is merged or recreated here.
Shared backend ownership is acknowledged in [PR #158 comment 6249016359](https://github.com/AY2627S1-CS3219-P1/FoC/pull/158#issuecomment-6249016359).
F0 owns migration 00004, read/projection/filter queries and the sole Postgres Apply
history hook. B1 owns migration 00005 and feature-local assignment workflow files.
B1, B3 and B5 consume `Tx.Apply` without history SQL or a second history method.

### Concrete backend contracts

- `Order.delivery_building_name = 23`; `ListOpenOrdersRequest.delivery_building_name = 5`.
  Legacy exact `delivery_location_id = 2` remains deprecated and is rejected even
  when explicitly present but empty. Proto/generated commit: `2e663e1`; verified backend: `6066038`.
- `Reader.GetWithParticipation(ctx, orderID, callerID) (Order, bool, error)` reads
  Order and history in one repeatable-read snapshot. `Service.GetOrder` authorizes
  visibility, then projects. List/count use the same `EXISTS` membership predicate.
- `Tx.Apply(ctx, change)` detects changed courier identity from its validated locked
  baseline, closes/opens assignment episodes, and commits with Order/audit/outbox.
  History failure poisons the transaction even if a caller swallows the error.
  `Order.HasParticipant` stays current-only. A retained terminal courier remains
  the current snapshot participant; completion does not fabricate a removal.
- `shared.OrderView(ctx, caller, order, images)` applies response-only privacy to
  all read/mutation responses. Nonparticipants get explicit coarse area and absent
  `delivery`, no disposition/closure text, and no later attachment URLs. This is
  projection, not visibility authorization. Replay handlers first call
  `Service.GetOrder`; omit Order on `ErrNotFound` for offer-only/failed attempts.
- B1 reservations: `AcceptOrderRequest.attempt_id = 2`, response `attempt = 2`,
  `AcceptOfferResponse.attempt = 3`, separate `AssignmentAttemptResult` and status.
  Integration/regeneration waits for the full verified backend handoff, not the
  proto-only commit. No peer merge/rebase/force-push is performed by F0.

## Current blockers and status

- Shared ownership is resolved. Backend implementation passed its final verification round after the first
  round identified intentional deprecated-field access requiring a localized
  staticcheck suppression. No further broad verification round is planned for this increment.
- `make fmt && make lint`: zero issues in all four modules. Full Order Go unit
  suite passed. Race-enabled PostgreSQL18 aggregate/RPC/shared suites passed,
  including migration up/down/up, evidence backfill, repeated assignment/removal,
  list/count uniqueness, forced history failure rollback and mounted Connect
  reads after withdrawal/reassignment/completion. Authentication identity is
  injected at the existing middleware seam, not tested against a live issuer.
- Updated frontend: type check zero errors/warnings, 62 tests, production build
  passed. Destination selector now uses the actual coarse field, not the retired
  exact filter. The final fixture browser run timed out resolving the exact destination
  select label. Its selector was adjusted to a substring match but not rerun
  after the second verification round. Earlier recovery-fixture evidence does
  not certify this latest browser source.
- Backend repeatable proof: `order-service/internal/order/assignmenthistory_integration_test.go`
  and `order-service/internal/rpc/order/readprivacy_integration_test.go`. Captured
  logs/report: ignored `.local/order-ui-backend-evidence/`.
- Browser recovery proof remains explicitly at Connect/S3 fixtures, not production
  authorization or S3/Credit/Kafka/Telegram integration. Source:
  `frontend/tests/order-ui-e2e.py`; local artifacts: `.local/order-ui-evidence/`.
- Exact independent read-only review completed in native Devin session
  `swamp-captain`, runtime version `3000.11.92`. Export metadata records
  `Claude Opus 5.5 Medium` and inference `generation_model=claude-opus-5-5-medium`.
  Final findings: no confirmed correctness/security bugs; one stale NFR-06.2
  traceability entry, corrected in `order-service/REQUIREMENTS.md`. Unconfirmed
  concerns are not promoted to defects. Full export and sanitized provenance:
  ignored `.local/order-ui-exact-review/`. The reviewer did not run tests and
  did not certify peer integration or the browser-selector adjustment.
  Earlier wrong-model/silent attempts do not count toward this completed review.
- B5's authoritative open-case assignment is external. Admin-only sensitive access
  fails closed. Peer lifecycle controls/recovery workers and proof authorization
  require their own integration/verification and are not certified here.
