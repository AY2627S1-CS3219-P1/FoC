# Communication Service

Sends FoC's messages to users. It runs the FoC Telegram bot, which links FoC accounts to Telegram chats, sends order updates and relays chat between an order's requester and courier. It also sends all FoC email: magic links for User Service, and order outcomes to users Telegram can't reach.

Also serves authenticated browser updates through
`RealtimeService.WatchChanges`. A separate `communication-realtime` Kafka group
consumes Credit and Order changes and fans out resource invalidations across
replicas using PostgreSQL LISTEN/NOTIFY. This runs without a Telegram token.
Browsers refetch through domain RPCs and recover missed hints on reconnect,
focus, and a slow fallback poll. Without `KAFKA_BROKERS`, the stream reports
`unavailable` and browser fallback reads continue. See the
[architecture](../docs/adr/realtime-invalidation.md) and
[verification](../docs/testing/realtime-invalidation.md) notes.

Telegram is optional. Without `TELEGRAM_BOT_TOKEN`, or if Telegram rejects the token, the service keeps running with the bot disabled: its health check reports `telegram_enabled: false`, and `CreateTelegramLink` returns `unavailable`. Email is optional too: with the default SMTP provider and no `SMTP_ADDR`, `SendMagicLink` returns `unavailable` and notifications go only to Telegram.

## Email

SMTP remains the default (`EMAIL_PROVIDER=smtp`), including local Mailpit. For
AWS SES API delivery, select `EMAIL_PROVIDER=ses`, set `EMAIL_FROM` to a verified
regional sender and `AWS_REGION` to that region. The SDK uses its default
credential chain, including IRSA temporary credentials. `SES_TIMEOUT` defaults
to `5s` when the caller has no deadline. SMTP settings are ignored in this mode.
The selected adapter sends both magic links and queued order notifications;
SES failures never switch delivery to SMTP. The AWS overlay grants the email
role only to Communication Service.

- **Magic links.** User Service calls `EmailService.SendMagicLink` with the address, the complete link and whether it signs in or finishes sign-up. Communication writes the email. The RPC is served only on the internal listener (`INTERNAL_PORT`), never the public one, and requires no access token: the caller hasn't signed in yet. In Kubernetes, an Istio policy lets only User Service reach that port. Any send failure returns `unavailable`, which User Service reports to the user.
- **User emails.** The notification consumer also reads `users.create` and keeps each user's email in `user_emails`. Users created before the consumer first read the topic, and outside Kafka's retention, have no stored email.

## Linking

1. The signed-in frontend calls `TelegramLinkService.CreateTelegramLink` and opens the returned `t.me/<bot>?start=<code>` link.
2. In Telegram, the user presses Start. The bot redeems the code and stores the user's chat ID.
3. The frontend polls `GetTelegramLink` until `linked` is true.

Codes are single-use, expire after 10 minutes, and are stored only as SHA-256 hashes. A new code replaces the user's unused ones. Each FoC account has at most one chat and each chat at most one account; linking again replaces the old pairing. Blocking the bot unlinks the chat. `UnlinkTelegram` unlinks from the app.

## Order notifications

The service consumes `order.status_changed` and `order.offer_made` (consumer group `communication-service`) and messages the participants who didn't cause the change. A second group, `communication-participants`, keeps the chat participants current from `order.status_changed` on its own, so it keeps going while notifications wait for Telegram. Admin and system changes reach both participants. Notifications run when `KAFKA_BROKERS` is set and either the bot started or email is configured.

- **Email fallback.** A recipient Telegram can't reach (no linked chat, a chat that turns out to be blocked, or Telegram disabled) gets outcome and action notifications by email: every always-deliver notification, offer accepted (to the courier), and new offers (to the requester). Progress updates aren't emailed, since the app shows them. The consumer commits fallback to `notification_emails` before acknowledging Kafka. A separate worker sends when the user's email projection is available, including after a restart. Missing addresses remain pending without blocking other recipients; historical users outside Kafka retention still need an email backfill. Temporary delivery failures retry after one minute without pausing Kafka. Permanent SMTP rejections (5xx) and SES message rejections remain recorded as `rejected` with their error and attempt time.

- **At least once.** After each send, `order_notifications_sent` records the event and recipient. For email, recording delivery and deleting the queued row commit together. A redelivered event skips recipients whose message is sent, queued, skipped or permanently rejected, even if their Telegram link changes. Multiple email workers claim separate rows with `FOR UPDATE SKIP LOCKED`. A crash between sending and recording can repeat one message. See [the email delivery decision](../docs/adr/durable-notification-email.md).

Queued new offers expire 30 minutes after the event; queued offer-acceptance notices are skipped if the order has advanced beyond their revision, or if the participants still haven't stored the order 30 minutes after the event. These checks run again before email delivery, including while an address is missing. Skipped rows remain recorded with their reason. Always-deliver emails have no automatic expiry. Inspect the backlog and permanently rejected deliveries with:

```sql
SELECT n.status, (u.user_id IS NULL) AS missing_email,
       count(*) AS notifications, min(n.created_at) AS oldest
FROM notification_emails n
LEFT JOIN user_emails u ON u.user_id = n.user_id
GROUP BY n.status, (u.user_id IS NULL);
```

- **Each notification has a delivery class.** It decides what happens when a message can't go out promptly, during an outage, retries or a consumer restart:

  | Class | Notifications |
  | -- | -- |
  | Drop if late: sent only within 30 minutes of the change (`LateWindow`) | Order open, courier accepted (to the requester), picked up, a courier's new offer (to the requester) |
  | Still current: sent however late, but only while no later status change has been applied to the order. If the participants haven't stored the order yet, it's sent only within the 30-minute window | Offer accepted (to the courier), courier unassigned (to the requester) |
  | Unless reassigned: sent however late, except while the same courier is currently assigned; checked again before queued email delivery | Unassigned (to the removed courier) |
  | Always: sent however late | Delivered, and every outcome: completed, cancelled, expired, failed, rejected |

