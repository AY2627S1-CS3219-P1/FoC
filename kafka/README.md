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
| `users.create` | `users.events.v1.UserCreated` | User UUID as UTF-8 text |
| `users.user_deleted` | `users.events.v1.UserDeleted` | User UUID as UTF-8 text |

`users.create` announces a newly created user. Its payload contains the event
ID, user ID, email, display name, and creation timestamp. The event ID is stable
across retries; consumers should deduplicate it. User Service publishes this
event from its transactional outbox after registration commits. Credit consumes
it to open the user's account with the starting credits.

`users.user_deleted` announces committed hard deletion, rather than a request
to delete. The GORM deletion outbox commits with account removal and has no
users FK. The value contains only a stable event ID, user ID, deletion time and
server-generated deletion operation ID (`deletion_request_id`). Publication is
at least once; consumers deduplicate event IDs. Kafka outages leave a pending
fact for retry and do not roll back an already successful account deletion.

The topic is an integration contract, not an instruction to erase reviews,
attached review photos, opaque order IDs, Credit accounts or financial history.
Consumers that keep copied account data own their cleanup. Retain a local
deletion marker if needed to handle a delayed `users.create`: the two topics
have no shared ordering guarantee, and replay must not recreate grants or
resurrect copied profiles. Existing Credit settlement remains driven by its
own durable reservation/ledger data and `order.outcome`, without looking up a
live user profile. This change publishes the deletion fact; it does not add a
system-wide cleanup coordinator or subscriber acknowledgements.

Order publishes its three topics from its outbox. Topic names match the outbox
fact kinds. `order.status_changed` drives Communication's notifications,
`order.outcome` reports how an order ended, and
`order.courier_warning_requested` asks User Service to warn a courier.

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
