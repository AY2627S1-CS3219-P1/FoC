# ADR: Telegram chat records and update acknowledgement

Date: 21 Oct 2026
Status: implemented in #151; pending review

The chat relay writes a dispute record before contacting Telegram. A record
represents an accepted delivery attempt, including failed attempts; the existing
`sent_at` column records acceptance time. Inserts are idempotent by message UUID
so a lost database response can be retried with the same ID.

The poller acknowledges an update only after any retry continuation finishes.
Continuations resume the unfinished stage: pre-send persistence, post-send reply
target persistence, or feedback to the sender. Retrying a reply-target write does
not resend a successfully delivered message. Retrying a failure notice does not
retry the original recipient delivery. Later updates wait behind the unfinished
update. Telegram rate-limit delays take precedence over the polling backoff;
permanent feedback rejections are acknowledged.

Continuations are held in memory. After a process crash, Telegram can redeliver
an unacknowledged update and repeat a send. A message delivered before its reply
target was saved can remain without that routing entry. Its pre-send dispute
record survives. These changes do not provide exactly-once delivery or a durable
outbox for routing recovery.

Chat eligibility and current Telegram links are rechecked before a delayed send
resumes. Reply targets for active orders survive cleanup regardless of message
age; ended orders use the retention period measured from their end time.
