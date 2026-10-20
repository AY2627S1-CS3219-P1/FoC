**Verdict: B2 is ready on the declared B3+B2+B4 stack, with no blockers.** F1, F2, W3, W4, W6 and W7 are all resolved. The small deltas introduce one new low-severity misclassification, which doesn't block. Readiness still depends on two things I didn't check: your running verification passing, and the final commit's source hashes matching the manifest.

**Candidate reference:** frozen, uncommitted candidate on baseline `ed03fda8ed1fc9354da79372ab37f8c280791350`, defined by `delivery-proof-cli-followup-records.json (source_binding.source_sha256)` (13 paths). I read the current source at those paths and compared it with `the frozen fix diff (private working artifact omitted)`. They match. I didn't recompute the SHA-256 hashes, since only read, grep and find were allowed. I ran no checks and assume no results from them. I didn't re-review B4/PR183 or unchanged source.

## Prior findings

- **F1: resolved.**
  - `SaveProofConfirmation` now uses `proofEvidenceMatches`. That checks immutable identity, media, size and scope, `CreatedAt`, an unchanged first `VerifiedAt`, and that `VerifiedAt` is not after `ReceivedAt` ([order-service/internal/order/postgresproofs.go:207-209](https://github.com/AY2627S1-CS3219-P1/FoC/blob/d5aa5ee4d9817a61d1704e42189aa50116d8fe32/order-service/internal/order/postgresproofs.go#L207-L209), [order-service/internal/order/proofs.go:624-632](https://github.com/AY2627S1-CS3219-P1/FoC/blob/d5aa5ee4d9817a61d1704e42189aa50116d8fe32/order-service/internal/order/proofs.go#L624-L632)).
  - `CreateUpload`'s post-signing check still compares the whole grant with `proofUploadMatches` (`proofs.go:155`), so URL replay stays strict.
  - A refresh can't change the scope (`proofs.go:112`) or touch an accepted row.
  - The new PostgreSQL regression refreshes the epoch inside the validator, which runs outside the lock, so there is no deadlock. It then checks that the receipt isn't rejected and that `DeliveredAt` equals the stored `received_at`. `rotatingProofIssuer` lives in an untagged test file, so it is available under the `integration` tag.
- **F2: resolved.** Timeouts, spawn failures and signalled or non-exited processes now map to `ErrUnavailable`. A normal nonzero exit is still `ErrPrecondition` ([order-service/internal/proofimage/decoder.go:91-105](https://github.com/AY2627S1-CS3219-P1/FoC/blob/d5aa5ee4d9817a61d1704e42189aa50116d8fe32/order-service/internal/proofimage/decoder.go#L91-L105)). The self-kill shell test exercises the signalled branch without depending on codec fixtures.
- **W3: resolved.** None of the stale statements remain. The proto, Go and TS comments are identical.
- **W4: resolved.** For a `ReceiptRepository`, `Proofs.ConfirmDelivery` dispatches to `ProofReceipts`, which calls the private `confirmReceipt`, so there is no recursion. The fake repositories don't implement `DueProofReceipts`, so the existing unit tests keep the immediate path. The RPC still calls `proofReceipts` directly.
- **W6: resolved.** Pending input also requires `strings.EqualFold(value.CourierID, caller.ID)`. If another courier's receipt is pending, the read returns an empty view rather than an error, which is fine.
- **W7: resolved.**
  - At fresh admission, an outage-authority error other than `ErrPrecondition` returns `ErrUnavailable`. `ProofOutages` uses `ErrPrecondition` only to mean "not active" (`proofoutages.go:45-47`).
  - Accepted and pending replay are both checked before that point, so replay doesn't depend on the current authority. PostgreSQL tests cover both cases.
  - Suspension policy is unchanged, as intended.

## New defect (low severity, not a blocker)

I traced this from the code and standard-library behaviour; I did not run it.

**N1: decoder output overflow is now reported as unavailable instead of invalid media.** Location: `order-service/internal/proofimage/decoder.go:88-90, 98-104, 114-116`.

1. `boundedOutput.Write` returns an error once stdout passes 1536 bytes (more than 512 three-byte frames, for example an animated WebP with 513+ frames) or stderr passes 2048 bytes.
2. `os/exec` then closes the pipe's read end. Next:
   - If the child writes again, it gets SIGPIPE and is signalled, so `!Exited()` is true.
   - If it had already finished and exited 0, `Run` returns the copy error, which is not an `*exec.ExitError`.
3. Both cases now return `ErrUnavailable`. At ed03fda both returned `ErrPrecondition`.

**Impact:**
- `VerifyUpload` reports an input that will always fail as a retryable outage. The UI keeps the input and retries until the deadline instead of showing Rejected.
- This contradicts the ADR sentence "Malformed media remains a precondition rejection".
- It can't cause a false acceptance or a receipt rejection, because such a proof never gets `verified_at` and so can't be admitted.

**Narrow fix:** record overflow in `boundedOutput` and return `ErrPrecondition` before checking the exit status. Add a script test that writes more than 1536 bytes and exits 0.

## Remaining warnings (documented, not blockers)

- **Leftover F2 path:** if the decoder fails a memory allocation and exits normally with an error code (for example under RLIMIT_AS rather than a cgroup OOM kill), that is still `ErrPrecondition` and the receipt would be rejected. The Compose cgroup `mem_limit` kills the process, which now maps to unavailable. The optional suggestion to treat a decoder rejection of an already-verified proof as unavailable was not adopted.
- **CreateUpload still refreshes reserved proofs:** it can still issue a new PUT URL for a proof reserved by a pending receipt. This is harmless, because the If-None-Match PUT prevents an overwrite and attachment no longer compares grant fields.
- **W1, W2, W5, W8 and W9** remain disclosed operational constraints. The stable-endpoint binding fails closed.

## Approved deferrals and exclusions

- Deferred: report/appeal retention, and revocation-safe case-admin access (admins stay fail-closed).
- No claim about live auth, Credit, Kafka, Telegram or a production rollout. No merge or deploy.
