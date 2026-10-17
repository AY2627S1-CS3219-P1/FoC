# Communication Service

Runs the FoC Telegram bot. It links FoC accounts to Telegram chats and sends order updates; chat relay will follow.

Telegram is optional. Without `TELEGRAM_BOT_TOKEN`, or if Telegram rejects the token, the service keeps running with the bot disabled: its health check reports `telegram_enabled: false`, and `CreateTelegramLink` returns `unavailable`.

## Linking

1. The signed-in frontend calls `TelegramLinkService.CreateTelegramLink` and opens the returned `t.me/<bot>?start=<code>` link.
2. In Telegram, the user presses Start. The bot redeems the code and stores the user's chat ID.
3. The frontend polls `GetTelegramLink` until `linked` is true.

Codes are single-use, expire after 10 minutes, and are stored only as SHA-256 hashes. A new code replaces the user's unused ones. Each FoC account has at most one chat and each chat at most one account; linking again replaces the old pairing. Blocking the bot unlinks the chat. `DeleteTelegramLink` unlinks from the app.

## Order notifications

The service consumes `order.status_changed` (consumer group `communication-service`) and messages the participants who didn't cause the change. Admin and system changes reach both participants. Users without a linked chat are skipped. Notifications run only when the bot started and `KAFKA_BROKERS` is set.

- **At least once.** After each send, `order_notifications_sent` records the event and recipient, and a redelivered event skips recorded recipients. A crash between sending and recording can repeat one message.
- **Stale events are skipped.** Events more than an hour old aren't sent. This also stops a new consumer group, which starts from the earliest offset, from sending an old backlog.
- **Unreachable chats are unlinked.** If Telegram says the user blocked the bot or the chat doesn't exist, the chat is unlinked and the message skipped. Other 4xx rejections are logged and skipped, because they would fail every time.
- **Telegram outages pause notifications.** A 429, 5xx or network error stops the consumer without committing the event. It restarts after 1 second, doubling up to 1 minute, and retries the event, so later events wait until Telegram recovers.

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
| `PORT` | `8080` | HTTP port inside the container. Compose maps it to `COMMUNICATION_PORT` (`8084`). |
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
| `internal/database` | Postgres pool and migration runner |
| `internal/telegram` | Bot API client and long-polling loop |
| `internal/bot` | Handles `/start` and blocked chats |
| `internal/linking` | Link codes and links, with the Postgres store |
| `internal/notify` | Order notification recipients, messages and sent records |
| `internal/handlers/health` | `HealthService` Connect handler |
| `internal/handlers/telegramlink` | `TelegramLinkService` Connect handler |
| `internal/router` | Mounts the handlers; everything except health requires an access token |

## Tests

`go test ./...` runs everything except the Postgres tests, which need a throwaway database because they truncate the linking tables:

```sh
TEST_DATABASE_URL='postgres://postgres:test@localhost:55432/postgres?sslmode=disable' go test ./internal/linking/
```

The notifications test also needs a broker with the `order.status_changed` topic. It runs the consumer against a fake Telegram API that fails the first send, then checks the replay sends once:

```sh
TEST_DATABASE_URL='postgres://postgres:test@localhost:55432/postgres?sslmode=disable' \
TEST_KAFKA_BROKERS=localhost:9092 go test -tags=integration ./cmd/server/
```
