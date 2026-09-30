# Location administration

The existing `supplier.location.v1.LocationAdminService` provides all four
administrative mutations. Discovery remains in `LocationDiscoveryService`.
Each mutation returns `{ location: Location }`, including the current revision,
Building, Categories, coordinates, optional fields, and lifecycle timestamps.

## Clients and authorization

The contract is in `proto/supplier/location/v1/admin.proto`. Generated Go
clients are in `pkg/gen/supplier/location/v1/locationv1connect`; TypeScript
descriptors are in `frontend/src/lib/gen/supplier/location/v1/admin_pb.ts`.

Use the supplier service base URL and the existing AuthService interceptor.
Compose this client in the frontend service composition root, following the
existing service conventions:

```ts
import { createClient } from '@connectrpc/connect';
import { createCustomTransport } from '$lib/connect/transport';
import { LocationAdminService } from '$lib/gen/supplier/location/v1/admin_pb';

const locationAdmin = createClient(LocationAdminService, createCustomTransport({
  baseUrl: supplierServiceBaseUrl,
  interceptors: [authService.interceptor],
}));
```

Only authenticated `admin` and `super_admin` callers may invoke these methods.
`user` and `suspended_user` callers receive `permission_denied`; missing or
invalid credentials receive `unauthenticated`. Authorization precedes input
validation. Hiding controls in the UI does not replace server authorization.

## Requests

| Method | Request | Behavior |
| --- | --- | --- |
| `createLocation` | `idempotencyKey`, complete `location` input | Creates a UUID identity with revision 1. Duplicate names are allowed. |
| `updateLocation` | `id`, `expectedRevision`, `updateMask`, partial `location` input | Applies selected fields atomically and increments the revision. |
| `archiveLocation` | `id` | Sets the archive timestamp and increments the revision on transition. |
| `unarchiveLocation` | `id` | Clears the archive timestamp and increments the revision on transition. |

Create requires an explicit `isSupplier` value, a name, an existing Building UUID,
and coordinates. A Supplier requires at least one existing Category UUID; an
ordinary Location must have no Categories. Duplicate Category IDs are invalid.

| Field | Rule |
| --- | --- |
| `name` | 1–200 Unicode characters, excluding leading and trailing whitespace |
| `floor` | At most 50 characters, excluding leading and trailing whitespace; blank clears it |
| `contact` | At most 500 characters, excluding leading and trailing whitespace; blank clears it |
| `details` | At most 2,000 characters, excluding leading and trailing whitespace; blank clears it |
| `coordinates` | Required, finite latitude −90…90 and longitude −180…180 |
| `opensAt`, `closesAt` | Both absent or both present and unequal; whole minutes from 00:00 through 23:59 |

Text is stored without leading or trailing whitespace. Times represent
Asia/Singapore wall-clock time, with no date or UTC conversion. Closing before
opening represents an overnight interval.

### Create retries

Generate one UUID idempotency key per intended create and retain it for retries.
The key is scoped to the authenticated caller and create method for 24 hours.
Repeating it with equivalent normalized input returns the same Location ID and
its **current state**, including subsequent edits or archive state. Category
order, UUID spelling, and trimmed text do not change the normalized payload.
A different payload returns `already_exists`. After expiry, the key can create
a new Location. The resource write and retry record commit together.

### Updates and clearing fields

Use the last returned `revision` as `expectedRevision` (a `bigint` in TypeScript).
The mask must be nonempty and contain unique supported **protobuf field names**:

```text
name, is_supplier, category_ids, building_id, floor, coordinates,
opens_at, closes_at, contact, details
```

Fields outside the mask are ignored, even if their supplied values are invalid.
Selected fields left unset are cleared; clearing a required field fails
validation. Select `opens_at` and `closes_at` together to change or clear hours.
Classification and Categories are checked against the merged Location, so
switching a Supplier to an ordinary Location also requires clearing Categories.

For example, clear contact and change the floor while preserving other fields:

```ts
const response = await locationAdmin.updateLocation({
  id: currentLocation.id,
  expectedRevision: currentLocation.revision,
  updateMask: { paths: ['floor', 'contact'] },
  location: { floor: '2' },
});
// Use response.location as the next editable state, including its revision.
```

On `aborted`, reload the Location and reconcile edits before retrying with its
new revision. Do not automatically replace the revision and overwrite changes.

## Archive lifecycle

Archive preserves the Location row, UUID, and historical references. Normal
discovery excludes archived Locations; direct lookup returns them with
`archivedAt`. Administrators may edit and restore them. Repeating archive on an
archived Location, or unarchive on an active Location, preserves its revision
and timestamps.

Disablement and addition-request mutation APIs are mounted through
`router.MountLocationServices`. Creating a Disablement rejects an archived
Location with `failed_precondition`, checking its archive state in the same
transaction as the new write. Addition requests propose new Locations rather
than select existing ones. Existing historical references remain valid. A
disablement itself does not make a Location unselectable.

## Stable errors

| Connect code | Meaning |
| --- | --- |
| `invalid_argument` | Invalid fields, UUIDs, opening hours, mask, or nonpositive expected revision |
| `unauthenticated` | Missing or invalid authentication |
| `permission_denied` | Caller is not an administrator |
| `not_found` | Location does not exist |
| `failed_precondition` | Supplier/Category invariant fails, or referenced Building/Category is missing |
| `already_exists` | Create key reused with a different normalized payload |
| `aborted` | Expected revision is stale; a database concurrency failure can also require retry |
| `internal` | Unexpected server failure; private database details are hidden |

Proto validation enforces input shape, clocks, and text lengths. The domain
also validates merged updates and enforces the Supplier/Category invariant so
that it returns `failed_precondition`. Future consumers of the shared
`LocationInput` must apply the same domain rules.

The frontend work for #50 can use these generated methods directly. This backend
change does not add pages, forms, components, or browser tests.
