# Credit Service

Credit owns users' credit accounts, the ledger, and the reservation that holds
a requester's credits for each order. The behaviour follows the `credit.v1`
contracts in [`proto/credit/v1`](../proto/credit/v1) and
[`docs/adr/credit-reservation-holds.md`](../docs/adr/credit-reservation-holds.md).

## What runs

`cmd/server` applies the embedded migrations, then runs two listeners and the
Kafka consumers.

| Listener | Default port | Serves | Authentication |
| --- | --- | --- | --- |
| Public | `EXTERNAL_PORT` (8080) | `credit.v1.CreditAccountService`, `grpc.health.v1.Health` | User Service JWT |
| Internal | `INTERNAL_PORT` (8081) | `credit.v1.CreditReservationService`, `grpc.health.v1.Health` | Istio workload principal in Kubernetes; no app credential |

The internal listener serves Order's reservation calls and does not verify an
application credential. Kubernetes authenticates the caller with Istio mTLS and
allows only `cluster.local/ns/foc/sa/order-service` to reach port 8081. Compose
publishes only Credit's public listener and keeps the internal one on its private
network. See [the landmine](../LANDMINES/credit-internal-listener-unauthenticated.md).

| Topic | Effect |
| --- | --- |
| `order.outcome` | Settles or releases the order's reservation. This is the only terminal settlement trigger. |
| `users.create` | Opens the user's account with the 100 starting credits. |

Both consumers share the `credit-service` group. Redelivery is harmless:
settled and released reservations never change, and the grant is posted only
when an account is created. Outcomes that contradict Credit's records are
logged at error level and acknowledged, so they don't block the partition.
These are an outcome mismatch, a payout for an unknown or unassigned
reservation, and an invalid payload. Database failures stop the subscription,
which restarts with backoff and redelivers the uncommitted event.

## Configuration

| Variable | Required | Notes |
| --- | --- | --- |
| `DATABASE_URL` | Yes | Credit's PostgreSQL database. |
| `USER_SERVICE_BASE_URL` | Yes | Origin used to fetch JWT keys; may use HTTP for internal service discovery. |
| `EXTERNAL_PORT` | No | Public listener port; defaults to 8080. |
| `INTERNAL_PORT` | Yes | Private reservation listener; required and must differ from `EXTERNAL_PORT`. |
| `RUN_MIGRATIONS` | No | `true` by default. |
| `KAFKA_BROKERS` | No | Comma-separated. Without it nothing is consumed, and reservations stay held until it is set. |

## Layout

- `internal/account`, `internal/reservation`: domain rules and Postgres persistence.
- `internal/rpc/<domain>`: Connect handlers. `internal/rpc/shared` maps domain
  errors to Connect codes and `credit.v1` ErrorInfo reasons.
- `internal/events`: Kafka handlers.
- `internal/router`: mounts the public and internal listeners.

## Development

```sh
docker compose up credit-service   # public listener on CREDIT_PORT (8085)
make test                          # unit tests
make test-integration              # needs Docker for testcontainers
make sqlc                          # after changing database/query or database/schema
```
