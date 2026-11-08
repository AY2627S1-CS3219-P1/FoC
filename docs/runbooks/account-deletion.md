# Account deletion operations runbook

Covers the `active → deleting → physically absent` lifecycle from
`docs/adr/account-deletion.md` (issue 263). Acceptance is irreversible; there
is no administrative reset. This runbook covers observation and incident
handling only.

## Signals

| Signal | Meaning | Action |
| --- | --- | --- |
| Deleting-account age | `users.deletion_requested_at` of accounts still present | Long waits are normal while work is outstanding. Investigate only if the account has no pending job or the job stops retrying. |
| Waiting reasons / `last_checked_at` | `account_deletion_jobs.waiting_reasons` and check timestamp | Stale `last_checked_at` (worker not running or repeatedly failing) needs investigation; fresh reasons mean the account still has outstanding work. |
| Dependency-check failures | Classified `last_error` on the job | Transient Order/Credit/database errors: confirm both services are healthy. The worker retries with bounded backoff; do not intervene per attempt. |
| Leased-job retries | `attempt_count` growth without check progress | A job repeatedly leased but never advancing suggests a crash loop or a poisoned handler; inspect service logs for the job's user UUID. |
| Deletion-outbox age | Unpublished `user_deleted` outbox rows | Broker outage or relay down. Committed deletions are never rolled back; restore publishing and the relay catches up. Verify consumer lag on `users.user_deleted` after recovery. |
| Consumer lag | `users.user_deleted` consumer group lag | Credit replay protection applies only once consumed; prioritize broker/consumer recovery. |

## Worker behaviour

- One leased job at a time per replica (`FOR UPDATE SKIP LOCKED`, 30 s lease,
  1-minute poll, bounded backoff to 5 minutes on dependency failure).
- Busy, unknown, errored or timed-out drain checks postpone the attempt. They
  never clear `is_deleting`, never erase the account and never abandon the job.
- Finalization additionally waits for `admissions_safe_after` (old unrestricted
  access-token lifetime + clock skew + admission bound). An old token admitting
  work inside that window is expected, not an incident.
- Restarting a worker replica is safe; expired leases are reclaimed by another
  replica.

## Incident handling

- **Deleting user reports "cannot start new work"**: expected. The UI shows
  waiting reasons; point them at finishing or withdrawing existing work.
- **Worker appears stalled**: check `last_checked_at` freshness, then Order and
  Credit health, then relay/broker health. Do not reset `is_deleting`; there is
  no reset path.
- **Broker outage**: deletion requests still commit locally. `users.user_deleted`
  publishes with retry once the broker returns; no operator action beyond
  restoring the broker.
- **Unexpected active obligation at consumption time**: an invariant violation.
  Preserve business evidence and existing recovery/settlement behaviour; report
  for repair. Never erase the obligation or reactivate the account.

## Rollback constraints

- Rollbacks must preserve accepted deleting state and the ability to complete
  existing work. Do not roll back to authorization code that admits new work
  for deleting accounts.
- Do not restore `NOT NULL` constraints while retained NULL references exist,
  and never fabricate dummy users to make a downgrade succeed.
- Feature-disable flags may stop new deletion requests but must not stop
  recovery of already accepted ones.
