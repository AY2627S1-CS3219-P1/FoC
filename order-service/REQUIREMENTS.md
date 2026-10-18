# Order Service — Requirements

Source: `Group 1 Product Backlog Document.pdf` §1 Functional Requirements (Order Service: R1–R4).
Scope: owned by `order-service/`. Credit settlement is owned by Credit Service (C1); Order publishes terminal outcomes.

## R1 — Request Management

### R1.1 Creation

| ID | Requirement | Priority | Planned |
|----|-------------|----------|---------|
| R1.1 | Support request creation. | High | Week 9 |
| R1.1.1 | Allow requester to specify supplier / pick-up location. | High | Week 9 |
| R1.1.2 | Allow requester to write description of item to be picked up. | High | Week 9 |
| R1.1.3 | Allow requesters to attach images to enhance item description. | High | Week 9 |
| R1.1.4 | Allow requester to specify delivery location. | High | Week 9 |
| R1.1.5 | Allow requester to optionally specify delivery instructions. | High | Week 9 |
| R1.1.6 | Allow requester to specify expected reward not greater than available credit balance. | High | Week 9 |
| R1.1.7 | Allow requester to specify a delivery time period. | Medium | Week 9 |
| R1.1.8 | Allow requester to specify an expiry time. | Medium | Week 9 |
| R1.1.9 | If reward reservation fails, the request must not become open. | High | Week 9 |
| R1.1.10 | Allow requester to specify an optional minimum courier aggregate rating. | Medium | Week 9 |
| R1.1.11 | Validate min rating: 1–5 stars in 0.5 increments. | Medium | Week 9 |
| R1.1.12 | Validate delivery request has different pick-up and delivery locations. | Medium | Week 9 |

### R1.2 Acceptance

| ID | Requirement | Priority | Planned |
|----|-------------|----------|---------|
| R1.2 | Support request acceptance by courier. | High | Week 9 |
| R1.2.1 | Assign at most one courier to a request. | High | Week 9 |
| R1.2.2 | Prevent a courier from accepting their own request. | High | Week 9 |
| R1.2.3 | Allow couriers to view open requests. | High | Week 9 |
| R1.2.4 | When acceptance races cancellation or expiry, commit only one outcome. | High | Week 9 |
| R1.2.5 | If min rating specified, reject acceptance by courier below that minimum. | Medium | Week 9 |
| R1.2.6 | Allow couriers to counteroffer with a larger credit amount than the original request. | Medium | Week 9 |

Counteroffer credit check: re-reserve on counteroffer; requester cannot accept above available balance (C1.2.2).

### R1.3 Cancellation

| ID | Requirement | Priority | Planned |
|----|-------------|----------|---------|
| R1.3 | Support request cancellation. | Medium | Week 9 |
| R1.3.1 | Allow requester to cancel before pickup. | Medium | Week 9 |
| R1.3.2 | Allow assigned courier to cancel an Awaiting Pickup request. | Medium | Week 9 |
| R1.3.3 | Allow requester to cancel after pickup. | Medium | Week 9 |
| R1.3.4 | Allow courier to cancel after pickup. | Medium | Week 9 |

### R1.4 Warning Triggers

| ID | Requirement | Priority | Planned |
|----|-------------|----------|---------|
| R1.4 | Identify request outcomes that trigger account warnings (User Service owns warnings). | — | — |
| R1.4.1 | When a courier cancels after pickup, request an account warning for that courier. | Medium | Week 9 |
| R1.4.2 | When a courier's payable reward reaches zero, request an account warning for that courier. | Low | Week 10 |

### R1.5 Item Disposition

| ID | Requirement | Priority | Planned |
|----|-------------|----------|---------|
| R1.5 | Support recording item disposition after cancellation. | — | — |
| R1.5.1 | After cancellation before pickup, record that the courier does not hold the item. | Medium | Week 9 |
| R1.5.2 | After cancellation following pickup, require the courier to record the item's final location. | Medium | Week 9 |

