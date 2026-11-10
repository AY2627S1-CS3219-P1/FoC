# Order/Credit conformance

Owners: Order / Credit. Coordination: #136; workload authentication: #29.
Order integration tests use scripted Credit endpoints, not a Credit implementation.

| Case | Required result |
| --- | --- |
| Identical/concurrent creation | One Order and reservation identity. |
| Changed retained payload | already_exists; no second financial intent. |
| Retry after expiry | Retained key lookup precedes new-operation deadline checks. |
| Confirmed refusal | Rejected; no release or outcome; local rejection replays. |
| Timeout / not_found | Pending; reconcile/retry the same identity. |
| Expiry during Reserve | Persist abort, confirm release, reject; never open. |
| Release before Reserve | Durable Credit tombstone fences delayed reservation. |
| Remote success / local crash | Recover the same identity without duplicate effects. |
| Lost publication response | Replay the same event ID. |
| Credit commit / offset crash | Deduplication, money and ledger commit together; redelivery is harmless. |
| Repeated terminal outcomes, including different event IDs | One payout/refund per Order. |
| Completed | Credit calculates payable reward and refunds remainder. |
| Requester cancellation after pickup | Pay full reward to courier. |
| Cancellation before pickup / expiry / failure | Return held credits. |
| Credit downtime | Order retains lifecycle; settlement resumes on recovery. |
| Financial mismatch | Detect at startup and within five minutes; alert admin if unexplained. |
| Invalid workload credentials | Reject missing, forged, expired and wrong-audience credentials under #29. |

Credit's own tests (`credit-service/internal/reservation`,
`credit-service/internal/router`) cover the Completed, after-pickup and release
settlements, and repeated outcomes with the same or different event IDs. They
also cover a mismatched outcome that moves no credits. They run against an in-memory
repository and Postgres, calling the consumer directly rather than through a broker round
trip with Order.
Financial reconciliation and authentication verification are Credit/#29
dependencies. Mark coverage only against actual implementations. Never
add settlement RPC fallback. GetReservation describes current state, not whether
an in-flight Reserve may still complete. Creation compensation is not settlement
of an opened order. Minimum rating, later lifecycle operations and UI are follow-ups.
