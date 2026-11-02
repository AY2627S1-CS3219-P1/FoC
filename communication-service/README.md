# Communication Service

Runs the FoC Telegram bot. It links FoC accounts to Telegram chats, sends order updates, and relays chat between an order's requester and courier.

Telegram is optional. Without `TELEGRAM_BOT_TOKEN`, or if Telegram rejects the token, the service keeps running with the bot disabled: its health check reports `telegram_enabled: false`, and `CreateTelegramLink` returns `unavailable`.

## Linking

1. The signed-in frontend calls `TelegramLinkService.CreateTelegramLink` and opens the returned `t.me/<bot>?start=<code>` link.
2. In Telegram, the user presses Start. The bot redeems the code and stores the user's chat ID.
3. The frontend polls `GetTelegramLink` until `linked` is true.

Codes are single-use, expire after 10 minutes, and are stored only as SHA-256 hashes. A new code replaces the user's unused ones. Each FoC account has at most one chat and each chat at most one account; linking again replaces the old pairing. Blocking the bot unlinks the chat. `UnlinkTelegram` unlinks from the app.

## Order notifications

The service consumes `order.status_changed` (consumer group `communication-service`) and messages the participants who didn't cause the change. Admin and system changes reach both participants. Users without a linked chat are skipped. Notifications run only when the bot started and `KAFKA_BROKERS` is set.

- **At least once.** After each send, `order_notifications_sent` records the event and recipient, and a redelivered event skips recorded recipients. A crash between sending and recording can repeat one message.
- **Stale events are skipped.** Events more than an hour old aren't sent. This also stops a new consumer group, which starts from the earliest offset, from sending an old backlog.
- **Unreachable chats are unlinked.** If Telegram says the user blocked the bot or the chat doesn't exist, the chat is unlinked and the message skipped. Other 4xx rejections are logged and skipped, because they would fail every time.
- **Telegram outages pause notifications.** A 429, 5xx or network error stops the consumer without committing the event. It restarts after 1 second, doubling up to 1 minute, and retries the event, so later events wait until Telegram recovers. A 429 response can extend this delay to Telegram's `retry_after`. The one-hour stale-event cutoff still applies during retries; delivery classification is tracked in [#200](https://github.com/AY2627S1-CS3219-P1/FoC/issues/200).

## Order chat

A requester and courier chat through the bot without seeing each other's Telegram accounts. Each relayed message is labelled with the sender's role and the order, such as "Your courier (Fine Food order):".

- **Who can chat.** The participants are a local copy of each order, kept in `order_participants` by the same `order.status_changed` consumer as notifications. Communication doesn't call Order Service, so chat keeps working while Order is down. Events are applied only if their revision is newer, so replays and late events can't restore an old courier. The copy can lag while the consumer is paused.
- **When.** From assignment (Awaiting Pickup) until Delivered, and for an hour after a Completed, Cancelled or Failed order ends.
- **Which order.** A reply to a relayed message or a notification goes to that message's order. Otherwise the message goes to the order chosen with `/chat`, or to the user's only open chat. A user with several open chats who hasn't chosen one is shown buttons to choose. Sending a message also makes its order the sender's current choice.
- **Failures.** The sender is told when the other person hasn't connected Telegram, has blocked the bot (their chat is then unlinked), or a send failed transiently. If the failure notice also fails transiently, the poller retries that notice before acknowledging the update. Later updates wait until it succeeds or Telegram permanently rejects it. Only text is relayed.
- **Dispute records.** The relay stores each accepted message attempt in `chat_messages` before sending it. Records include failed delivery attempts; `sent_at` is the acceptance time, not proof of delivery. Storage retries reuse the same record ID. After a successful send, reply-target writes retry without resending the message. Retry continuations are held in memory: a process crash can cause Telegram to redeliver an unacknowledged update and repeat a send, or leave the earlier message without a reply target. The dispute record survives either case.
- **Retention.** An hourly job deletes message records and reply targets 30 days after their order ends, plus notification records older than 30 days. Reply targets for active orders with a courier and a chat-eligible status remain available regardless of message age. Targets without an order projection, courier, or chat-eligible status expire 30 days after creation unless the order has an end time; ended orders use the 30-day retention period from that time. Message records without an order projection expire 30 days after their acceptance time (`sent_at`). Selections are removed when their chat closes or the order projection no longer exists.

