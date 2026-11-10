# ADR: User reports live in User Service

Date: 9 Nov 2026
Status: accepted

The backlog assigns reports and appeals to a separate Report Service (RP1–RP12).
User Service already reserved `role_changes.report_id` and
`account_warnings.report_id` for it. We are implementing reports (RP1–RP5) as a
domain in User Service (`user-service/internal/report`), exposed as
`user.report.v1.ReportService`.

## Decision

- **Targets.** A report targets **exactly one review or one order**. RP1.3 asks
  for every report to identify its request; we keep review targets
  deliberately. For a review, the subject is the review's author, and the
  originating request is the review's `order_id`. For an order, the reporter
  names the other participant.
- **Lifecycle (RP3).** `open → under_review ⇄ awaiting_response → closed`.
  Administrators begin review and request a response. A response from the
  reported participant moves `awaiting_response` back to `under_review`
  (RP3.4). An administrator may close a report from any unclosed state, with an
  outcome and a reason.
- **Responses (RP2).** Only the reported participant responds, any time before
  the report closes. This remains true while suspended (U6.8.1). Responses are
  append-only text.
- **Visibility (RP4).** The reporter and the reported participant can both
  list and read the report, including its state, outcome, reason and
  responses. Other users get not found.
- **Outcomes (RP5).** Closing a report and applying its outcome happen in one
  database transaction:
  - `warning` creates an `account_warnings` row whose `request_id` is the
    originating order and whose `report_id` is the report.
  - `suspension` goes through `RoleService.ChangeUserRole`, so the existing role
    rules still apply: no acting on administrators, an existing suspension is
    refused, and the audit row carries `report_id`.
  - `review_removal` goes through `review.Service.RemoveReview`, which writes the
    removal audit and recomputes ratings. It is only valid for review targets.

  The close runs in a GORM transaction. Its connection
  (`tx.Statement.ConnPool`) backs both the sqlc report and review queries, so
  the GORM and sqlc domains share one transaction
  (`user-service/internal/reportoutcome`).
- Administrators cannot act on a report they filed or one about themselves.

### Appeals (RP7–RP12)

Appeals are a sibling domain (`user-service/internal/appeal`), exposed as
`user.appeal.v1.AppealService`. They reuse the report lifecycle and the outcome
applier.

- **Decisions.** An appeal challenges one or more final decisions, one row per
  decision in `user_appeal_decisions`. A decision's target names its kind:
  - a closed report's outcome, appealed by the reported participant, or by the
    reporter for a no-action outcome on an order;
  - an active warning that no report issued, such as a courier warning;
  - a review an administrator removed outside a report, appealed by its author;
  - a live review that the reviewee's closed report kept with no action.

  A warning or review removal that a report produced is appealed as that
  report's outcome, so one decision cannot be undone twice. Credit adjustments
  are reserved in the contract and rejected until #302.
- **Idempotency (RP7.7).** The client sends an idempotency key. A retry with
  the same key and payload returns the original appeal, and the same key with a
  different payload is `AlreadyExists`. A decision is in at most one unclosed
  appeal.
- **Lifecycle (RP10).** Same as reports. `RequestResponse` names a counterpart,
  the other participant the decision affects, and only that participant may
  reply while the appeal awaits a response (RP9).
- **Visibility (RP11.3).** The appellant and every counterpart can read the
  appeal and its outcomes. Suspended users may appeal and respond (U6.8.1).
- **Closing (RP11, RP12).** The administrator resolves every decision as
  upheld or overturned, with a reason. Overturned decisions are undone in the
  closing transaction:
  - a warning is marked removed and linked to the appeal;
  - a suspension is lifted through `ChangeUserRole`, back to the role the user
    had before the report's suspension, with the report's `report_id`;
  - a removed review is restored through `review.Service.RestoreReview`, which
    records a `review_restorations` row and recomputes ratings;
  - a retained review is removed through `RemoveReview`;
  - an overturned no-action outcome records the disposition only.

  `review_removals` no longer has a unique `review_id`; each restoration answers
  one removal, so a restored review can be removed again.

Reasons for keeping reports in User Service:

- Every consequence in RP5.2–RP5.4 lands on data User Service owns: warnings,
  roles and reviews. They commit atomically with the close, with no saga or
  outbox.
- A new service would need its own database, deployment and gateway route, and
  would add cross-service calls for every consequence.

## Deferred

- **Participant check (RP1.1, RP1.2)** — #301. Order reports are not yet checked
  against the order's requester and courier. Order's `GetOrderParticipants` is
  declared but not implemented. Draft #287 adds the internal listener and the
  User → Order client configuration this needs.
- **Credit adjustment (RP5.5)** — #302. Credit has no administrative adjustment
  entry type.
- **Response-request notification (RP9.1)** — #303, for reports and appeals.
- **Image evidence (RP1.6, RP2.2).** User Service has no object storage. A
  separate attachments table can be added without changing the report tables.
- **Review-moderation decisions (RP7.4).** Content moderation (#264) does not
  produce appealable decisions yet.

## Consequences

- The reports migration is `00012`, and appeals add `00013` and `00014`. Draft #287 already uses `00010` and
  `00011`, and goose here refuses missing versions, so whichever change merges
  second must renumber.
- `user_reports.review_id` uses the default `NO ACTION` foreign key, like
  `review_removals.review_id`. Hard-deleting a review author therefore fails while
  another user's report still references the review. Users are only soft-deleted
  today; revisit this with permanent deletion.
- Revisit this decision if reports need their own scaling, retention or access
  model.
