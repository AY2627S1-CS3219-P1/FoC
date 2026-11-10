# Moderation evidence: dedicated bucket and workload role

## Decision

Reports and appeals evidence for `moderation-service` lives in its own S3
bucket (`<name>-evidence-*`), created only when `enable_moderation_service` is
true. The bucket blocks all public access, uses AES256 encryption, denies
non-TLS requests, keeps objects on destroy, and allows browser CORS only for
GET/HEAD/PUT from the application origin.

The bucket is reachable only through the `<name>-moderation-evidence` IAM role.
That role is assumed only by `system:serviceaccount:foc:moderation-service` with
the STS audience. It may only call GetObject, PutObject and DeleteObject on
`evidence/*`. It has no list permission and no access to other prefixes, other
buckets, mail, ECR push, or database administration.

## Rationale

Evidence attached to reports and appeals is sensitive, so its access boundary
must not depend on prefix policies inside another service's bucket. A separate
bucket gives the moderation workload its own blast radius, retention and
teardown decision, and lets the IAM policy name one bucket and one prefix
without carve-outs. Moving reports and appeals out of User into
`moderation-service` (issue #336) also moves ownership of their objects. A
dedicated bucket makes that ownership visible in infrastructure instead of
inheriting User's storage.

DeleteObject is granted because the runtime removes orphaned uploads after 24
hours. HeadObject is covered by GetObject. Listing is withheld because cleanup
works from database rows, not by scanning the bucket.

## Alternatives

- A prefix in User's bucket, shared by User and `moderation-service`. Rejected:
  both roles would need to be scoped to exclude each other's prefixes, CORS and
  retention would be shared, and a mistake in one policy would expose the
  other service's objects.
- Keeping evidence under User's role and proxying through user-service.
  Rejected: it keeps a storage dependency on User after the service extraction
  and puts the extracted service's data behind another service's credentials.

## Consequences

- The runtime must read its bucket name and role from the `moderation` Terraform
  output and must write only under `evidence/`. The role is not bound to the
  ServiceAccount until the runtime and release wiring use these settings.
- Existing evidence in User's storage is not migrated by this change, and the
  moderation role gets no access to User's bucket. Any migration is a separate,
  reviewed step.
- Teardown must empty this bucket on purpose, because `force_destroy` is false.
