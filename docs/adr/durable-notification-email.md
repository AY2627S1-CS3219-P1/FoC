# Durable email fallback for order notifications

Order outcomes and user-created events arrive on different Kafka topics.
There is no ordering guarantee between them, so the user email projection may
be missing when an outcome is handled. Both topics share a sequential subscriber;
retrying the outcome there can prevent its user event from being processed.

## Decision

Communication commits eligible email fallback to `notification_emails` before
acknowledging the order event. The event/recipient primary key makes acceptance
idempotent. Delivery checks consider queued and permanently rejected emails
accepted as well as sent messages, so replay cannot switch them to Telegram.

An independent worker joins pending rows to `user_emails`. Unknown recipients
remain pending, without preventing ready recipients from being processed. A
later projection or email backfill makes them eligible on the next poll; no
in-memory wake-up or replay of the outcome is required.

Each SMTP attempt holds a PostgreSQL row lock. `SKIP LOCKED` permits multiple
workers without expiring a lease while its original sender is still running.
After locking a row, a separate sent-record lookup sees any completion that
committed while a concurrent replay INSERT was waiting behind its deletion.
Such stale replay rows are removed without another SMTP attempt.
SMTP's configured timeout bounds the network exchange. Sending successfully
records `order_notifications_sent` and deletes the queued row in one transaction.
Temporary SMTP failures reschedule that row after one minute. Permanent SMTP
rejections retain a `rejected` row with the error and attempt time for inspection.
Both failures leave the Kafka consumer free to project later user events.

Delivery remains at least once: SMTP cannot join the database transaction, so
a crash after SMTP acceptance and before commit may repeat a message. A rollback
or shutdown preserves work for the next worker. Magic links remain synchronous.

Pending and rejected rows are not automatically expired. Users whose creation
events have left Kafka retention still need an email backfill; their notifications
remain visibly pending until then. This change does not implement that backfill.

The email worker starts as soon as mail delivery is configured, independently
of bot readiness and Kafka connectivity, so accepted backlog can recover even
while Telegram's startup check is retrying.

When SES API delivery is selected, the same worker and queue use Communication's
SES adapter. A typed SES `MessageRejected` error retains a rejected row; transport,
throttling and account-availability errors retry. SES acceptance has the same
send-before-database-commit window as SMTP, and failures never switch providers.
