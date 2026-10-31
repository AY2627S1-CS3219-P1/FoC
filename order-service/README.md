# Order Service

Order implements authenticated reads, `CreateOrder`, `CreateItemImageUpload`,
durable creation recovery and Kafka outbox publication on foundation #135.
Assigned couriers can confirm pickup; requesters can confirm receipt. Both actions
use local transactions and receipt emits the settlement outcome. Repeats by the
same participant return the stored Order without another write.
Reservation uses Credit RPCs; terminal settlement is driven by `order.outcome`.
See the [design ADR](../docs/adr/order-credit-pubsub.md) and
[Credit conformance](../docs/testing/order-credit-pubsub.md). Later lifecycle RPCs,
minimum courier rating and the browser creation flow remain follow-ups.

## Run the executable tests

With Docker available, run from the repository root:

```sh
GOCACHE=/tmp/foc-go-build make -C order-service test-creation
```

This starts disposable PostgreSQL 18, Kafka 4.2.2 and Floci 2.3.0 containers.
Floci verifies S3 signatures and keeps buckets private. Supplier and Credit are
explicitly scripted Connect servers in these tests; they exercise the real Order
handlers, clients, database, uploads and publisher, without claiming an actual
Credit backend or production workload-identity deployment.

Coverage includes fifty concurrent duplicate creations, per-account limits,
changed/retained retries, expiry, unknown financial results, process-instance
recovery after remote success/local rollback, S3 size/type/overwrite enforcement,
atomic image attachment, image access, cleanup, Kafka delivery and recovery after
lost broker responses or local acknowledgement failures. Terminal relay tests
use the existing aggregate to produce real committed facts; terminal mutation
RPCs remain unimplemented.

`make -C order-service test-integration` runs all database/infrastructure tests.
Do not set `TEST_DATABASE_URL` to a valuable database: foundation tests **delete
its public schema**. Without it, disposable databases are used. `make fmt`,
`make lint`, `make buf-lint`, `make buf-generate` and `make -C order-service sqlc`
are the normal repository checks; generated output is committed with its sources.

## Local services

Copy `.env.example` to `.env`, configure database/authentication settings, then:

```sh
docker compose up -d postgres kafka kafka-init floci order-storage-init
docker compose up --build user-service supplier-service order-service
```

Order listens on port 8083 by default. Health is public and checks PostgreSQL.
For an existing Postgres volume, create `order_dev` once if missing; the database
init script runs only on fresh volumes. User Service public keys are needed to
serve protected RPCs. Remote Credit/Kafka/S3 reachability is not a startup probe.
Kafka downtime retains outgoing facts; image storage failures do not create
invalid image attachments. Embedded migrations run unless `RUN_MIGRATIONS=false`.
Migration 00002 defines creation workflows and the shared idempotency key table;
00003 adds images and relay claims. These definitions are for fresh databases;
Goose does not rerun an edited migration on databases where it was already applied.

Compose configures local Floci with deterministic `test`/`test` credentials and
an explicit bucket/CORS initialization job. The bucket is private. Use the
standard AWS credential chain and separately provisioned bucket/CORS in deployed
environments. Local CORS permits localhost/127.0.0.1 port 5173; provision the
appropriate origins when using another frontend port. `storage-init` refuses to
run outside `APP_ENV=local`. Image bodies persist in the `order-images` volume.

Host-run configuration:

```sh
export DATABASE_URL='postgresql://foc:changeme@localhost:5432/order_dev?sslmode=disable'
export USER_SERVICE_BASE_URL=http://localhost:8081
export SUPPLIER_SERVICE_BASE_URL=http://localhost:8082
export APP_ENV=local
export KAFKA_BROKERS=localhost:9092
export ORDER_S3_BUCKET=order-images
export ORDER_S3_ENDPOINT=http://localhost:4566
export AWS_REGION=us-east-1
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
```

`ORDER_S3_PUBLIC_ENDPOINT` controls the hostname in browser upload/download URLs;
`ORDER_S3_ENDPOINT` is the server-side endpoint. Signed hosts cannot be rewritten
after signing. Compose uses `floci:4566` internally and `localhost:4566` publicly.
Service URLs may use HTTP for internal discovery; public S3 URLs require HTTPS
outside local mode.

