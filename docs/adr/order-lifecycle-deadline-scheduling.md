# Order owns persisted lifecycle deadline scheduling

Status: accepted

Order Service runs two in-process polling workers for request expiry and delivery timeout. PostgreSQL stores deadlines and delivery confirmation receipts; process-local timers do not own eligibility. This keeps scheduling with the aggregate that must authorize and commit each transition, without making an external scheduler, Kafka, Credit or proof storage available before local deadline processing can proceed.

## Decision and boundaries

Start both workers with the service's existing cancellation context and WaitGroup. Scan immediately on startup and normally wait one second between successful scans. Share discovery, bounded concurrency, retry pacing and shutdown in a private scanner, while retaining separate expiry and timeout policies. The scanner processes UUID-keyset pages of 50 candidates with four workers and five-second contexts for discovery and individual attempts.

Discovery is advisory. Each attempt locks the parent Order, reads relationships in fresh statements, then uses the database clock to recheck status and deadline. Timeout also reads the eligible pending delivery confirmation receipt in a separate statement after the lock, through the borrowed proof transaction. No remote calls or nested transactions occur under that lock. Status, offer changes, System audit and outgoing facts commit together through the existing aggregate mutation boundary. Multiple service instances may discover the same candidate; correctness comes from locked rechecks and atomic writes, not a leader lease or exactly-once scheduling.

Expiry retains the requester's original deadline after withdrawal or unassignment. Timeout is due at pickup plus twice the allowed delivery duration. An eligible on-time pending delivery confirmation receipt protects the order during delayed validation. Rejected proof releases that protection; cancellation can win the same lock. Admission, proof recovery and effective delivery time remain governed by the [receipt decision](delivery-confirmation-receipts.md). Credit owns settlement, not either timer worker.

Advance past failed candidates so they cannot starve later pages. Retry individual failures with advisory backoff capped at 30 seconds without delaying healthy discovery. Discovery failures back off the scan loop. Cursor and retry bookkeeping are disposable: restart discovers outstanding work from persisted Order state. Cancellation stops both workers and joins them before the database pool closes.

## Alternatives and consequences

- Per-order in-memory timers would lose pending work on process exit and require an additional durable recovery mechanism. Persisted discovery already supplies that mechanism.
- An external scheduler or delayed-message system would add deployment and delivery/retry dependencies while still requiring the same aggregate lock, deadline checks and duplicate handling. It is not required for the current ownership and recovery contract.
- Polling trades repeated database reads and duplicate discovery across instances for simple restart recovery and no separate scheduling infrastructure. One second is a normal inter-scan delay, not a real-time completion guarantee. Backlogs, lock contention, pending confirmations and discovery-clock skew can delay processing. Final eligibility remains database-clock authoritative. Partial indexes support candidate discovery, but no production throughput or outage-load benchmark is claimed.

See the [aggregate transaction decision](order-aggregate-foundation.md) for mutation and outbox guarantees, and [timer verification](../plans/order-lifecycle-timers.md) for repeatable race, restart-recovery and worker-lifetime evidence. This records the implemented scheduling decision without changing runtime behavior.