- **A new environment skips its backlog.** The first time notifications run, `notifications_start` records the time, and earlier events aren't sent. An installation that already handled order events before this table existed starts at 1970 instead, so nothing waiting during its upgrade is skipped. The participants consumer still reads them from the earliest offset to fill in chat participants. A group that loses its offsets, such as after a week-long outage, replays retained events: anything already sent is skipped, and the rest follow their class.
- **Unreachable chats are unlinked.** If Telegram says the user blocked the bot or the chat doesn't exist, the chat is unlinked and the message skipped. Other 4xx rejections are logged and skipped, because they would fail every time.
- **Telegram outages pause notifications.** A 429, 5xx or network error stops the consumer without committing the event. It restarts after 1 second, doubling up to 1 minute, and retries the event, so later events wait until Telegram recovers. A 429 response can extend this delay to Telegram's `retry_after`. Drop-if-late notifications that are still failing after 30 minutes are skipped on a later retry.

## Order chat

A requester and courier chat through the bot without seeing each other's Telegram accounts. Each relayed message is labelled with the sender's role and the order, such as "Your courier (Fine Food order):".

- **Who can chat.** The participants are a local copy of each order, kept in `order_participants` by their own `order.status_changed` consumer (group `communication-participants`). Communication doesn't call Order Service, so chat keeps working while Order is down. Events are applied only if their revision is newer, so replays and late events can't restore an old courier. The copy lags only while its own consumer is behind; a Telegram outage doesn't pause it, so a delayed still-current notification is checked against changes made since.
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
| `INTERNAL_PORT` | required | Internal listener port (`8081`). It serves health and `EmailService`, and is never published. |
| `DATABASE_URL` | required | Postgres URL. Compose sets it from `COMMUNICATION_DATABASE_URL`. |
| `USER_SERVICE_BASE_URL` | required | User Service URL, for verifying access tokens. |
| `RUN_MIGRATIONS` | `true` | Set `false` to skip migrations on startup. |
| `TELEGRAM_BOT_TOKEN` | empty | Bot token from BotFather. Empty disables Telegram. |
| `TELEGRAM_API_URL` | `https://api.telegram.org` | Bot API base URL, for pointing tests at a fake server. |
| `KAFKA_BROKERS` | empty | Comma-separated brokers. Compose sets `kafka:9092`. Empty disables order notifications and browser change streams. |
| `EMAIL_PROVIDER` | `smtp` | Email transport: `smtp` or `ses`. |
| `EMAIL_FROM` | required for SES | Verified SES sender address. |
| `AWS_REGION` | required for SES | Region containing the verified sender. |
| `SES_TIMEOUT` | `5s` | Bounds SES delivery when the caller has no deadline. |
| `SMTP_ADDR` | empty | SMTP relay `host:port`. Compose uses Mailpit at `mailpit:1025`. Empty disables email with the SMTP provider. |
| `SMTP_FROM` | required with `SMTP_ADDR` | Sender address, such as `FoC <no-reply@foc.local>`. |
| `SMTP_USERNAME`, `SMTP_PASSWORD` | empty | Relay credentials, sent only when `SMTP_USERNAME` is set. STARTTLS is used when the server offers it. |
| `SMTP_TIMEOUT` | `5s` | Bounds each SMTP exchange. |

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
| `internal/notify` | Order notification recipients, messages, delivery classes, email fallback and sent records |
| `internal/email` | SMTP sender and the email text Communication writes |
| `internal/users` | Users' emails from `users.create` |
| `internal/chat` | Order participants, chat relay, `/chat` selection and retention |
| `internal/handlers/health` | `HealthService` Connect handler |
| `internal/handlers/telegramlink` | `TelegramLinkService` Connect handler |
| `internal/handlers/accountemail` | Internal `EmailService` Connect handler |
| `internal/router` | Mounts the handlers. On the public listener, everything except health requires an access token; the internal listener serves `EmailService` |

## Database

Stores use sqlc, like Order and Supplier. After changing `migrations` or `queries`, run `make sqlc` in this directory (sqlc v1.30.0) and commit the generated `internal/database/communicationdb` with its sources.

## Tests

`go test ./...` runs everything except the Postgres tests. They need a throwaway database because they truncate the linking and chat tables, so don't point them at the Compose database. Start a separate one, run the tests, then remove it:

```sh
docker run -d --rm --name communication-test-db -p 55432:5432 -e POSTGRES_PASSWORD=test postgres:18
TEST_DATABASE_URL='postgres://postgres:test@localhost:55432/postgres?sslmode=disable' go test ./internal/linking/ ./internal/chat/
docker rm -f communication-test-db
```

The notification tests also need a dedicated broker with `order.status_changed`, `users.create` and `order.offer_made` provisioned. They check Telegram replay after a failed send, and commit an outcome before its user's event, restart the consumer, then verify the later projection delivers exactly one email across an outcome replay. PostgreSQL tests cover missing-user isolation, retries, recorded rejections, concurrent workers, cancellation, atomic completion and migration down/up.

```sh
TEST_DATABASE_URL='postgres://postgres:test@localhost:55432/postgres?sslmode=disable' \
TEST_KAFKA_BROKERS=localhost:9092 go test -tags=integration ./cmd/server/
```
