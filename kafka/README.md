# Kafka contracts

Kafka message contracts live here, separately from the Connect RPC contracts
under `proto/`. Files use `<service>/events/v1/<topic>.proto`, with package
`<service>.events.v1`. The service is the producer/owner. Use `.proto`, and
declare messages and enums without RPC services.

| Topic | Value message | Key |
| --- | --- | --- |
| `order.status_changed` | `order.events.v1.OrderStatusChanged` | Order UUID as UTF-8 text |
| `order.outcome` | `order.events.v1.OrderOutcome` | Order UUID as UTF-8 text |
| `order.courier_warning_requested` | `order.events.v1.CourierWarningRequested` | Order UUID as UTF-8 text |
| `order.offer_made` | `order.events.v1.OrderOfferMade` | Order UUID as UTF-8 text |
| `users.create` | `users.events.v1.UserCreated` | User UUID as UTF-8 text |
| `credit.account_changed` | `credit.events.v1.AccountChanged` | User UUID as UTF-8 text |
| `order.changed` | `order.events.v1.OrderChanged` | Order UUID as UTF-8 text |
| `moderation.outcome_requested` | `moderation.events.v1.OutcomeRequested` | Affected user or review UUID as UTF-8 text |

Credit publishes account changes with each committed nonzero ledger posting.
Order publishes read-model changes through its existing outbox, including offers
and workflow-only changes. Communication's separate `communication-realtime`
group turns these events into advisory browser invalidations. Duplicates are
safe; do not deduplicate Order changes solely by aggregate revision. See
[the live-update ADR](../docs/adr/realtime-invalidation.md).

`users.create` announces a newly created user. Its payload contains the event
ID, user ID, email, display name, and creation timestamp. The event ID is stable
across retries; consumers should deduplicate it. User Service publishes this
event from its transactional outbox after registration commits. Credit consumes
it to open the user's account with the starting credits, and Communication
keeps each user's email for email notifications.

Order publishes its four topics from its outbox. Topic names match the outbox
fact kinds. `order.status_changed` drives Communication's notifications,
`order.outcome` reports how an order ended,
`order.courier_warning_requested` asks User Service to warn a courier, and
`order.offer_made` lets Communication tell the requester about a courier's
offer.

Values use binary Protobuf (`proto.Marshal` / `proto.Unmarshal`), without a
schema registry framing prefix. The topic identifies its value message.
Generated Go types live under `pkg/kafkagen`; Kafka generation does not run
Connect-Go or TypeScript plugins.

```sh
make buf-lint
make buf-generate-kafka # Go messages only; no frontend dependencies needed
make buf-generate      # RPC and Kafka contracts
```

Generation cleans separate output directories, so RPC and Kafka generation
cannot delete each other's output. Commit schemas and generated code together.
Reserve removed field numbers and names; check `buf breaking` against the
target branch before merging. Add compatible fields to v1; a future incompatible
contract needs a deliberate schema/topic migration.

`moderation.outcome_requested` asks User Service to apply one consequence of a
closed report or appeal: a warning, a suspension or its reinstatement, or a
review removal or restoration. Moderation Service publishes it from its
transactional outbox when the report or appeal closes. The key is the user or
review the consequence changes, so a report's outcome and the appeal that
undoes it arrive in order. User Service records each event ID in
`moderation_outcome_events` in the same transaction as the change, and records
an outcome it can never apply (for example, a warning already removed) as
rejected instead of retrying it. See
[the moderation-service ADR](../docs/adr/moderation-service.md).

## Topic provisioning

Add each topic to `infra/kafka/topics.conf` with partitions, replication factor,
and its schema path relative to this directory. The init script validates the
schema exists and the topic matches `<service>.<topic>` before creating topics.
Shared type files do not get topics unless explicitly listed in the manifest.

```sh
docker compose up kafka-init
```

The Compose init job waits for the broker health check and uses
`--create --if-not-exists`. Broker auto-creation is disabled. Rerunning init
creates missing topics but does not change existing topic settings or delete
topics removed from the manifest. Replication factor 1 is for the local
single-broker setup; shared environments need their own settings.

Order publishes immutable facts from its transactional outbox. Credit consumes
`order.outcome` as the sole terminal settlement trigger. Communication consumes
status changes; User owns courier warnings. Consumers deduplicate stable event
IDs and must not assume ordering across topics. Credit also enforces settlement
at most once per Order. See [the design ADR](../docs/adr/order-credit-pubsub.md)
and [Credit conformance](../docs/testing/order-credit-pubsub.md).
