# Admin-only synchronous account deletion

Status: accepted for implementation in #286; generation and verification are
pending in draft PR #287.

## Decision

An administrator handles an emailed deletion request using the existing
account-management card and an explicit confirmation. UserAdminService.DeleteUser
checks Order and then Credit sequentially. The first outstanding result or
unknown/error aborts the request. Checks run outside the User Service transaction.

After both checks pass, User Service rechecks current administrator/target roles
under row locks and hard-deletes the eligible account through its existing GORM
store. Sessions, credentials and disposable preferences are removed physically.
A users.user_deleted outbox fact commits in that same transaction. Delivery to
Kafka is asynchronous and retryable; successful deletion does not depend on the
broker being reachable. Only the event relay runs in the background.

Reviews and administrative history survive with nullable live-user references.
Retained review queries use LEFT JOINs and expose missing-participant flags and
empty IDs/names, which clients render as **Deleted account**. Removing a review
later does not attempt to recalculate a deleted recipient's profile. A surviving
recipient's count and rating still include retained, nonremoved reviews.

The same email may register as a new UUID. Old Order/Credit identifiers and
financial records are retained and never rebound to the new account. A valid
queued settlement continues using Credit-owned state.

## Deliberate concurrency boundary

These are point-in-time administrative eligibility checks. There is no
cross-service transaction preventing an already-authorized concurrent action
from starting after a successful check. Deletion does not add lifecycle flags,
admission fencing, prepare/abort RPCs, a grace horizon or a deletion job worker.
Strict admission coordination remains a future enhancement in the historical
#263 design, not an implied guarantee of this feature.

Immediate access-token invalidation is deferred to #228. User Service rejects
subsequent protected calls/refreshes when the live user is absent; other services
may still accept an unexpired access token until the centralized revocation
integration lands. Deleting refresh sessions does not revoke signed access JWTs.

## Media integration

At the implementation baseline, #253 (review uploads) and #256 (profile uploads)
are unmerged. No absent tables, storage clients or upload jobs are introduced
here. Contributed review photos must survive when those changes are integrated;
profile pictures must be durably scheduled for deletion. The integration steps
are in the runbook. A legacy nonempty users.profile_picture_key currently fails
deletion closed rather than discarding its only cleanup reference.

## Historical work

#263 is closed with its body preserved. Keep iz/feat/user-deletion at
63f8787acf058b51bb3cc6730e51e44987dfe508 untouched as a reference for future
self-service enhancements. This implementation starts from main and selectively
reuses its read-only outstanding-work contracts/queries, retained-reference
mapping and UserDeleted schema. It does not merge the broader lifecycle.