### R1.6 Lifecycle

| ID | Transition | Priority | Planned |
|----|------------|----------|---------|
| R1.6 | Support the request lifecycle. | High | Week 9 |
| R1.6.1 | Newly created → Awaiting Courier. | High | Week 9 |
| R1.6.2 | Eligible courier assignment: Awaiting Courier → Awaiting Pickup. | High | Week 9 |
| R1.6.3 | Assigned courier pickup confirmation: Awaiting Pickup → Delivering. | High | Week 9 |
| R1.6.4 | Courier uploads proof of delivery photo: Delivering → Delivered. | High | Week 9 |
| R1.6.5 | Requester receipt confirmation: Delivered → Completed. | High | Week 9 |
| R1.6.6 | Requester cancellation before pickup → Cancelled. | High | Week 9 |
| R1.6.7 | Courier cancellation before pickup → Awaiting Courier (return to Counteroffer/Offer Acceptance Phase). | High | Week 9 |
| R1.6.8 | Requester cancellation after pickup → Cancelled. | High | Week 9 |
| R1.6.9 | Courier cancellation after pickup → Failed. | High | Week 9 |
| R1.6.10 | Twice the allowed delivery duration in Delivering → Failed. | High | Week 9 |
| R1.6.11 | Expiry before acceptance: Awaiting Courier → Expired. | High | Week 9 |
| R1.6.12 | Reject a transition attempted by an unauthorized actor. | High | Week 9 |
| R1.6.13 | Reject a transition from an ineligible source state. | High | Week 9 |

### R1.7 Delivery Confirmation

| ID | Requirement | Priority | Planned |
|----|-------------|----------|---------|
| R1.7 | Support delivery confirmation by the courier. | High | Week 9 |
| R1.7.1 | Require at least one proof photo unless system confirms proof upload is unavailable. | High | Week 9 |
| R1.7.2 | Successful proof upload associates the proof with the request. | High | Week 9 |
| R1.7.3 | After a failed proof upload, keep the request in Delivering. | High | Week 9 |
| R1.7.4 | After a failed proof upload, state that the proof was not saved. | High | Week 9 |
| R1.7.5 | After a failed proof upload, allow the courier to retry. | High | Week 9 |
| R1.7.6 | Repeating the same successful proof upload must not create another proof record. | High | Week 9 |
| R1.7.7 | If system confirms proof upload is unavailable, allow confirmation without photo and record the reason. | Low | Week 10 |
| R1.7.8 | Permit unattended delivery with proof when the recipient is unreachable. | High | Week 9 |

### R1.8 Order History

| ID | Requirement | Priority | Planned |
|----|-------------|----------|---------|
| R1.8 | Support an order history for each user (suspended users retain access, U6.7). | Medium | Week 9 |
| R1.8.1 | Allow a user to view requests in which that user participated. | Medium | Week 9 |
| R1.8.2 | Retain all requests in order history. | Medium | Week 9 |
| R1.8.3 | Each entry contains request status, requester, courier when assigned, and request details. | Medium | Week 9 |

## R2 — Admin Handling During Suspension

| ID | Requirement | Priority | Planned |
|----|-------------|----------|---------|
| R2 | Support administrative handling of active requests during account suspension. | Low | Week 10 |
| R2.1 | For deferred suspension, allow admin to cancel a request involving that account before pickup. | Low | Week 10 |
| R2.1.1 | Administrative cancellation under R2.1 → Cancelled. | Low | Week 10 |
| R2.2 | For deferred suspension, allow admin to unassign that account from an Awaiting Pickup request. | Low | Week 10 |
| R2.3 | Administrative unassignment under R2.2 → Awaiting Courier. | Low | Week 10 |

## R3 — Reviews

