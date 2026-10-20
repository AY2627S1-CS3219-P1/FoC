# PR177 B2 delivery-proof review

**Reviewed:** HEAD `ed03fda8ed1fc9354da79372ab37f8c280791350` against B3 base `dea9c7c78130853793e8e5a8774f607cace02256`.

**Scope:** I read all 61 paths in the manifest through the pinned diff and the source files at their workspace paths. I did not recompute the manifest hashes, because only read, grep and find were allowed. I also read the pertinent Mutator, `NewChange`/`validateFields`, `Transition`, router/auth, `shared.ConnectError`, `Caller`/`HasParticipant` and the Progress suspension handling. No reads were blocked. I ran no tests or builds and made no edits.

**Verdict: specific changes required.** The parent-suspected race is real (finding F1). Everything else holds on the declared stack.

## Confirmed findings

### F1 (Medium–High): refreshing a grant while a receipt is pending makes recovery reject a valid on-time receipt
- **Where:**
  - `order-service/internal/order/postgresproofs.go:207` calls `proofUploadMatches`. That check compares Generation, SigningEpoch, GrantedAt and ExpiresAt (`order-service/internal/order/proofs.go:623-630`).
  - `CreateUpload` refreshes the grant without checking whether the proof is reserved by a pending receipt (`order-service/internal/order/proofs.go:117-131`).
  - The SQL upsert only requires `state='uploading'` (`order-service/database/query/proofs.sql:16-23`).
  - `ProofReceipts.process` then classifies the failure as invalid proof (`order-service/internal/order/proofreceipts.go:168-189`).
- **Interleaving:**
  1. The courier uploads P and verifies it (`verified_at` = T0).
  2. Before the deadline, ConfirmDelivery admits receipt R (`received_at` = T1). The pending state is committed.
  3. `process` reaches `confirmReceipt`. Phase 1 (`proofs.go:286-298`) snapshots P at generation 1 and releases the lock. Inspection and decoding run outside the lock (`proofs.go:327-331`).
  4. The same courier calls CreateProofUpload with P's `upload_id`. This happens after the 10-minute grant expiry or after a credential/route epoch change. The call passes `canUploadProof` (still Delivering and before the deadline) and commits generation 2. `verified_at` is preserved.
  5. Phase 3: `recheckProofs` passes, because it compares only immutable fields. Then `SaveProofConfirmation`'s `proofUploadMatches` fails on generation 1 vs 2 and returns ErrPrecondition.
  6. `process` marks R **rejected**. `HasPendingDelivery` goes false, so after the deadline B4 may time out an order whose receipt was valid.
- **Notes:**
  - `HasPendingDelivery` (`proofs.sql:56-65`) still counts this receipt as eligible, so B2 and B4 disagree about it.
  - The shipped UI does not trigger this: the controller locks after submission. But the server-side contract is reachable by any client acting as the receipt's own courier.
  - No test covers a refresh while a receipt is pending. `proofroundtrip_test.go:36-67` tests only `CreateUpload`'s own post-signing check, where comparing the whole row is correct.
- **Contract violated:** a valid confirmation durably received before the deadline stays eligible after processing delay. Grant metadata is not proof identity: "expiry or credential rotation refreshes the generation, not the proof identity" (receipts ADR).
- **Narrow fix:**
  - In `SaveProofConfirmation`, replace `proofUploadMatches` with an immutable-identity predicate: ID, Order, Courier, UploadID, Key, ContentType, SizeBytes, UploadScope, `CreatedAt.Equal` and `VerifiedAt` unchanged. That matches what `recheckProofs` already checks. Keep `proofUploadMatches` for `CreateUpload`.
  - Optionally, `CreateUpload` could refuse to refresh a row where `ProofReservedByReceipt` is true. A submitted proof needs no new PUT grant.
  - Add a PostgreSQL regression: block the validator, refresh the grant, release, then assert the receipt is accepted with `DeliveredAt == received_at`.

### F2 (Medium): environmental decoder failures are treated as invalid media, so recovery can reject a valid receipt
- **Where:** `order-service/internal/proofimage/decoder.go:91-98`. Any non-timeout `cmd.Run` error becomes ErrPrecondition, and `proofreceipts.go:168` then rejects the receipt.
- **Interleaving:**
  1. Photos are verified once.
  2. During receipt processing, the ImageMagick child is killed, for example by a cgroup OOM kill under the Compose `mem_limit` while two 1 GiB-cache decodes run (the ADR says the cache limit is not an RSS bound), or by another external signal.
  3. Go sees an `ExitError` and `bounded.Err()` is nil, so the result is ErrPrecondition and receipt R is rejected.
