# ADR: Reports and appeals live in Moderation Service

Date: 10 Nov 2026
Status: accepted; supersedes [User reports live in User Service](user-reports-in-user-service.md) (#336)

Reports, appeals and their evidence (RP1–RP12) were first built as domains in
User Service, so that each outcome committed in the same transaction as the
User data it changed. They now run in their own service,
`moderation-service`, with its own database. The backlog assigned them to a
separate service, and moderation has its own scaling, retention and access
model. The report, appeal and evidence rules themselves are unchanged; see
the superseded ADR for them.

## Boundary

- **Moderation Service owns** reports, report responses, appeals, appeal
  decisions, appeal responses, evidence attachments and an outcome outbox, in
  `moderation_dev`. It serves `moderation.report.v1.ReportService`,
  `moderation.appeal.v1.AppealService` and `moderation.v1.HealthService`
  through the gateway.
- **User Service owns** users, reviews, warnings, role changes, review
  removals and restorations. `role_changes.report_id`,
  `account_warnings.report_id`/`appeal_id` and `review_restorations.appeal_id`
  keep Moderation IDs as opaque references, like `order_id`. User Service
  migration `00016` drops the moderation tables and the foreign keys that tied
  User data to them.
- **Order Service** still answers order participants on its internal listener.
  Moderation is now one of its allowed callers.

## Reads from User Service

Moderation reads through `user.v1.UserInternalService`, served only on User's
internal listener and restricted by an Istio policy to Moderation's service
account:

- `GetUsers` names participants and gives their token roles. Names are filled
  at read time. When User Service is down, reports and appeals are still shown,
  without names.
- `GetReviewFacts` gives a review's author, reviewee, order, whether it is
  removed and, if a report removed it, that report. User Service records the
  report on `review_removals.report_id`, which replaces the earlier
  same-timestamp check across tables.
- `GetWarningFacts` gives a warning's owner, order, issuing report and
  whether it is active.

Checks that need these facts fail closed. A report or appeal is not accepted
while User Service cannot answer. A report records its originating order when
it is filed, so closing it needs no lookup. Whether a closed report kept a
review is answered from Moderation's own reports.

## Applying outcomes

Closing a report or appeal records its consequences in `outcome_outbox`, in
the closing transaction. A leased relay publishes each one as
`moderation.outcome_requested` (`kafka/moderation/events/v1`). User Service
applies it through the domain that owns it, as the in-process applier did:

| Event action | User Service applies |
|---|---|
| `warn_user` | an active warning linked to the report |
| `suspend_user` | a suspension through the role rules, linked to the report |
| `remove_review` | a removal, linked to the report when a report made it |
| `remove_warning` | a removal linked to the appeal; a report's warning is found by its report |
| `restore_review` | a restoration linked to the appeal |
| `reinstate_user` | the role the user had before the report's suspension |

- **Ordering.** The Kafka key is the affected user or review, so a report's
  outcome and the appeal that undoes it are consumed in order.
- **Exactly once in effect.** User Service records each event ID in
  `moderation_outcome_events` in the same transaction as the change. A
  redelivered event is a no-op.
- **Rejections.** An outcome that can never apply is recorded as rejected and
  acknowledged, so it cannot stall the subscription. Examples: suspending an
  administrator, or removing a warning that is already removed.

## Consequences

- An outcome is now eventually consistent with the close. The close commits
  first, and User Service applies the change once it consumes the event,
  usually within seconds. While Kafka or User Service is down, closed outcomes
  wait in the outbox or in Kafka.
- A refusal that used to roll back the close can now surface only as a
  rejected event. To keep the common case visible to the administrator,
  closing with a suspension checks the subject's role first and refuses an
  administrator or an already-suspended user. Rarer cases are logged by User
  Service and leave the report closed: for example, a role change between the
  check and the event, or overturning a warning someone already removed.
- Evidence images use their own bucket (`MODERATION_S3_BUCKET`, locally
  `moderation-images`). The orphan cleanup moved with them.
- Moderation Service adds one more deployable: database, migration job,
  service account, Compose service, gateway route and image.

## Deferred

- Credit-adjustment outcomes (#302) and response-request notifications (#303),
  as before.
- Showing administrators when User Service rejected an outcome. Today it is
  only logged.
