# Delivery proof

The proof API, persistence, recovery worker and Order-detail controls are mounted on the B3 dependency (#179). Downstream lifecycle timers are owned by #183. Retention and case-admin dependencies remain deferred. See [observed verification and limitations](../explanations/delivery-proof-verification.md).

## Approved decisions

- D1: the same upload ID identifies one proof. Return the same unexpired upload grant; refresh an expired grant without creating a proof. Stable object keys, immutable objects and existing S3/Floci signing infrastructure remain authoritative.
- D2: verify actual image decoding as well as stored length and media type. Metadata alone does not establish image validity. Enforce 1–10,000,000 bytes before upload. Use supported JPEG, PNG, WebP, HEIC and HEIF formats without claiming the photograph depicts the delivery.
- D3: five item images and five delivery proof images are separate limits. This clarifies NFR-18.1's ambiguous combined wording.
- D4: identical delivery confirmation retries return success without another mutation, including after receipt completion. Different proofs or unattended choice cannot replace the accepted confirmation.
- D5: retain files and successful uploads in the page after failure. Retry or explicitly remove failed selections. Never silently omit them. Distinguish unknown upload result from a verified missing object.
- D6: requester and delivering courier can read proof. A former courier removed before pickup cannot see later proof. Admin role alone is insufficient. Case-admin proof reads fail closed until authoritative assignment to an associated open report/appeal can be verified (NFR-06.3).
- D7: NFR-06.4 automated retention is explicitly deferred and incomplete. It needs the latest associated request/report/appeal closure and open-case information. Do not delete merely 90 days after order closure.
- D8: a server-confirmed upload outage permits zero-proof confirmation for both handover and unattended delivery. Record a server-derived reason. A client assertion, invalid image, missing object, local signing/configuration failure or individual network failure is not sufficient evidence.
- D9: under the order lock, reject first durable receipt at `received_at >= picked_up_at + 2 * delivery_duration`. Valid on-time pending confirmation remains eligible after processing delay. Proof must already be uploaded and server-verified; neither upload alone nor a replacement proof reserves time. Accepted repeats remain successful. Effective delivery time is the durable server receipt, while processing/audit time is separate. Storage unavailability retains pending for recovery and prolonged-failure alert; invalid proof rejects that submission and permits overdue timeout. Cancellation may win and must never be resurrected. See [delivery-confirmation-receipts.md](../adr/delivery-confirmation-receipts.md).


## Transaction and storage boundaries

Use `Mutator.Mutate` -> callback -> `Transition` -> `NewChange` for lifecycle changes. All object-store calls and image decoding occur outside order transactions. Recheck assignment, status, durable receipt eligibility and proof identity under the order lock before committing confirmation. Proof attachment, accepted proof set, unattended choice, outage reason, order revision, audit and status outbox fact commit atomically. There is no separate settlement path.

The courier uploads and calls `VerifyProofUpload` before confirmation. First successful verification records immutable preexistence evidence. Confirmation commits a normalized pending receipt before reinspection; unavailable inspection retains it for recovery. `HasPendingDelivery` and `ProofServerTime` are transaction methods consumed by downstream timers after the same parent lock. Effective delivery and processing/audit times remain separate. See the [receipt-time ADR](../adr/delivery-confirmation-receipts.md).

## Implemented contracts and controls

- Proof upload, verification, abandonment, confirmation and private reads have generated Connect contracts and mounted adapters. `size_bytes` is 1–10,000,000; signed uploads return required headers. `ProofTx` shares the aggregate transaction in proof migration `00007`.
- Signing uses fixed SDK time and credential/endpoint epochs rather than persisted bearer grants. Expiry or credential rotation refreshes the grant generation, not proof identity. Attachment compares immutable evidence, not renewable signing metadata.
- Abandonment fences unknown admissions before the browser removes a photo. Accepted evidence cannot be retired. Twenty active staged identities and 200 new identities per order/courier/hour bound staging; known retries/removal remain available at that admission budget. Cleanup revisits retired keys after outstanding grant expiry and never deletes accepted evidence.
- Server-confirmed outage evidence is scoped to the actual PUT route, bounded in time and locked against revocation during admission. Missing authority fails closed. Pending and accepted replay do not depend on current certification.
- The existing Order-detail action area mounts the proof panel only for the assigned courier while Delivering. Files and successful uploads survive retryable failures in the page; immutable server-pending input is restored after reload. API calls are bounded to 15 seconds. Direct PUT budget is 15 seconds plus one second per started 64 KiB, capped at 168 seconds for the maximum file size; slower links may fail retryably.
- The shared decoder supports JPEG, PNG, WebP, HEIC and HEIF, verifies actual pixels, and applies bounded bytes, dimensions, output, execution and concurrency. Production enables grants only after five-format capability checks. Container limits and the restricted decoder policy are required; no measured peak-RSS guarantee is claimed. See the [decoder ADR](../adr/delivery-proof-decoding.md).

## Acceptance boundaries

R1.7 requires verified stored proof or authoritative outage evidence, durable retry identity and duplicate-safe confirmation. R1.6 requires atomic delivery and the strict receipt deadline without overriding cancellation. NFR-06 requires private access; closure-dependent retention is incomplete. NFR-18 requires separate five-image limits and actual supported-format decoding. Browser fixtures cover retained input, focus and mutation feedback at 360/768/1440px, but do not establish live authentication or polished mobile layout.
