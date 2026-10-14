# Kafka contracts

Kafka message contracts live here, separately from the Connect RPC contracts
under `proto/`. Files use `<service>/events/v1/<topic>.proto`, with package
`<service>.events.v1`. The service is the producer/owner. Use `.proto`, and
declare messages and enums without RPC services.

| Topic | Value message | Key |
| --- | --- | --- |
| `users.create` | `users.events.v1.UserCreated` | User UUID as UTF-8 text |

`users.create` announces a newly created user. Its payload contains the event
ID, user ID, email, display name, and creation timestamp. The event ID is stable
across retries; consumers should deduplicate it. Producers and consumers have
not yet been wired up.

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
