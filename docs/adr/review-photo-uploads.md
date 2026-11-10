# Review photos: direct-to-storage uploads with the light validation pattern

## Decision

Review photos upload directly from the browser to object storage through
pre-signed PUT grants; the backend signs, validates metadata and attaches, but
never proxies image bytes. The feature follows the order item image pattern
(`docs/adr/order-creation-images-relay.md`), not the delivery proof pattern
(`docs/adr/delivery-proof-decoding.md`): there is no byte-level decoding, no
per-upload verification RPC and no signing-epoch fencing. Staged photos attach
inside the review submission transaction, where each photo's stored object
metadata is compared against the grant-signed shape recorded on its row.

## Rationale

Review photos are decorative evidence of a transaction, not anti-fraud
evidence; nobody profits from substituting a corrupt image the way a courier
might profit from faking a delivery proof. The order item image precedent
already established that metadata-only validation is proportionate for this
threat model: the pre-signed PUT bakes the exact content type and size into
the signature, the object is immutable (`If-None-Match: *`), and attachment
compares storage's observed metadata against the row — the browser's claims
are never trusted, and the backend never decodes pixels it has no reason to
distrust.

The review submission transaction is the consistency boundary. Photos are
staged (state `uploading`, owned by the uploader, `review_id` NULL) because a
photo row exists before its review does; ownership is the authorization anchor
for the pre-review window. Attachment guards on ownership, staging state and
the inspected shape, so a photo claimed by cleanup, replaced or staged by
another user fails the guarded update and rolls the submission back. The
`deleting` state makes orphan cleanup a crash-safe, retryable two-phase delete
and fences concurrent attachment.

## Idempotency

No idempotency-key machinery is added. Order creation needs its
`order_idempotency_keys` table because nothing in the schema prevents two
orders with identical content; reviews are the opposite case —
`UNIQUE (reviewer_id, order_id)` and `UNIQUE (reviewee_id, order_id)` make a
duplicate review impossible at the schema level. A retry after a lost response
therefore fails with `409 AlreadyExists` instead of double-executing, and the
frontend recovers by treating that outcome as success and fetching the
existing review — recovery logic the client needs anyway for ambiguous
timeouts. Duplicate upload grants create orphan staging rows, which the
24-hour cleanup bounds. When the review sub-domain one day needs a genuinely
keyless-but-unsafe mutation (no natural uniqueness), that is the moment to
port the creation-key machinery, not before.

## Alternatives

- Byte-level decoding with ImageMagick (the delivery proof pattern) would
  detect polyglot or mislabeled files, at the cost of a pinned native codec in
  the user-service image, decoder isolation requirements and a second
  verification pass. Rejected: nothing in the review workflow benefits from
  rejecting a non-decoding image.
- Proxying uploads through the backend would allow stream-rejecting bad bytes
  before storage, but doubles public-internet byte crossings, scales backend
  resources with upload traffic and reintroduces long-lived request handling
  the presigned design deliberately avoids.
- Reusing the order-service `internal/images` package directly is impossible
  (Go `internal/` visibility) and migrating order-service onto a shared
  package in the same change was deferred to keep this change contained. The
  port is copied to `pkg/images` verbatim; the duplication is temporary and
  the follow-up is migrating order-service onto the shared package, then
  deleting its internal copy.
- Embedding permanent object URLs in review reads was rejected in favor of
  per-response pre-signed GET grants (~5 minutes), matching the order read
  paths; responses carrying them are inherently non-cacheable.

## Operational requirements

- The `user-images` bucket is provisioned outside the service in production;
  local development uses the `user-storage-init` job against FloCI.
- Cleanup runs on a minute ticker in the composition root; claim semantics
  (`FOR UPDATE SKIP LOCKED`) make multiple replicas safe.
- Without `USER_S3_BUCKET` configured, photo RPCs report unavailability and
  reviews submit without photos; the review feature itself does not depend on
  storage.
