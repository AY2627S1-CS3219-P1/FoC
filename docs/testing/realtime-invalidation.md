# Live-update verification

The change covers Credit and Order event production, Communication's stream,
and the mounted frontend reads. Streaming delivery is advisory; existing read
APIs remain authoritative.

## Backend

Run ordinary tests with `GOWORK=off go test -mod=readonly -race ./...` in Order,
Credit, and Communication. Connect integration tests require local socket access.
Use writable `GOCACHE` and `GOLANGCI_LINT_CACHE` in restricted environments, as
documented in LANDMINES.

Order's integration suite uses PostgreSQL 18 and **resets the supplied database
schema**. Point `TEST_DATABASE_URL` only at a disposable database:

Order migration `00010_change_events.sql` follows the offer-publication migration
`00009_offer_made_facts.sql`. The integration suite downgrades to version 9 with
an unpublished offer fact, reapplies version 10, and verifies both offer facts
and advisory changes can still be recorded. Rolling back version 10 removes
only `order.changed` rows and retains `order.offer_made` support.

```sh
cd order-service
GOWORK=off go test -mod=readonly -tags=integration -p 1 ./internal/order -count=1 -timeout=10m
```

Credit's additional integration test starts and removes its own PostgreSQL 18
testcontainer. It drives actual reservation, reversal, release, and settlement
operations and checks all seven ledger kinds, no-op/replay behavior, rollback,
and publication with a lost broker acknowledgement:

```sh
cd credit-service
GOWORK=off go test -mod=readonly -tags=integration ./internal/events -count=1 -timeout=4m
```

For replica fanout, provision `credit.account_changed` and `order.changed` in an
isolated Kafka broker and set `TEST_KAFKA_BROKERS` plus `TEST_DATABASE_URL`:

```sh
cd communication-service
GOWORK=off go test -mod=readonly -race ./internal/realtime -count=1 -timeout=2m
```

This uses two independent hubs and HTTP servers sharing only PostgreSQL and
Kafka. It verifies authenticated ready/change frames on the production router,
then terminates real LISTEN sessions and checks stream failure and reconnection.
The test has no Telegram dependency. Additional tests cover scoped recipients,
bounded/coalesced work, slow subscriptions, and token expiry. The server transport
test checks Connect JSON over HTTP/1.1 and native gRPC over h2c with the production
server configuration and CORS middleware.
It also checks an idle HTTP/2 period longer than the write timeout: per-write
deadlines must be cleared afterward, or Go resets an otherwise healthy stream.

## Frontend

Run `npm run check`, `npm test`, and `npm run build` in `frontend`. Start its dev
server before running `npm run test:wireframe` and `npm run test:realtime`.

The existing wireframe suite mocks streaming as unavailable and checks normal
flows plus File-object preservation across refresh/revision changes. The realtime
suite binds a temporary Connect JSON streaming fixture on localhost:8084; domain
RPCs use shared browser fixtures. It checks two users and multiple tabs for
credit isolation, offers without draft loss, pending acceptance read-only lookup,
marketplace removal, participant progress, independent settlement, missed-hint
recovery, expiry updates, and fallback during stream failure. It asserts no
uncaught browser errors. This is browser integration, not a real business saga.

## Deployment boundary

Both local and production application overlays must render the explicit
`/communication.v1.RealtimeService` route with request/backend timeouts `0s`.
`make k8s-smoke` uses the existing public Communication health RPC and a new
authenticated framed-stream probe; it verifies that ready is flushed through
the actual deployed Gateway. Rendering and localhost transport tests do not
replace running that smoke check after deployment.

Deploy migrations and Kafka topics before updated services, then deploy the
frontend. The new stream is independent of optional Telegram configuration.
