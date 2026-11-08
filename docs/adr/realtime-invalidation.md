# ADR: Domain events and advisory browser invalidation

Date: 8 Nov 2026
Status: accepted

Credits, orders, offers, and background operations can change while a browser
remains on the same page. Navigation refresh and individual polling leave
inconsistent views and duplicate refresh work.

## Publication

Services record events in their local transaction and publish through their
outbox/Kafka pipeline. Order extends its existing outbox with `order.changed`;
Credit publishes `credit.account_changed` from its first domain outbox. This
follows one backend publishing convention. Durable publication supports that
consistency; it is not required for browser correctness. There is no additional
in-memory publisher or UI-only outbox.

Credit records an event beside each nonzero ledger posting, including the
starting grant. The ledger UUID is its stable event ID. Replayed operations and
zero postings emit nothing extra. Changes and event intent roll back together.
Publication uses leased claims, broker acknowledgements, and stable IDs on retry.

Order combines observable changes per order per transaction, including receipt
rejection and operation state changes without an aggregate revision increase.
Internal lease/retry bookkeeping emits nothing. Recipients are captured after
the parent lock: requester, current/historical couriers, offer owners, and
operation callers. Change events append an ordinal at the observed revision;
only their event IDs identify them uniquely. Existing audit, business-fact, and
terminal-outcome constraints remain enforced.

Business claims ignore earlier change events so a failed UI topic cannot hold
settlement behind its retry. The same publisher handles both kinds; an individual
failed publication can still consume its bounded five-second attempt. Change
encoding uses stored routing data rather than an actor from an older audit row.

## Delivery and authorization

Communication owns `RealtimeService.WatchChanges`, one authenticated Connect
stream per tab. Its independent `communication-realtime` Kafka group runs even
when Telegram is disabled or unavailable. The consumer publishes small,
recipient-scoped PostgreSQL notifications in Communication's own database.
Every replica holds a dedicated LISTEN connection and forwards matching hints
to its sockets. No service reads another service's database. Kafka offsets
advance after fanout publication; duplicates and partial fanout retries are safe.

Browser messages contain ready, heartbeat, or `CREDITS`, `ORDERS`, and
`OPEN_ORDERS` invalidation scopes. Contents and operation results are fetched
through authorized read APIs. Hints grant no access. Suspended users receive
their own updates but not marketplace hints; administrators receive coarse
order invalidations.

Subscriptions register before ready. Listener loss terminates affected streams.
Per-client pending work is bounded by the three scopes and coalesced; stalled
clients disconnect. Streaming writes have ten-second deadlines, heartbeats run
every 20 seconds, and streams end at token expiry. Immediate role/token
revocation remains centralized access-token lifecycle work, as in CONVENTIONS.md.

## Browser recovery and operations

Mounted views refetch after ready/reconnection, focus, visibility restoration,
and network restoration. A shared 60-second visible-tab fallback repairs missed
hints even while connected. Retry backoff is jittered from 1 to 30 seconds;
45 seconds without a frame triggers reconnection. There is no notification
history, acknowledgement, or resume cursor.

Each mounted read permits one in-flight request and a trailing refresh; bursts
combine over 200 ms. Credit displays share account state. Refreshes preserve
drafts, File objects, filters, scroll, and immutable retry identities. Only
authoritative access loss clears selected private data. Transport errors preserve
current state. Pending operations use owner-scoped read-only status RPCs when a
server-issued ID is known. Missing IDs or failed reads remain uncertain; hints
never automatically repeat a financial mutation.

Deploy migrations/topics, then producers and Communication, then the frontend.
Gateway streaming routes have unlimited request lifetime; application write
deadlines and heartbeat/watchdog behavior bound stalled connections. The existing
Communication health RPC and authenticated stream smoke probe verify exposure.

Publication failures and consumer/listener recovery are logged. Diagnose stale
views using pending outbox count/oldest timestamp, Kafka group lag, listener
connectivity, and browser retries. Outages delay freshness without changing the
result of committed operations. Keep domain outbox retention consistent with
the existing Order publication history; no browser inbox is stored.

Follow-ups: [remaining candidates](../plans/realtime-followups.md).