- The bytes are immutable (If-None-Match PUT) and already decoded successfully at first verification. A failed re-decode is therefore much more likely environmental than malformed.
- **Contract violated:** an unavailable later inspection must keep the receipt pending, retry and alert.
- **Fix:** map signalled or killed exits (`ProcessState` not `Exited()`, or exit code -1) to ErrUnavailable. Optionally, during receipt processing, treat a decoder ErrPrecondition on an already-verified proof as unavailable plus alert. Keep size, type and missing-object mismatches as rejections.

## Warnings (non-blocking)
- **W1 (Low–Medium):** The physical byte scope includes the internal endpoint URL (`order-service/internal/images/s3.go:58`). A benign endpoint change, such as a VPC endpoint or a hostname change on the same bucket, would make accepted evidence unreadable (`proofs.go:552`, ErrUnavailable). It would also leave pending receipts retrying forever (`proofs.go:467`). Consider scoping by bucket and region only, or a configured stable storage ID.
- **W2 (Low):** Cleanup reschedules every retired key hourly with no end condition (`proofcleanup.go:78`, `proofs.sql:98-99`), capped at 50 per minute. Work grows without bound. `CleanOnce` also aborts the whole batch on the first per-candidate error (`proofcleanup.go:87-89`), so a persistent error can block the head of the queue.
- **W3 (Low, standards):** Stale statements contradict the integrated code:
  - `order-service/internal/order/prooftypes.go:112-116`: "No concrete retry-stable signing adapter is wired".
  - `docs/plans/delivery-proof.md:52, 62, 63`: no PostgreSQL adapter, cleanup worker, migration or outage adapter yet.
  - `proto/order/v1/order.proto:181-182`: "returns the same proof and URL". The URL now changes when the grant refreshes.
- **W4 (Low):** The exported `Proofs.ConfirmDelivery` ("immediate application operation", `proofs.go:252-256`) cannot succeed against the PostgreSQL adapter, because `SaveProofConfirmation` requires a pending receipt (`postgresproofs.go:192-195`). Only fake-backed tests exercise it.
- **W5 (Low):** `ForOrder` (a NO_SIDE_EFFECTS read) takes the parent FOR UPDATE lock (`proofs.go:524`), so polling serializes with mutations and timers.
- **W6 (Low, uncertain):** Pending input is shown to `IsAssignedTo(caller)` rather than to `pending.CourierID == caller` (`proofs.go:530`). This only matters if B1/B3 allow reassignment while Delivering. I did not confirm whether they do.
- **W7 (Low):** An outage-authority read error becomes ErrPrecondition at admission (`proofreceipts.go:74-79`), so the UI shows "Rejected" instead of Unavailable. It is harmless because no receipt is created.
- **W8 (Low):** Each replica runs its own probe counter and publishes to the shared row (`proofoutages.go:71`). This causes generation churn and flapping across replicas. It fails closed.
- **W9 (Low):**
  - The production image moved from distroless to `debian:trixie-slim` with unpinned apt packages and an undigested base, which increases attack surface and makes builds less reproducible.
  - Recovery has no per-receipt timeout and no SKIP LOCKED, so replicas process the same due receipts redundantly.

## Checked and found sound
- **Locking and the landmine:** the parent is locked first, then relationships are read in separate statements.
- **Clocks and times:** receipt time is `clock_timestamp()` sampled after the lock. Effective `DeliveredAt` and the Credit fact's `DeliveredAt` equal the receipt time, while `UpdatedAt` and audit time are processing time. The microsecond deadline rounding in `HasPendingDelivery` matches Go's `Before`.
- **Verification and admission:**
  - The first verification is immutable (COALESCE).
  - Admission requires `verified_at` ≤ receipt.
  - Abandoning a reserved proof is fenced.
  - Removal is acknowledged before the browser discards the photo.
  - Cancellation results in a superseded receipt.
  - Accepted identical replay works after Completed.
- **Outage exception:** zero-proof confirmation needs locked, fresh evidence at admission; afterwards that evidence is immutable.
- **Limits:** five proof photos are independent of item photos. 1–10,000,000 bytes is enforced in the proto, application, signed PUT and read bound.
- **Read access:** proof reads are limited to requester and current courier. Admin role or former-courier status grants nothing.
- **Suspension:** handling matches the delivered Progress and Disposition policy.
- **RPC wiring:**
  - The proof RPCs sit behind `RequireCaller` and the validate interceptor.
  - Generated Go and TS output contains the new RPCs and fields.
  - The frontend adapter releases saved input only on authoritative codes. Composition uses the auth-intercepted transport.

## External dependencies (not reviewed)
- B4 timer consumer (PR183): I reviewed only B2's exported seam (`HasPendingDelivery` and `ProofServerTime`, which require the lock). F1 is a case where B2 rejects a receipt that this seam still counts as eligible.
- Root `main.go` is frozen per the handoff.
- Live auth, Credit, Kafka, Telegram and production infrastructure, plus production RSS and isolation.
- Deferred by Andrew: case-admin revocation-safe access and report/appeal retention.
