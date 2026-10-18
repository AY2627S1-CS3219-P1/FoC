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
- D11: central transaction detection of courier assignment/removal is the proposed
  writer seam. Keep episode history in the same local transaction as Order/audit/
  outbox, rather than requiring three feature writers to duplicate SQL. Exact
  schema, migration number, historical backfill and ownership await acknowledgment.
- D12: use snapshot building names for the initial coarse filter because stored
  Order snapshots do not contain stable Building IDs. Do not resolve an exact
  destination ID into a coarse filter on a caller's behalf.

## Ownership and handoff

F0 retains shared frontend services/routes/composition/detail shell and the safe
read/history design. B1 owns acceptance/offers, B2 delivery proof, B3 cancellation/
disposition and existing pickup/receipt frontend controls, B4 timers and B5 admin
operations/case authorization. No peer backend is merged or recreated here.
Shared backend proto, conversion, read, persistence and migration ownership must
be acknowledged before editing. Publish exact exports and exporter commit, and
acknowledge feature-local integration requests with consumer commits.

## Current blockers and status

- ADR committed as `c378d61`; draft PR #158 published on #150's branch.
- Messenger rejects sends because the session message budget is exhausted.
  Shared-backend ownership/numbering request is posted on PR #158 as a fallback,
  not treated as acknowledgment. Frontend work proceeds under existing F0 ownership.
- Shared frontend exports/read pages are committed as `53b5e7d`; creation and
  repeatable browser evidence as `0904eef`. The delivery projection policy and
  its unit test are `7e302ce`, but no production handler calls it yet.
- Backend safe read/mutation wiring, coarse destination contract/filtering,
  assignment episodes/backfill/list-count membership and item-image authorization
  integration remain unimplemented pending shared-file acknowledgment. The coarse
  frontend filter is disabled, and the adapter rejects it rather than silently
  dropping an unsupported protobuf field.
- Exact-model review is still a completion gate. The Agent override recorded
  `openai-codex/gpt-6.1-sol`, so that run does not count. A direct CLI invocation
  explicitly selecting `devin/claude-opus-5.5` is being attempted without fallback.
- `npm run check`: 0 errors/warnings. `npm test`: 11 files, 61 tests passed.
  `npm run build` passed before the final picker/sign-out layout fixes; current
  source passed the second type/unit/browser round. `make fmt && make lint` passed
  with the pinned linter (0 issues in each module). Full Order Go tests passed.
- Browser evidence passed at explicit Connect/S3 fixtures: retained image after
  lost upload response, explicit invalid-image removal, lost CreateOrder response,
  same-payload/key/image-ID recovery after refresh without re-upload, authoritative
  status refresh, suspended history, no page exceptions and no horizontal scrolling
  at 360/768/1440px. Source: `frontend/tests/order-ui-e2e.py`. Report/screenshots/video:
  ignored `.local/order-ui-evidence/`. This is frontend seam evidence, not real
  backend authorization/history, S3 signatures, Credit settlement or Telegram.
- First browser triage exposed a narrow-screen Location picker layout issue. The
  second round passed after fixing the picker/sign-out layout. No third broad
  verification round is planned. Independent-review fixes, if any, must be labelled
  unverified unless the user authorizes additional verification.