| ID | Requirement | Priority | Planned |
|----|-------------|----------|---------|
| R3 | Support reviews between participants in a Completed request. | Medium | Week 7 |
| R3.1 | Allow requester to review the courier after Completed. | Medium | Week 7 |
| R3.2 | Allow courier to review the requester after Completed. | Medium | Week 7 |
| R3.3 | A submitted review contains a rating. | Medium | Week 7 |
| R3.3.1 | Rating is an integer from 1–5 stars. | Medium | Week 7 |
| R3.4 | A submitted review contains a comment. | Medium | Week 7 |
| R3.5 | Accept at most one review from each participant about the other per Completed request. | Medium | Week 7 |
| R3.6 | Aggregate rating = arithmetic mean of that user's ratings in reviews not removed. | Medium | Week 7 |
| R3.7 | Review count = number of reviews about that user not removed. | Medium | Week 7 |

Suspended users cannot submit reviews (U6.5). Report outcome may request review removal (RP5.4); appeal may restore/remove (RP12.3, RP12.4).

## R4 — Review Moderation

| ID | Requirement | Priority | Planned |
|----|-------------|----------|---------|
| R4 | Support administrative review moderation. | Low | Week 7 |
| R4.1 | Allow an admin to remove a review. | Low | Week 7 |
| R4.2 | Require the admin to record a reason when removing a review. | Low | Week 7 |

## Relevant NFRs

- NFR-02 (one-courier invariant under 50 concurrent accepts), NFR-11.6 (concurrent acceptance tests).
- NFR-04 / NFR-04.1 (idempotent request-state events; no duplicate transitions), NFR-20 (pub-sub with Credit Service for settlement).
- NFR-05.2.3/05.2.5, NFR-06.2/06.3 (auth by persisted request relationship; exact locations and proof images restricted to participants + assigned admin).
- NFR-09 / NFR-09.5 (restart without losing orders; detect Order/Credit mismatches within 5 min).
- NFR-10.3 (audit: credit settlement, review removal/restoration), NFR-18.1/18.2/18.5 (≤5 images, ≤10MB each, reject oversize before storage), NFR-19.1 (≤10 requests/min/account).

### Foundation test traceability

These links map the checked-in NFR summaries above to existing tests. Test comments
link back to the corresponding anchor here. The source PRD PDF is unavailable in
this repository; bundled subclauses retain their existing grouping. Evidence below
covers the foundation boundaries and does not certify the complete NFR.

