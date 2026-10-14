# Kafka transport

This package wraps kafka-go with binary Protobuf publishing and typed handlers.
Create dependencies in each service's `main.go` and inject them into operations.
It does not read environment variables, create topics, or start goroutines.

```go
publisher, err := kafka.NewPublisher(kafka.PublisherConfig{
    Brokers: []string{"kafka:9092"},
})
if err != nil { return err }
defer publisher.Close()

err = publisher.Publish(ctx, "users.create", []byte(event.UserId), event)
```

`Publish` marshals a `proto.Message`, hashes its key to select a partition, and
waits for acknowledgement from all in-sync replicas. The writer's write timeout
is five seconds; the caller's context can impose a shorter deadline. A delivery
error does not prove Kafka rejected the message. Retry with the same event ID.
Topic auto-creation is disabled; provision topics separately.

```go
subscriber, err := kafka.NewSubscriber(kafka.SubscriberConfig{
    Brokers: []string{"kafka:9092"},
    GroupID: "credit-service",
})
if err != nil { return err }
err = kafka.Register(subscriber, "users.create",
    func() *eventsv1.UserCreated { return new(eventsv1.UserCreated) },
    func(ctx context.Context, meta kafka.Metadata, event *eventsv1.UserCreated) error {
        return logic.HandleUserCreated(ctx, event)
    },
)
if err != nil { return err }
return subscriber.Run(ctx)
```

Import `kafka` from `github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka` and
`eventsv1` from `github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1`.
These fragments illustrate composition; they do not wire an existing service.

## Consumption and lifecycle

Register one handler per topic before `Run`. The factory must return a fresh
message each time. `Run` subscribes to every registered topic and processes
deliveries sequentially across the subscription. It blocks until cancellation
or failure and can only be called once. The server owns starting it alongside
HTTP serving, monitoring its returned error, and cancelling and waiting for it
during shutdown. The reader is closed when `Run` exits. Handlers must respect
their context so cancellation can finish.

Replicas of one consumer share a group ID; independent services use different
groups. New groups start at the earliest retained offset, while existing groups
resume their committed offsets. A handler's successful return is followed by a
synchronous offset commit. Decode, handler, fetch, or commit failure stops `Run`;
create a new subscriber to restart. Handler/decode failures do not commit their
offsets. Malformed messages remain pending until repaired or handled operationally.

There are no application-level retries, dead-letter routing, or compensating
actions. kafka-go retains its built-in transport retry behavior.

## Saga and database responsibilities

Consumption is at least once. A crash after committing business changes but
before committing the offset can repeat the handler. In a service transaction,
record the processed event ID, apply business changes, and enqueue outgoing
events in an outbox together. Duplicate events should return success without
repeating the business changes. Services implement this persistence themselves.

A business rejection can commit a rejection state and an outgoing failure
event, then return success. Technical failures return an error and leave the
delivery uncommitted. The wrapper does not generate event IDs, correlate saga
replies, publish replies automatically, or guarantee exactly-once effects.
Protobuf decoding checks the wire format; business validation belongs in handlers.

## Tests

```sh
cd pkg
go test ./...
go test -race ./kafka
go vet ./...
```

The integration test requires an isolated broker and an empty, pre-created
topic. It publishes one test message, fails its first handler, then verifies
replay and acknowledgement using the same unique consumer group.

```sh
TEST_KAFKA_BROKERS=localhost:9092 TEST_KAFKA_TOPIC=wrapper-test \
    go test ./kafka -run TestKafkaIntegration -count=1
```
