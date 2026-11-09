# Admin account deletion

Implementation: #286 / draft PR #287. The draft is not ready to deploy until the
deferred generation and verification below are complete.

## Operation

1. Verify the deletion request received by email outside the app.
2. Open Manage users and find the exact account by email. Confirm its identity.
3. Click the red Delete account button and confirm the selected account.
4. If Order or Credit reports work or cannot answer, resolve that reason and
   request deletion again. The failed request has not enrolled a deletion job.
5. On success the live account is physically absent and one pending deletion
   event is durable. The administrator remains signed in.

Never retry by substituting a new account with the same email. A re-registration
has a different UUID. A network/lost-response error is uncertain: verify the
original account and history before submitting another confirmed request.

## Runtime wiring

User Service needs ORDER_SERVICE_BASE_URL and CREDIT_SERVICE_BASE_URL pointing
to private listeners. Missing settings fail deletion closed, while ordinary
User Service features remain available. Compose defaults to Order/Credit :8081;
Kubernetes uses the internal Services on service port 8080 -> container 8081.
Each check is bounded to five seconds, with a fifteen-second operation deadline.

The private RPCs never receive a Gateway route. Kubernetes requires User's mTLS
workload identity for the checks. Credit reservation methods remain Order-only;
User does not gain reservation privileges. Bare Compose relies on its private
network while application workload authentication remains tracked by #29/#157.

Provision users.user_deleted from infra/kafka/topics.conf. Monitor pending
user_deleted_outbox rows/age and relay errors. A publish failure is retried under
a lease; event IDs and deletion timestamps do not change. Do not tell an admin
that a broker outage restored an already-removed account.

## Pending feature integrations

Inspected open dependencies on 9 November 2026:

| Dependency | Required integration |
| --- | --- |
| #228, token revocation | Capture sessions before hard deletion and reuse its final revocation API. Do not add another revocation store. Access JWTs currently expire naturally in other services. |
| #253, review photos | Make review_photos.owner_id nullable with SET NULL; keep attached review_id, object_key and ordinal. Owner absence alone must never make an attached photo an orphan. Preserve tracking/expiry of unattached staging uploads. |
| #256, profile pictures | Replace its user-owned CASCADE cleanup with a durable deleting row/cleanup intent surviving the user. Clear profile_picture_id and enqueue private keys before removal. Respect pending PUT grants and retry storage deletion. Replace the legacy-key fail-closed guard only once cleanup is wired. |

The migrations here are numbered after main's current 00009 (User), 00008
(Order) and 00005 (Credit). Rebase and renumber new migrations when dependencies
merge. Never overwrite an already-applied migration or import the historical
lifecycle/job migrations. Missing-user rendering in future review/photo UIs
must use the deletion flags and empty IDs to show Deleted account and suppress
profile links.

## Deferred generation and verification

At the user's instruction, no Go/Buf toolchain installation, generation or tests
were attempted for this implementation. Generated internal Order/Credit RPCs,
their outstanding-work queries, the retained-review protobuf and the deletion
event are reused byte-for-byte from the historical commit where compatible.
No generated file is hand-edited.

The new admin RPC and changed nullable review queries still require generation.
The draft currently will not build/type-check until those outputs are refreshed.

```sh
# Install frontend dependencies using the repository's normal workflow first.
make buf-lint
make buf-generate
# From user-service/, order-service/, and credit-service/, respectively:
make sqlc
# From the repository root:
make fmt
make fmt-check
make lint
```

Check Buf compatibility against the actual PR base. Run affected module fast
tests with GOWORK=off go test -mod=readonly ./..., disposable PostgreSQL tests
using TEST_DATABASE_URL, and frontend check/tests/build. Exercise the updated
mesh smoke checks: User can read outstanding work; default/gateway/foreign/Order
identities cannot; User cannot call Credit reservation methods. Add integrated
photo retention/private cleanup verification when the dependent features land.

Do not restore historical NOT NULL/CASCADE constraints after retained NULL
references exist. The down migration rejects that downgrade instead of creating
dummy users or losing retained contributions.
