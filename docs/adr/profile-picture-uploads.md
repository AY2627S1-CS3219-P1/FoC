# ADR: Profile pictures use row-referenced direct-to-storage uploads

Date: 7 Nov 2026
Status: implemented on the profile-picture backend branch; stacks on the review
photo upload work.

## Context

Users need to upload, replace and remove a profile picture. User Service already
has the provider-independent `pkg/images` port and the `user-images` bucket from
the review photo work. Review photos already establish the repository's
row-as-reference model: the row ID is the stable application reference and the
storage key is only an attribute of that row.

## Decision

Profile pictures use the same model. A `profile_pictures` row moves through
`pending` -> `active` -> `deleting`:

- `users.profile_picture_id` points at the active row.
- `object_key` is storage metadata on the row, never the API reference.
- The client sees the row ID as `picture_id`.
- No separate deletion table: a superseded or removed picture is the durable
  `deleting` row that cleanup uses to retry object deletion.

The browser uploads directly to object storage:

1. `CreateProfilePictureUpload` records a pending row and returns a pre-signed
   PUT grant.
2. The browser PUTs the bytes.
3. `UpdateMyProfilePicture` inspects the stored object, requires the observed
   content type and size to match the row, then moves the pending row to
   `active`, the previous active row to `deleting`, and the user pointer to the
   new row in one transaction.
4. `RemoveMyProfilePicture` moves the active row to `deleting` and clears the
   pointer.

There are at most three round trips: grant, PUT and update. There is no separate
verify or confirm RPC.

### Cleanup

- Pending rows older than 24 hours are moved to `deleting`.
- Rows in `deleting` are claimed by cleanup, their objects are deleted, then the
  rows are deleted.
- A failed object delete leaves the row in `deleting`, so the next pass retries.
- A row is therefore the durable reference for its object at every stage; no
  object is orphaned without a row.

The `profile_pictures_one_active` partial unique index keeps at most one active
picture per user.

### Idempotent submit

The object key is deterministic,
`profile/picture/<user-id>/<picture-id>`. If a submit response is lost, a retry
sees the user already pointing at that picture and returns success without
changing state again.

### Validation

Content type and size are signed into the PUT grant, and the same shape is
checked against storage metadata at submit. There is deliberately no byte
decoding or image conversion. Accepted types and the 10,000,000-byte limit match
the repository's other image workflows.

### Read path

Profile reads resolve the active row and mint a short-lived pre-signed GET URL
per response. Responses carrying it are marked `Cache-Control: no-store`. A
missing or unreadable picture degrades to a profile without a URL.

## Alternatives considered

- **Pending-only table plus an S3 key on the user:** the reference becomes
  platform-specific, the active picture has no durable metadata row, and a
  replaced object has nothing to point at for cleanup. Rejected.
- **Pending table plus a separate deletion queue:** works, but duplicates the
  row lifecycle the review photo model already establishes.
- **Copy from a temporary prefix to a stable key at submit:** avoids a table but
  needs a new storage port operation and bucket lifecycle configuration.
- **Fold the picture into `UpdateMyProfile`:** fewer RPCs, but couples every
  profile form save to picture upload state and makes onboarding harder.
- **Review-photo staging and attach with ordinals:** supports ordered
  multi-photo attachment; profile pictures have one slot.

## Operational notes

- The feature reuses the `user-images` bucket, CORS and upload headers added by
  the review photo work. Deployments without `USER_S3_BUCKET` keep profile reads
  but report picture changes as unavailable.
- Replacement and removal delete the old object immediately as an optimization;
  if that fails, the `deleting` row remains and the minute cleanup job retries.
- Onboarding uses the same RPCs and is tracked separately.
