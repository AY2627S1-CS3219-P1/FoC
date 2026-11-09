# Admin-only synchronous account deletion

> Implementation plan for [issue #286](https://github.com/AY2627S1-CS3219-P1/FoC/issues/286). Handwritten implementation is in draft PR #287; new admin RPC/review-query generation and all execution checks are deferred at the user's direction. See [the runbook](../runbooks/admin-account-deletion.md) for exact remaining commands and media/revocation integration. Do not merge or deploy the draft until generation and validation are complete.

## Outcome and superseded work

Replace the self-service deletion workflow in #263 with a small, **admin-only, synchronous hard-delete operation** on the existing Manage users page.

Users request deletion by emailing an administrator; collecting/verifying that request is assumed and out of scope. An administrator finds the account using the existing email lookup, clicks one red Delete account button in the account/role-management card, reviews a confirmation, and submits the request. The RPC returns success only after the live User Service account has been physically removed and its deletion event recorded.

Historical design: #263. Preserve its body and keep `iz/feat/user-deletion` unchanged as a historical implementation for possible future self-service enhancements. This issue replaces that scope; it does not ask to finish or merge that branch.

## Inspected code and reuse boundary

Inspected main at `32d60dd84d6737c5d417c7a58cab49467f199558` and historical branch `iz/feat/user-deletion` at `63f8787acf058b51bb3cc6730e51e44987dfe508` on 9 November 2026.

Use main as the new implementation base. Selectively reuse the historical branch's:
- Order/Credit `GetUserOutstandingWork` contracts, read-only queries and adapters.
- Retained-review nullable references, LEFT JOIN queries and missing-account presentation.
- `users.user_deleted` schema, topic declaration and reliable outbox publication pattern.

Do not import its self-service AuthService deletion RPC, deleting JWT/caller state, irreversible lifecycle flag, admission grace configuration, deletion job table/worker/status UI, broad authorization edits, or unrelated CI/landmine changes.

Relevant current files:
- `proto/user/v1/admin.proto` and `user-service/internal/handlers/admin/handler.go`.
- `user-service/internal/service/roles.go` and `internal/store/{users,admin,sessions}.go`.
- `frontend/src/routes/admin/users/+page.svelte`, `lib/services/admin` and the existing `ConfirmationDialog.svelte`.
- Historical `order-service/database/query/outstanding.sql`, `credit-service/database/query/outstanding.sql` and their internal handlers.
- Historical `user-service/migrations/00013_retained_references.sql` and review changes.
- Photo dependencies #253/#256 and token-revocation dependency #228; re-read their integrated target before implementation.

## Scope boundaries

- Admin-only RPC and UI; no user-facing Delete my account control or email-request system.
- Synchronous prerequisite checks and local hard deletion. No delayed acceptance, polling, background deletion coordinator, prepare/abort protocol or new lifecycle claim.
- Background delivery of an already-committed outbox event is allowed. Synchronous deletion does not require Kafka availability or receipt by every subscriber.
- Keep contributed reviews **and their attached review photos**. Preserve rating/history semantics and render absent participants as Deleted account.
- Delete direct account data, sessions, login challenges and disposable preferences. Delete profile pictures when that feature lands, with durable object cleanup.
- Keep Order/Credit records and opaque historical user IDs. Never cascade-delete Credit accounts, reservations or ledger entries because the User Service account is removed.
- A deleted user's email can sign up again with a new UUID. Do not relink old reviews, orders, credits or photos to the new account.
- Follow the existing User Service GORM architecture. This is an extension of account administration, not a new persistence domain or a migration of User Service to sqlc.

## API and authorization

Extend the existing `user.v1.UserAdminService` with `DeleteUser`, accepting the selected `user_id` and returning an empty success response. Allocate unused proto numbers and generate Go/Connect/TypeScript output normally.

The handler derives actor identity from verified caller context, validates the target UUID, invokes application logic and maps errors. It does not query databases, orchestrate remote checks or construct dependencies.

Enforce authorization at the router/interceptor **and application layer using current database roles**, following ChangeUserRole:
- Ordinary/suspended/unauthenticated callers cannot delete accounts.
- No self-deletion through this admin operation.
- Admins may delete ordinary/suspended users; only super-admins may delete an admin.
- A super-admin/bootstrap account is protected. Return a clear reason rather than nulling bootstrap safeguards.
- Recheck current actor/target permissions in the final local transaction so stale JWT roles or stale UI data cannot authorize deletion.

Add the new method to the existing admin authorization switch. The existing UserAdminService gateway route is sufficient; do not expose internal outstanding-work services publicly.

Suggested layering:
`internal/handlers/admin → internal/service account-administration operation → consumer-owned ports → internal/store GORM adapters / internal Connect adapters`.

Extend existing structs and transaction factories through constructor/composition-root injection. Do not add a generic dependency container or copy the historical sqlc deletion-job subsystem.

## Synchronous algorithm and first-error behaviour

1. Validate and authorize actor/target without mutating state.
2. Check Order, then Credit, **sequentially**, using bounded request-context RPCs. Reuse the branch implementation rather than introducing another protocol.
3. Stop immediately at the first outstanding-work response, invalid response, timeout or dependency error. Do not call the next service or write local deletion state after that failure.
4. Return a safe service-specific reason: e.g. “Account has an active delivery,” “Account has an unresolved offer,” or “Could not verify outstanding work with Order Service; try again.” Do not expose raw database errors, credentials or private internal URLs.
5. After all required checks pass, open one User Service GORM transaction. Re-read/lock actor and target, recheck permissions/protection, collect cleanup metadata, physically remove the live account and append its deletion outbox record.
6. Commit and return success. On any local failure roll back all local writes and return the reason/error category. Never report success for a GORM soft delete.
7. Invoke #228's final access-token revocation integration with session IDs captured before deletion when available. Do not build a second revocation store. Until that PR lands, document the existing access-token-expiry limitation.

Keep all remote checks outside the local DB transaction. Apply a total RPC deadline and per-dependency bounds; no polling/retry loop inside the destructive request.

These checks are a **point-in-time administrative precondition**, not a globally atomic lock on other services. Do not claim they prevent a target's concurrent, already-authorized new request. The product assumes the emailed deletion request is being handled administratively; a strict concurrent-admission guarantee and the broader #263 lifecycle remain future enhancement, not hidden scope.

### Outstanding work definitions

Reuse the historical branch's authoritative, consistent-snapshot queries and stable reason categories. Do not substitute frontend cached lists.

Order includes current nonterminal requester/courier orders, open/accepting offers and relevant pending creation/assignment/cancellation recovery. Historical resolved assignments alone do not block.

Credit's existing endpoint reports active holds/reservations/assignment-related work; retain that conservative prerequisite for this synchronous cut rather than silently ignoring a true result. Positive available balance and immutable history alone do not block.

Independently of this conservative check, **valid autonomous settlement must remain executable after User Service deletion** using retained Credit-owned data. Deletion consumers must not disable historical payouts/refunds or recreate grants. This is separate from starting new user activity.

Timeouts/unavailable checks are unknown and fail the RPC. The old concurrent composite drainer, “unavailable” fallback, worker postponement semantics and dropped unknown reasons must not be copied into this synchronous operation.

## Hard deletion and retained data

Use explicit GORM `Unscoped` deletion for the target and disposable soft-deletable child rows. Migrations own the schema; do not use AutoMigrate. Use new migration numbers after rebasing rather than copying historical numbers over photo migrations.

In one local transaction:
- Preserve retained reviews/audits with nullable `ON DELETE SET NULL` live-user references.
- Delete sessions and auth tokens; also delete email-bound unused registration/login challenges that have no target FK. Coordinate with existing email/session issuance locks.
- Delete favourites/preferences that have no meaning without the account.
- Remove local copied email/display-name payloads from obsolete user-created outbox rows and cancel unpublished creation facts safely. A publication already sent to Kafka cannot be retracted.
- Preserve attached review-photo rows/object keys and their review associations.
- Clear a profile-picture pointer and record durable private-image cleanup before deleting metadata or the user.
- Physically delete the target user and append one stable UserDeleted event fact.
- Optionally keep minimal actor/target IDs and deletion time in the outbox/audit metadata for administrative traceability; do not retain a full copied profile merely as an audit.

Do not delete or rewrite Order/Credit history, balances, reservations or append-only ledger records. Do not reuse or null their opaque historical IDs.

### Reviews and review photos

Change reviewer/reviewee FKs from CASCADE to nullable SET NULL. Handle removal-audit actor/participant nullability so retained reviews can still be moderated later. Use LEFT JOIN and consistent count/list predicates; avoid hiding a review because a participant disappeared.

Keep a surviving recipient's rating/count based on retained, nonremoved reviews. Update domain and generated-query mappings as required without an ORM rewrite.

For #253, `review_photos.owner_id` currently cascades with users. Replace this with a deletion-safe relationship: attached contributions remain, with a nullable owner reference and intact review_id/ordinal/object_key. They are still governed by review visibility. **Do not enqueue attached review photos for deletion or classify them as orphans merely because their author is gone.**

Unattached staging uploads are not published contributions. Preserve their cleanup tracking and expire them using the existing orphan mechanism, without accidentally deleting retained attachments.

For #256, profile pictures are account data: remove them rather than retain them as contributions. Keep a durable deleting row or cleanup intent that survives user deletion and accounts for still-valid upload grants. Object-storage errors after the local commit are cleanup retries, not a rollback of an already-deleted account. If #256 is unmerged, document its precise integration requirement and do not fabricate main-only tables.

Removing name/avatar does not promise that contributed text/photos contain no identifying content. The admin confirmation must accurately state the selected retention policy.

## users.user_deleted event

Continue using the explicit `users.user_deleted` topic with a UserDeleted Protobuf message and user UUID UTF-8 key. Reuse the historical event contract where possible; do not change field meaning or numbers casually. A deletion-request/operation UUID in that existing schema can be a server-generated correlation ID; it does not require a job/state machine.

Persist the event in a **GORM-backed outbox within the same transaction as hard deletion**. The outbox has no users FK; its event ID and deleted_at are stable across retries. Use the existing User Service creation-outbox lease/publish/ack/retry pattern, extended with a typed deletion fact or a small separate GORM adapter.

Publish after commit. Kafka outages leave a committed pending fact and do not turn successful local deletion into an error implying rollback. Consumers deduplicate event IDs and cannot assume creation/deletion ordering across topics.

Provision the topic through `infra/kafka/topics.conf` and document schema/key/retention in `kafka/README.md`. Do not emit is_deleting events or delete retained contributions in event handlers.

Publishing and a compatible consumable contract are in scope. Reuse existing service-owned consumer cleanup only where needed; do not implement a new system-wide lifecycle or duplicate Credit's ledger logic. Document that subscribers must retain valid settlement capabilities and handle delayed creation without granting starting credits again.

## Admin UI

Add **one red Delete account button inside the selected user's existing role/account card**, alongside role management. Reuse ConfirmationDialog and the current AdminApi/Connect adapter/service/composition root.

The confirmation shows the selected account's name/email, clearly states hard deletion and possible re-registration, and explains that reviews/attached review photos remain while account/profile-picture data is removed. Confirmation must be explicit and cancellable.

- No RPC is sent before confirmation.
- Snapshot the target UUID when opening the dialog. A search change or newly selected account invalidates the confirmation.
- Disable repeated delete, role-change and conflicting lookup submissions while deletion is in flight; prevent late search/role responses from restoring a deleted/stale selected user.
- Keep the selected card and show the returned first blocker/error on failure.
- On success clear the target card/form state, show a completion message, and keep the administrator signed in.
- A lost response is uncertain. Recheck the original UUID/account rather than deleting another account found by the same email. Re-registration may already have produced a different UUID.
- Direct API permission checks remain authoritative; hiding a button is not authorization.

## Errors and retries

- Invalid target: InvalidArgument.
- Missing actor/session: Unauthenticated.
- Insufficient current role, self/protected target: PermissionDenied/FailedPrecondition using stable existing conventions.
- Missing target: NotFound.
- Outstanding work: FailedPrecondition with safe service/reason.
- Check timeout/unavailability: DeadlineExceeded/Unavailable with safe service context; stop at that dependency.
- Local persistence failure: generic mapped Internal/Unavailable, log wrapped cause server-side; all local writes rolled back.
- Same UUID requested again after successful deletion: NotFound/already-absent handling; do not generate duplicate deletion facts or act on a newly registered account with the old email.

Do not return success before DB commit and do not return an external publication failure as though the user row were restored.

## Implementation slices and validation

1. Add/reuse internal outstanding-work reads and generated contracts without importing the old lifecycle.
2. Extend the existing GORM account-administration service/store and transaction factory; add retained-reference/outbox migrations.
3. Add admin DeleteUser RPC, current-role protection and typed errors.
4. Add the red button/confirmation and AdminApi adapter/state handling.
5. Wire the user-deleted relay/topic and photo retention/profile-image cleanup integration.
6. Add targeted checks and document the administrative, point-in-time concurrency boundary.

Required meaningful tests:
- Nonadmin/suspended/unauthenticated actor, self-delete, protected bootstrap/super-admin, admin-versus-admin and super-admin-versus-admin permissions.
- First-check outstanding/error stops before Credit and before any local writes; second-check outstanding/error stops before local deletion; bounded timeout and cancellation.
- Current actor/target permissions change before commit; recheck denies deletion.
- Real DB: user/session/challenge removal is physical, transaction rollback on delete/outbox failure, re-register same email as a distinct UUID.
- Retained reviews, recipient stats/counts, missing-author presentation and later review removal audit.
- Retained attached review photos are not orphan-cleaned; private profile-image cleanup survives account deletion and transient storage failures.
- Outbox broker outage, duplicate publication/replay and one stable deletion fact.
- Valid queued settlement using retained Credit records still succeeds without a live User Service profile.
- UI cancel, confirmation target change, duplicate clicks, surfaced dependency reason, success clears only target and leaves admin signed in, lost-response/new-account safety.

Run Buf generation/lint/breaking, relevant GORM/schema and service/RPC integration tests, module fast tests, formatting/lint, and frontend check/tests/build. Use existing integration harnesses and disposable databases. Never hand-edit generated outputs or claim unrun checks passed.

## Acceptance checklist

- [ ] Only authorized administrators can hard-delete an eligible target through the existing account card and admin RPC.
- [ ] Explicit red-button confirmation is required and tied to the selected target UUID.
- [ ] Synchronous neighbouring-service checks stop at the first outstanding result or error and leave the account unchanged on failure.
- [ ] Successful response follows committed physical deletion and outbox insertion.
- [ ] The same email can register anew with a new UUID.
- [ ] Reviews, attached review photos and required Order/Credit history remain meaningful.
- [ ] Account credentials/preferences and profile-picture data are removed or durably queued for cleanup.
- [ ] users.user_deleted is reliably published after commit; asynchronous settlement remains compatible.
- [ ] Implementation extends the existing GORM-backed User Service administration architecture.
- [ ] No self-service lifecycle, irreversible deleting flag, admission grace period, job worker or distributed coordinator is introduced.
- [ ] #263 and iz/feat/user-deletion remain unchanged as historical references.

## Historical references

- Superseded plan: #263.
- [Historical wide-scope branch](https://github.com/AY2627S1-CS3219-P1/FoC/tree/iz/feat/user-deletion), inspected commit [63f8787](https://github.com/AY2627S1-CS3219-P1/FoC/commit/63f8787acf058b51bb3cc6730e51e44987dfe508).
- [Existing admin contract](https://github.com/AY2627S1-CS3219-P1/FoC/blob/32d60dd84d6737c5d417c7a58cab49467f199558/proto/user/v1/admin.proto).
- [Existing Manage users page](https://github.com/AY2627S1-CS3219-P1/FoC/blob/32d60dd84d6737c5d417c7a58cab49467f199558/frontend/src/routes/admin/users/+page.svelte).


