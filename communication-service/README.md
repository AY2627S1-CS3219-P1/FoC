# Communication Service

Runs the FoC Telegram bot. It links FoC accounts to Telegram chats; order notifications and chat relay will follow.

Telegram is optional. Without `TELEGRAM_BOT_TOKEN`, or if Telegram rejects the token, the service keeps running with the bot disabled: its health check reports `telegram_enabled: false`, and `CreateTelegramLink` returns `unavailable`.

## Linking

1. The signed-in frontend calls `TelegramLinkService.CreateTelegramLink` and opens the returned `t.me/<bot>?start=<code>` link.
2. In Telegram, the user presses Start. The bot redeems the code and stores the user's chat ID.
3. The frontend polls `GetTelegramLink` until `linked` is true.

Codes are single-use, expire after 10 minutes, and are stored only as SHA-256 hashes. A new code replaces the user's unused ones. Each FoC account has at most one chat and each chat at most one account; linking again replaces the old pairing. Blocking the bot unlinks the chat. `DeleteTelegramLink` unlinks from the app.

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
| `internal/handlers/health` | `HealthService` Connect handler |
| `internal/handlers/telegramlink` | `TelegramLinkService` Connect handler |
| `internal/router` | Mounts the handlers; everything except health requires an access token |

## Tests

`go test ./...` runs everything except the Postgres tests, which need a throwaway database because they truncate the linking tables:

```sh
TEST_DATABASE_URL='postgres://postgres:test@localhost:55432/postgres?sslmode=disable' go test ./internal/linking/
```
