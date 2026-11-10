# ADR: Admin analytics read from owning services

Date: 4 Nov 2026
Status: accepted

The admin dashboard (A1.1–A1.13) shows counts by current state, user counts and
credit totals, and lets an administrator open a single record. Each figure is
served by the service that owns the data, through an administrator-only Connect
RPC that aggregates its own PostgreSQL tables on request:

- Order: `OrderAdminService.GetOrderStats` counts requests by status (A1.1).
  Single requests use `OrderService.GetOrder` and keep the admin redaction
  (A1.9).
- User: `UserAdminService.GetUserStats` counts active and suspended users, and
  `UserAdminService.GetUser` returns one profile (A1.4, A1.5, A1.12). Active
  means not deleted and not suspended, so administrators are active users.
- Credit: `CreditAdminService.GetCreditTotals` returns credits currently
  reserved, and credits returned to requesters and paid to couriers over the
  lifetime of all reservations (A1.6–A1.8). It is mounted on the authenticated
  public listener, never the internal listener.

Daily series follow the same pattern. `OrderAdminService.GetDailyOrderCounts`
counts orders created per day, and `CreditAdminService.GetDailyTransactionVolume`
sums credits paid to couriers and counts those payments per day. Both take an
inclusive date range of at most 366 days, bucket days from midnight to midnight
in Asia/Singapore, and return every day in the range, including empty ones. Each
service indexes the timestamp it scans, so a range query reads only the rows in
that range.

Reports, appeals and administrative credit adjustments (A1.2, A1.3, A1.10,
A1.11, A1.13) have no owning implementation yet. Their dashboard panels are
deferred until the Report Service and the adjustment ledger kind exist; each
will add its own administrator RPC following this pattern.

## Alternatives considered

**ScyllaDB read model.** A separate analytics service would consume Kafka into
Scylla. The dashboard's queries are a few `GROUP BY` and `SUM` aggregates over
small tables that PostgreSQL answers on page load, so there is no write
throughput or partitioning problem for Scylla to solve. A read model would also
need events that do not exist (Credit and user role changes publish none),
a backfill, and per-event deduplication, because Scylla counters are not
idempotent under the at-least-once delivery of `pkg/kafka`. The counts would
become eventually consistent, and another cluster would need operating.

**RocksDB.** RocksDB is an embedded, single-process key-value store. Its state
would be local to one replica and rebuilt from Kafka after any loss, which
amounts to hand-writing a stream processor. It has the same event gaps as
Scylla.

Neither store helps the single-record views: they must come from the owning
service so privacy rules such as fail-closed admin delivery details
(`docs/plans/delivery-proof.md` D6) stay in one place.

The daily series did not change this choice: a bounded, indexed range grouped
by day is still a small query for PostgreSQL at this scale. Revisit it if
trend queries need ranges or dimensions that indexes cannot keep cheap, or if
the aggregates measurably load the transactional databases. Prefer a PostgreSQL read replica
or materialized view before introducing a new datastore.
