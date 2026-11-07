# ADR: Profile picture uploads use direct-to-storage grants

Date: 7 Nov 2026
Status: implemented on the profile-picture backend branch; stacks on the review
photo upload work.

## Context

Users need to upload, replace and remove a profile picture. User Service already
has `users.profile_picture_key` and the review photo work introduced the
provider-independent `pkg/images` port plus the `user-images` bucket. Profile
pictures are low-stakes UI data, so the review photo staging machine would be
more machinery than the feature needs.

## Decision

The browser uploads directly to object storage and the final profile update is
also the verification step:

1. `CreateProfilePictureUpload` validates the caller and requested shape, records
   one pending upload row and returns a pre-signed PUT grant.
2. The browser PUTs the bytes using that grant.
3. `UpdateMyProfilePicture` inspects the stored object (`HeadObject`), requires
   the observed content type and size to match the pending row, then atomically
   deletes the pending row and sets `users.profile_picture_key`.

There are at most three round trips: grant, PUT and update. There is no separate
verify or confirm RPC. `RemoveMyProfilePicture` clears the key in one call.

### Pending uploads

`profile_picture_uploads` holds only grants, not the current picture. It gives
the submit an ownership anchor and lets cleanup reap abandoned uploads. Rows are
created with a deterministic key,
`profile/picture/<user-id>/<upload-id>`, which also makes a lost-response retry
idempotent: if the row is gone but the user's current key already matches the
deterministic key, the submit succeeds.

The `deleting` boolean is a cleanup claim, not a state machine. Cleanup claims
expired rows, deletes their objects and then deletes the rows. A failed object
delete leaves the claim in place for the next pass.

### Validation

Content type and size are signed into the PUT grant, and the same shape is
checked against storage metadata at submit. There is deliberately no byte
decoding or image conversion. Accepted types and the 10,000,000-byte limit match
the repository's other image workflows.

### Read path

Profile reads mint a short-lived pre-signed GET URL per response. Responses
carrying it are marked `Cache-Control: no-store`. If storage is unavailable the
profile is returned without a URL rather than failing the whole read.

## Alternatives considered

- **No pending table, key derived only from the caller and upload ID:** simpler,
  but abandoned uploads have no durable record for cleanup.
- **Copy from a temporary prefix to a stable key at submit:** avoids the table,
  but needs a new storage port operation and bucket lifecycle configuration.
- **Fold the picture into `UpdateMyProfile`:** fewer RPCs, but couples every
  profile form save to picture upload state and makes onboarding harder.
- **Review-photo staging and attach machine:** supports ordered multi-photo
  attachment; profile pictures have one slot, so this is unnecessary.

## Operational notes

- The feature reuses the `user-images` bucket, CORS and upload headers added by
  the review photo work. Deployments without `USER_S3_BUCKET` keep the profile
  read/write paths but report picture changes as unavailable.
- Replacing or removing a picture deletes the previous object after the database
  commit best-effort. A failed delete can leave an unreferenced object; pending
  uploads are still reaped by the minute cleanup job.
- Onboarding uses the same RPCs and is tracked separately.
