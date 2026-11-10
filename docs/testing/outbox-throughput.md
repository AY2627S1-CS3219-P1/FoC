# Outbox publication follow-up

Addresses the batching and scheduling causes in [issue #321](https://github.com/AY2627S1-CS3219-P1/FoC/issues/321).
The five-second settlement acceptance criterion remains open: the local
80-events/sec workload below exceeded measured publication capacity.

The shared Kafka publisher flushes incomplete batches after 10 ms, retaining
the default batch size, synchronous acknowledgement and `RequireAll`.
Order's relay immediately continues after a full batch of 50 attempted facts.
It waits one second after exhausting eligible work or a storage failure.
Per-event retry delays, frozen batch eligibility, leases and stable IDs remain.
The exported `Drain` interface is unchanged.

## Verification on 10 November 2026

- Deterministic clock tests verify that 151 queued facts cross batch boundaries
  without advancing time, including a full batch of deferred facts. Empty
  queues and claim failures wait before polling again. Race tests pass.
- Kafka's existing failure/redelivery integration test and the new serial-flush
  integration test pass against Kafka 4.2.2. The latter warms connections and
  requires 12 acknowledged serial calls to finish within three seconds.
- Formatting and correctness lint pass for `pkg` and `order-service` with the
  pinned golangci-lint 2.14.0.
- An isolated PostgreSQL 18/Kafka test exercised the actual Order relay and
  Credit outcome handler with 400 staged cancellations (800 source events),
  arriving at 80 events/sec for ten seconds. All 400 eventually settled, the
  outbox emptied, and account/ledger/hold consistency passed. This workload
  **did not meet** the five-second settlement target: 114/400 met it; the
  conservative observed p95 bound was 12.629 seconds and maximum 13.132 seconds.
  The 800 publications spanned 22.835 seconds, approximately 35 events/sec.
  Removing the scheduling quota does not establish sufficient physical
  publication capacity at this offered rate. The remaining bottleneck was not
  profiled; these results do not identify its cause.
- Replaying those 400 outcomes with original and fresh event IDs left complete
  financial snapshots unchanged after a committed consumer-offset barrier.
- A controlled producer process exited after real Kafka acknowledgement of an
  outcome and before confirming its outbox row. The row remained unpublished
  and leased. After the native 30-second lease expired, the updated relay
  republished it and emptied the queue without an additional financial effect.

Source cancellation rows were staged privately; these checks do not certify
public API/browser flows. Timing includes source database work and nominal
100 ms observation polling plus query/process scheduling overhead. The broker,
database and observer shared a host. Each load scenario ran once. No AWS sizing,
ten-minute acceptance workload or 10,000-in-flight API requirement was verified.

Private raw results and reproducible drivers are under
`/tmp/foc-relay-drain-9ndsthio/`; the earlier publisher comparison is under
`/tmp/foc-batchsize1-33k8i848/`. These local artifacts are not required to run the
committed regression tests. See `pkg/kafka/README.md` for real-broker setup.