## Credit dependency and service authentication

Live creation additionally requires the actual Credit Reserve/GetReservation and
creation-compensation backend:

```sh
export CREDIT_SERVICE_BASE_URL=http://credit-service:8081
```

In Kubernetes, Order calls Credit's internal ClusterIP Service. Istio mTLS
authenticates the `order-service` Kubernetes ServiceAccount, and an
AuthorizationPolicy rejects other callers on Credit's internal listener. The
application does not send a workload bearer token. Compose keeps the internal
listener off published host ports and relies on its private service network.
User JWTs are used only for delegated Supplier reads and are never sent to Credit
or persisted. Credit's internal RPC handler still has no application-level
credential verification; see the tracked
[unauthenticated-listener landmine](../LANDMINES/credit-internal-listener-unauthenticated.md).

New creation is available when Supplier and Credit are configured; there is no
admission feature flag. Reads and image uploads can run without Credit. An
existing pending financial workflow requires Credit recovery configuration at
startup. A configured Credit outage leaves durable Pending Reservation work.
The Kubernetes smoke check verifies that only the Order workload identity can
reach Credit's internal listener; it does not claim the full D3 business flow
has been exercised end to end.

## Test the API manually

Use a real User Service access token in `USER_TOKEN`. Ask for a signed upload:

```sh
curl --fail-with-body http://localhost:8083/order.v1.OrderService/CreateItemImageUpload \
  -H "Authorization: Bearer $USER_TOKEN" -H 'Content-Type: application/json' \
  -d '{"contentType":"image/png","sizeBytes":"12345"}'
```

Use the file's exact byte count. PUT its bytes to the returned `uploadUrl` with
all `uploadHeaders`; browsers supply Content-Length from the File/Blob. The grant
lasts ten minutes and cannot overwrite an existing object. A lost successful PUT
response may yield 412 on retry; CreateOrder checks the object before attaching.
The supported types are JPEG, PNG, WebP, HEIC and HEIF. Files are limited to
10,000,000 bytes and each creation accepts at most five distinct image IDs.

Call `CreateOrder` with the existing contract fields, a UUID `idempotencyKey`,
valid Supplier pickup/delivery UUIDs and a future `expiresAt`. Include returned
image IDs in `itemImageIds`, or omit them for a text-only order. A confirmed hold
opens the Order; an unknown result returns Pending Reservation. Use GetOrder or
ListMyOrders to inspect it. An identical retained retry reuses the workflow,
including after its original deadline. Changed input returns already_exists.
Retries do not consume the ten-new-orders-per-minute quota. Object keys remain
internal; authorized reads return five-minute signed download URLs.

## Contributor boundaries

Keep operations in `internal/order`, adapters at their consuming boundaries and
composition in `cmd/server`. Never perform network calls inside Order transaction
callbacks. Lock Order first and read relationships in another statement; see the
locking-snapshot landmine. Creation, images, retry mapping, audit and facts commit
atomically. Recovery checks its persisted generation/phase after reacquiring the
lock. Unknown reservations are compensated before expiry rejection; Rejected
emits no terminal outcome.

The relay claims the earliest outstanding revision/ordinal per Order and marks
publication only after broker acknowledgement. A crash can replay an old event;
consumers deduplicate event IDs and Credit enforces one financial terminal effect
per Order. Different topics have no common ordering guarantee. Pending creation
recovers immediately on startup, with bounded calls, leased claims and backoff.
Unattached uploads older than 24 hours are cleaned up; attached history is retained.
Credit owns terminal settlement and startup/five-minute reconciliation and alerts.

The shared `order_idempotency_keys` table uses `(caller_id, method, idempotency_key)`
as its primary key and stores `request_hash`, `resource_id` and expiry. Creation
references its workflow; future MakeOffer will reference its offer in the same
table. Workflow payload, progress and result remain separate. Insert resource and
key in one transaction. Resolve the resource before deciding expiry; a missing
resource is an integrity error, never permission to execute again. There is no
generic expired-key cleanup that could remove unresolved creation mappings.