## Run locally

1. Create your own test bot: message [@BotFather](https://t.me/BotFather), send `/newbot`, and copy the token. Telegram lets only one process poll a bot at a time, so don't share a token with teammates.
2. Set `TELEGRAM_BOT_TOKEN` and `COMMUNICATION_DATABASE_URL` in the root `.env` (see `.env.example`).
3. If your Postgres volume existed before this service, create its database once (see [LANDMINES](../LANDMINES/new-service-database.md)):

   ```sh
   docker compose exec postgres psql -U foc -c 'CREATE DATABASE communication_dev'
   ```

4. Start the service:

   ```sh
   docker compose up -d communication-service
   ```

5. Send `/start` to your bot in Telegram. It replies with a greeting.

The bot uses long polling, so it needs no public URL. Migrations in `migrations/` are applied on startup unless `RUN_MIGRATIONS=false`.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `PORT` | `8080` | Public HTTP port inside the container. Compose maps it to `COMMUNICATION_PORT` (`8084`). |
| `DATABASE_URL` | required | Postgres URL. Compose sets it from `COMMUNICATION_DATABASE_URL`. |
| `USER_SERVICE_BASE_URL` | required | User Service URL, for verifying access tokens. |
| `RUN_MIGRATIONS` | `true` | Set `false` to skip migrations on startup. |
| `TELEGRAM_BOT_TOKEN` | empty | Bot token from BotFather. Empty disables Telegram. |
| `TELEGRAM_API_URL` | `https://api.telegram.org` | Bot API base URL, for pointing tests at a fake server. |
| `KAFKA_BROKERS` | empty | Comma-separated brokers. Compose sets `kafka:9092`. Empty disables order notifications. |

## Health check

```sh
curl -X POST localhost:8084/communication.v1.HealthService/Check -H 'Content-Type: application/json' -d '{}'
```

## Layout

| Path | Contents |
|---|---|
| `cmd/server` | Configuration, startup and shutdown |
| `migrations` | Goose migrations, embedded in the binary |
| `queries` | sqlc queries |
| `internal/database` | Postgres pool and migration runner |
| `internal/database/communicationdb` | Generated by sqlc; don't edit |
| `internal/telegram` | Bot API client and long-polling loop |
| `internal/bot` | Handles `/start` and blocked chats |
| `internal/linking` | Link codes and links, with the Postgres store |
| `internal/notify` | Order notification recipients, messages and sent records |
| `internal/chat` | Order participants, chat relay, `/chat` selection and retention |
| `internal/handlers/health` | `HealthService` Connect handler |
| `internal/handlers/telegramlink` | `TelegramLinkService` Connect handler |
| `internal/router` | Mounts the handlers; everything except health requires an access token |

## Database

Stores use sqlc, like Order and Supplier. After changing `migrations` or `queries`, run `make sqlc` in this directory (sqlc v1.30.0) and commit the generated `internal/database/communicationdb` with its sources.

## Tests

`go test ./...` runs everything except the Postgres tests. They need a throwaway database because they truncate the linking and chat tables, so don't point them at the Compose database. Start a separate one, run the tests, then remove it:

```sh
docker run -d --rm --name communication-test-db -p 55432:5432 -e POSTGRES_PASSWORD=test postgres:18
TEST_DATABASE_URL='postgres://postgres:test@localhost:55432/postgres?sslmode=disable' go test ./internal/linking/ ./internal/chat/
docker rm -f communication-test-db
```

The notifications test also needs a broker with the `order.status_changed` topic. It runs the consumer against a fake Telegram API that fails the first send, then checks the replay sends once:

```sh
TEST_DATABASE_URL='postgres://postgres:test@localhost:55432/postgres?sslmode=disable' \
TEST_KAFKA_BROKERS=localhost:9092 go test -tags=integration ./cmd/server/
```