| NFR | Tests | Coverage and remaining work |
| --- | --- | --- |
| <a id="nfr-02"></a>NFR-02 / NFR-11.6 | [`TestPostgresFoundation/competing mutations serialize to one courier`](internal/order/postgres_integration_test.go#L403); [`TestPostgresFoundation/assignment racing expiry commits one change`](internal/order/postgres_integration_test.go#L429) | **Partial:** one courier wins among 16 local assignment mutations, and assignment/expiry commits one local change. The required 50 concurrent acceptance RPCs and their future saga are not exercised. |
| <a id="nfr-04"></a>NFR-04 / NFR-04.1 | [`TestCancellationDefaultsAndRepeats`](internal/order/transitions_test.go#L64); [`TestMutateRecognizedRepeatCommitsWithoutWrites`](internal/order/mutation_test.go#L79); [`TestSameStatusAssignmentRepeatCannotCommit`](internal/order/change_invariants_test.go#L63); [Postgres receipt repeat](internal/order/postgres_integration_test.go#L188); [revision conflicts](internal/order/postgres_integration_test.go#L319); [conditional revision writes](internal/order/postgres_integration_test.go#L356); [atomic rollback](internal/order/postgres_integration_test.go#L370); [outbox duplicate protection](internal/order/postgres_integration_test.go#L502) | **Partial:** action-specific local repeats, revision checks, atomic rollback and persisted fact uniqueness. Kafka delivery/replay and downstream idempotency are not exercised. |
| <a id="nfr-05-auth"></a>NFR-05.2.3 / NFR-05.2.5 | [persisted authorization baseline](internal/order/transaction_invariants_integration_test.go#L25); [persisted participant/history visibility](internal/order/reads_integration_test.go#L110); [persisted offer visibility](internal/order/reads_integration_test.go#L141); [`TestTransitions`](internal/order/transitions_test.go#L10); [`TestGetOrderAccessAndRedaction`](internal/order/service_test.go#L42); [`TestListValidationAndAdminAuthorizationOrder`](internal/order/service_test.go#L109); [`TestAuthorizationBeforeValidation`](internal/rpc/order/handler_test.go#L142) | **Foundation reads and mutation boundary:** integration tests use stored relationships. Domain fakes verify policy; the RPC test verifies method authorization before validation with injected identity. Future mutation operations and end-to-end JWT authentication need their own coverage. |
| NFR-06.2 | [`TestOrderViewProtectsEveryResponse`](internal/rpc/shared/conversion_test.go); [`TestReadPrivacyThroughConnectAndPostgres`](internal/rpc/order/readprivacy_integration_test.go); [`TestAssignmentHistoryPrivacyAndAtomicity`](internal/order/assignmenthistory_integration_test.go) | **Backend read/mutation projection and history:** real PostgreSQL18 and mounted Connect reads verify coarse destination/filtering, exact-filter rejection, historical/admin redaction and current-participant exact access. Shared response tests verify sensitive text and attachment-URL suppression. Live JWT issuance and authoritative open-case admin grants are not exercised; admin-only sensitive access fails closed. |

The following NFRs have no direct foundation test proving the stated requirement:

| NFR | Current coverage limit |
| --- | --- |
| NFR-06.3 | Proof-image authorization/workflows are outside this foundation and are not certified by the item-image/read-projection tests above. |
| NFR-09 / NFR-09.5 | Snapshot round trips and transaction tests do not restart the server/database, recover workflows or detect Order/Credit mismatches within five minutes. |
| NFR-10.3 | Local Order audit persistence is tested, but Credit settlement and review removal/restoration audit actions are outside this foundation. |
| NFR-18.1 / NFR-18.2 / NFR-18.5 | The storage fake tests interface semantics, without image-count, size-limit or pre-upload rejection workflows. |
| NFR-19.1 | No request creation rate limiter or requests-per-account test is implemented. |
| NFR-20 | Durable outbox facts are tested; publication to Credit and downstream settlement are deferred. |

### Creation, images and publication execution

The replacement implements R1.1.1–R1.1.9 and R1.1.12 at the backend boundary,
including the five-image/10,000,000-byte limits and NFR-19.1 admission quota.
[`tests/creation`](tests/creation) contains runnable PostgreSQL/Connect, Floci and
Kafka coverage; see [setup and scope](README.md). Credit endpoints are scripted
fixtures. Minimum courier rating (R1.1.10–11) depends on Review and is incomplete.

| Requirement | Execution evidence and limit |
| --- | --- |
| NFR-04 / NFR-09 | Concurrent duplicate creation, stable identities, restart after remote success/local rollback, unknown outcomes, delayed responses and durable publication replay. Whole-deployment recovery is not certified. |
| NFR-05 / NFR-06 | Image owner/attachment checks and authorized signed download grants. Proof-image workflows and the complete relationship/privacy NFR remain follow-ups. |
| NFR-18 | Supported types, count, exact signed byte length, altered requests rejected before object storage, atomic attachment and orphan cleanup. Bodies are not decoded or converted. |
| NFR-19.1 | Fifty distinct attempts admit ten per rolling minute; retained retries consume no quota. |
| NFR-20 | **Partial:** Order publishes outcome events through real Kafka. Credit consumption, financial deduplication/settlement and five-minute reconciliation/alerts remain Credit dependencies. |

Run `GOCACHE=/tmp/foc-go-build make -C order-service test-creation` from the root.
Later lifecycle RPCs and browser UI are not implemented by this slice. Existing
foundation tables above describe their original coverage, not the newer tests.
