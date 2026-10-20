# Delivery proof

Status: user-approved design; partial feature-local implementation, NOT wired or feature-ready. Starting base: `e056db71ca814eb96aa45aea96cb82e5aeefedd2` (#150). No dependency merge has been performed. #148 pickup/receipt is not present.

## Publication authorization

Andrew explicitly requested publication now, treating UI #158 and acceptance/offers #159 as delivered dependencies and B3–B5 as downstream consumers. This supersedes waiting for unavailable shared owners before publishing B2. Target `andrew/feat/order-acceptance-offers` (#159), not the rewritten #150 branch. Inspected #158 head `049157b4f346b1ef0e8af9de9bc7146fd274bcf3` and #159 head `de1968e0f20174bf1f20fcb89986b0fbfd66d5ee`, including their PR descriptions and actual service/route/history contracts. Their unresolved findings are not waived by this dependency assumption.

The B1-to-B2 three-dot diff contains only the 15 B2 files; no UI/B1 code was copied or replaced. B2's original tests do not certify the unmerged combined stack. Publish as a draft for independent Opus review and downstream inspection, with persistence/RPC/UI integration, deployment capability and E2E limits explicit. Exact-model review remains required; publication is not feature-ready approval.

## Approved decisions

- D1 / Q1: the same upload ID identifies one proof. Return the same unexpired upload grant; refresh an expired grant without creating a proof. Stable object keys, immutable objects and #150 S3/Floci signing infrastructure remain authoritative.
- D2 / Q2: verify actual image decoding as well as stored length and media type. Metadata alone does not establish image validity. Enforce 1–10,000,000 bytes before upload. Use supported JPEG, PNG, WebP, HEIC and HEIF formats without claiming the photograph depicts the delivery.
- D3 / Q3: five item images and five delivery proof images are separate limits. This clarifies NFR-18.1's ambiguous combined wording.
- D4 / Q4: identical delivery confirmation retries return success without another mutation, including after receipt completion. Different proofs or unattended choice cannot replace the accepted confirmation.
- D5 / Q5: retain files and successful uploads in the page after failure. Retry or explicitly remove failed selections. Never silently omit them. Distinguish unknown upload result from a verified missing object.
- D6 / Q6, Q10: requester and delivering courier can read proof. A former courier removed before pickup cannot see later proof. Admin role alone is insufficient. Case-admin proof reads fail closed until authoritative assignment to an associated open report/appeal can be verified (NFR-06.3).
- D7 / Q7: NFR-06.4 automated retention is explicitly deferred and incomplete. It needs the latest associated request/report/appeal closure and open-case information. Do not delete merely 90 days after order closure. OrderOrchestrator owns external dependency escalation; B2 owns the proof integration boundary.
- D8 / Q8, Q11: a server-confirmed upload outage permits zero-proof confirmation for both handover and unattended delivery. Record a server-derived reason. A client assertion, invalid image, missing object, local signing/configuration failure or individual network failure is not sufficient evidence.
- D9 / Q9: under the order lock, reject first confirmation at `now >= picked_up_at + 2 * delivery_duration`. Earlier photo upload does not reserve confirmation. Recognized repeats remain successful. Outage does not bypass the deadline.

Q1–Q11 are resolved. Andrew explicitly confirmed implementation after the working-process explanation. Conservatively 15 independent questions were used. No round limit. Maximum 25 ordinary plus 5 buffer questions remains in force.

## Work boundaries and dependencies

Use `Mutator.Mutate` -> callback -> `Transition` -> `NewChange` for lifecycle changes. All object-store calls and image decoding occur outside order transactions. Recheck assignment, status, deadline and proof identity under the order lock before committing confirmation. Proof attachment, accepted proof set, unattended choice, outage reason, order revision, audit and status outbox fact must commit atomically. No new settlement path.

Reuse `images.ObjectStorage` through a consumer-owned proof boundary, with server-side bounded object reading and image validation. A storage availability authority must distinguish a confirmed storage outage from a bad file, missing object or configuration/authentication failure. No browser-controlled bypass.

Pending shared-file handoff: proof upload size/headers, proof verification/read and outage-aware confirmation protobufs/generated output; shared RPC server/composition; migration numbering and aggregate persistence integration. No shared edit before acknowledged ownership. Initial Messenger rejoin found only OrderProof, and DMs to OrderOrchestrator, OrderUI, OrderTimers and OrderAdmin returned not found. This blocks shared integration, not feature-local implementation.

OrderUI permanently owns shared frontend services, routes and detail-shell composition. B2 supplies feature-local UI and an explicit adapter request after a published exporter contract/commit. No guessed imports or silent cherry-picks. #148 integration and final stack base require orchestrator handoff.

## Acceptance and verification

- R1.7.1–R1.7.8: upload creation is not proof of stored bytes; valid uploaded proof or authoritative outage exception is required. Failed/unknown upload keeps Delivering. Retry identities and accepted confirmation remain durable and duplicate-safe.
- R1.6.4, R1.6.10: atomic Delivered mutation and strict two-duration boundary; cancellation/timeout cannot be overwritten after remote verification.
- NFR-06.3: authorize before signed reads, no role-only admin access. NFR-06.4 remains incomplete.
- NFR-18: supported actual image formats and byte/count limits. Bounds must also prevent excessive decoded dimensions.
- NFR-07/08/13: 360/768/1440 widths, visible focus, labelled controls, retained input and distinct Not Submitted/Pending/Succeeded/Rejected/Failed mutation feedback.

Targeted checks during development. At feature-ready, reserve E2E infrastructure and run a medium-hard repeatable failure/retry/privacy/race scenario. Separate exact `devin/claude-opus-5.5` design and implementation reviews are required; failed/unavailable review is a blocked gate, not a waiver. At most two full verify/triage/fix rounds. Do not describe fake-backed seams as live storage, persistence or browser E2E.

## Decisions log

- Fork: refresh expired grants while retaining proof identity, rather than preserve an unusable literal URL forever. User approved contract clarification.
- Fork: actual image validation replaces initial metadata-only recommendation. User approved.
- Fork: unattended outage behavior matches handover outage behavior. User explicitly rejected a separate unattended restriction.
- Blocker: no reachable shared-file owners or UI exporter handoff at implementation start. Continue feature-local code and tests. Shared integration and final feature readiness remain blocked unless handoff arrives.
- Significant decision: use the existing ImageMagick/libheif codecs rather than implement codecs. The validator forces an allowlisted input coder, processes actual pixels, limits dimensions/cache/threads/output/time and has two decoder slots. Proof admission occurs before allocating file bytes and rejects saturation rather than queueing full image bodies. These are not hard native-process isolation. Production must supply a restricted ImageMagick policy and process/container memory, CPU and PID controls, and require all five format checks without skips before enabling the feature.
- Operational default: allow at most 20 staged proof identities per order/courier. This is separate from five accepted proofs. Reuse of an existing identity bypasses new-admission quota. A real adapter must enforce this count under the parent lock and implement fenced orphan cleanup. Neither the PostgreSQL adapter nor cleanup worker is present yet.
- Significant decision: bound each browser/API/storage operation to 15 seconds, inspect unknown PUT results and abort work on unmount. Unknown confirmation freezes its exact input for retry. A known authoritative rejection releases the frozen input. The shared adapter must forward cancellation and distinguish authoritative rejection from unknown transport/authentication outcomes.
- Blocker: the original #150 branch was rewritten upstream. Refreshed `origin/yihao/feat/order-creation-images` is `d91310b379a337c8dc40cb4168b3ba25f9ebf511` and no longer contains the pinned `e056db7`. Do not rebase/merge silently or select a misleading PR base. The orchestrator must provide an updated integration handoff.

## Explicit integration request (not implemented contracts)

B2 requests acknowledged ownership for these shared changes before editing:

- `CreateProofUploadRequest.size_bytes = 4`, exact 1–10,000,000-byte validation; `CreateProofUploadResponse.upload_headers = 4`. Stable upload identity is scoped by `(order_id, courier_id, upload_id)` and immutable size/type. An unexpired generation must produce the identical grant across replicas/restarts. Signing epoch alone is insufficient across credential rotation. The current `ProofGrantIssuer` is an unimplemented dependency, not proof this promise has been solved. Do not persist bearer grants contrary to the foundation ADR without an explicit reviewed exception.
- A proof-upload verification RPC and a separately authorized proof-read RPC. B2's application methods are `VerifyUpload` and `ForOrder`. No item-image read authorization may be reused for proof. Missing/invalid proof remains failed_precondition, dependency errors remain unavailable, and the frontend must distinguish confirmed absence from an unknown upload outcome.
- Permit zero proof IDs in the protobuf shape only with an explicit outage-exception request, never a client-provided reason. Application validation requires either 1–5 unique proof IDs or zero IDs plus `WithoutProof`. The server obtains scoped evidence with an ID, generation, bounded validity and server-owned reason. `IsProofOutageCurrent` must lock the authoritative generation through transaction commit so recovery publication and confirmation have a defined serialization order. Generic HEAD/GET failures, signing errors and a client failure do not prove PUT unavailability. There is no concrete outage authority or local outage-table adapter yet.
- Allocate migration number and sqlc changes for upload identities/signing metadata/staging states, immutable accepted confirmations and authoritative outage evidence. `ProofTx` must share the exact aggregate transaction. Parent locking precedes fresh proof reads; attachment/state fencing/accepted input/audit/outbox/order revision commit together. Persistence errors poison commit. `uploading -> accepted` cannot race a cleanup tombstone (`deleting`). No migration or concrete persistence implementation has been added.
- Compose proof operations and typed RPC adapters in the shared Order server. Do not recreate #148 pickup/receipt or #149 notifications. A future `NewDecoder` instance is shared rather than created per request. Enabling upload grants must depend on real decoder capability, not leave users with uploads that cannot later be verified.
- OrderUI supplies the shared API adapter and mounts `DeliveryProofForm` through its existing detail-shell action snippet, keyed by caller/order and only for the assigned courier while Delivering. The feature-owned `DeliveryProofAPI` takes abort signals; success invokes `onDelivered` to refresh current state. No shared singleton, route or detail-shell code has been changed. The exporter at `origin/andrew/feat/order-ui-foundation` was inspected at `049157b`; this is not an acknowledged B2 consumer handoff.

## Verification status

First round: existing and new targeted Go suites passed with the race detector, including genuine JPEG/PNG/WebP/HEIC/HEIF decode fixtures. Root lint found one unchecked close; frontend check found a Svelte rune-name collision. Both were corrected before the second round. Review also identified bounded-wait/admission issues; those changes and regression tests are included in the second round.

The second/final round passed: `make fmt && make lint` returned zero issues for all four modules; `cd order-service && GOWORK=off go test -mod=readonly -race -json ./... -count=1` passed 206 cases with zero failures; `cd frontend && npm run check && npm test && npm run build` reported zero Svelte errors/warnings, 61 passing tests across 10 files and a successful production build. The build emitted dependency browser-externalization and adapter-auto deployment notices; no claim is made that these dependency/deployment warnings were fixed. No application-code changes followed this successful round. Ignored `.local/order-proof-verification/` retains logs, Go JSON results and a SHA-256 source manifest. Backend commit: `fa3d32e`; feature-local UI commit: `e503675`.

The required exact independent review is BLOCKED, not waived or approved. Do not count an unverified model invocation as the required review. No Docker, browser, live S3, real PostgreSQL proof persistence, authorization issuer, real-case service, Kafka/Telegram consumption or integrated end-to-end result is claimed. UI viewport/focus behavior and reviewer-accessible screenshots/video remain unverified and absent until the feature can be integrated and E2E infrastructure is allocated. The upload size and retry-stable signing adapter are still missing, so NFR-18 and R1.7 are not claimed complete at the live API seam. Retention remains explicitly incomplete.
