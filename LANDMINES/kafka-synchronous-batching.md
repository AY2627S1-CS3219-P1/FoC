# Synchronous Kafka batching delays outbox publication

**Applies when:** A deployment uses a build without the explicit short Kafka
flush timeout, or performance evidence relies on Kafka consumer lag alone.

## Failure

At `ae4ee3cf`, the shared publisher leaves kafka-go's batch timeout and batch
size at their defaults (one second and 100 messages). Order's `Relay.Drain`
publishes one row and waits for acknowledgement before submitting the next.
Each call therefore waits for the batch timer instead of filling a batch.
User's relay has the same serial structure; its sustained rate has not been
measured.

A real local API pilot reproduced approximately one acknowledged Order event
per second. After load stopped, 3,503 rows remained unpublished and the oldest
was 437 seconds old. A successful create/cancel left its reservation `HELD`
past the workflow's 30-second deadline. The same workflow passed with an empty
Order outbox. Credit's broker lag was zero while unpublished events accumulated.

## Proceed

Measure pending outbox count and oldest age alongside consumer lag and committed
business effects. Do not interpret successful mutation responses or zero Kafka
lag as proof of timely settlement. Keep experiments separate and record backlog
before the next experiment; deterministic fixtures do not reset exercised state.

The fix is tracked in [issue #321](https://github.com/AY2627S1-CS3219-P1/FoC/issues/321).
The [local run report](../docs/testing/aws-performance-local.md) records evidence
and the limits of the pilot. The test harness does not change application
publisher settings or resize the deployment to hide this limit.

## Removal condition

Remove this entry after a real-broker regression demonstrates that serial
publication no longer incurs the one-second batch wait, and a representative
mutation workload demonstrates bounded outbox age and timely committed Credit
effects. Preserve acknowledgement and retry/idempotency guarantees.
