# Communication Service

Runs the FoC Telegram bot. It will link FoC accounts to Telegram chats, send order notifications and relay chat between an order's participants. This skeleton serves a health check and answers `/start`.

Telegram is optional. Without `TELEGRAM_BOT_TOKEN`, or if Telegram rejects the token, the service keeps running with the bot disabled, and its health check reports `telegram_enabled: false`.

## Run locally

1. Create your own test bot: message [@BotFather](https://t.me/BotFather), send `/newbot`, and copy the token. Telegram lets only one process poll a bot at a time, so don't share a token with teammates.
2. Set `TELEGRAM_BOT_TOKEN` in the root `.env`.
3. Start the service:

   ```sh
   docker compose up -d communication-service
   ```

4. Send `/start` to your bot in Telegram. It replies with a greeting.

The bot uses long polling, so it needs no public URL.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `PORT` | `8080` | HTTP port inside the container. Compose maps it to `COMMUNICATION_PORT` (`8084`). |
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
| `internal/telegram` | Bot API client and long-polling loop |
| `internal/bot` | Replies to messages sent to the bot |
| `internal/handlers/health` | `HealthService` Connect handler |
| `internal/router` | Mounts the Connect handlers |
