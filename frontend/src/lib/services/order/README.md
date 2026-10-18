# Order frontend integration

OrderUI owns shared services, routes and detail composition. Feature owners deliver
feature-local components and request shared adapter/route integration explicitly.
Exporter/consumer commit handoffs are required; an export does not transfer ownership.

## Exports

- `$lib/services`: `orderApi`, `creditAccountApi`, existing `authService` and
  `locationApi`. Singleton calls belong in browser lifecycle/events, not server
  load/actions/hooks. Protected Order/Credit transports share the auth interceptor.
- `$lib/services/order`: `OrderApi`, `OrderQuery`, `OrderPage`, `CreateOrderInput`,
  `ItemImageGrant`, and the non-singleton `createOrderApi(transport)` factory.
- `orderApi`: `getOrder(id)`, `listOpenOrders(query)`, `listMyOrders(query)`,
  `createOrder(input, idempotencyKey)` and `createItemImageUpload(type, size)`.
  These methods retain generated Order response values and bigint totals/credits.
- `$lib/components/order`: `OrderDetailShell`, `OrderCard`, `LocationPicker`.
- `OrderDetailShell` props: `order: Order`, `refresh: () => Promise<void>` and
  optional `actions: Snippet<[Order, () => Promise<void>]>`. OrderUI composes the
  snippet from feature-local controls in the detail route. This is not a registry
  or generic lifecycle-action framework. A successful mutation must refresh even
  when its response contains an Order, because creation retries can replay an
  earlier result and other participants can change current state.
- `LocationPicker` props: `label`, bindable `value` (Location UUID), `disabled`
  and optional `onchange(location)`. It searches active Supplier Locations and
  keeps disabled-but-selectable warnings. Archiving remains backend-authoritative.

## Routes

- `/orders`: open browsing; query keys `pickup`, `destination` and `page`.
- `/orders/mine`: participation history; query keys `role`, `status` and `page`.
- `/orders/[id]`: shared detail/read shell with active-page refresh.
- `/orders/new`: creation, optional item uploads and submitted recovery.

Destination filtering uses `ListOpenOrdersRequest.delivery_building_name = 5`.
`Order.delivery_building_name = 23` is the explicit approximate destination.
Nonparticipants receive no `delivery` object. The legacy exact destination filter
at field 2 is retained but rejected. Former-courier history is backed by durable
assignment episodes, not inferred from offers or current-courier-only snapshots.
Backend privacy proof is separate from the mocked browser scenario below.
No lifecycle adapter methods or controls are promised without a concrete consumer
request and acknowledged commit handoff.

## Creation safety

Before dispatch, persist the caller-scoped, JSON-safe submitted values and retry
key in tab storage. An uncertain result freezes those values for the same retry.
No file bytes, credentials, signed URLs or unfinished drafts are persisted. A
24-hour-expired or corrupt record blocks automatic replay, rather than silently
starting a duplicate Order. Logout/account change clears submission records.

Images upload directly through signed S3 PUT grants. Grant headers are forwarded,
application cookies/bearer credentials are not. Lost acknowledgments and write-once
repeat responses stay uncertain; explicit CreateOrder verifies object metadata.
Failed selected images require retry/removal; they are never silently omitted.
There is no new upload-status RPC. Existing backend orphan cleanup owns abandoned
uploads. Credit account availability is advisory; Order's reservation decides.

## Verification

```sh
cd frontend
npm run check
npm test
npm run build
cd ..
python3 frontend/tests/order-ui-e2e.py --url http://127.0.0.1:5187 --channel chrome
```

Run a worktree-local dev server on that isolated port first. The browser scenario
uses explicit Connect/S3 fixtures, not real User/Credit/Supplier/Order deployments,
S3 signature validation, Kafka settlement or Telegram consumption. Artifacts are
written to ignored `.local/order-ui-evidence/`. Python Playwright and installed
Chrome (or matching Playwright Chromium without `--channel`) are prerequisites.

The latest coarse-filter browser extension timed out on exact label lookup. Its
selector adjustment has not been rerun. Type checks, 62 unit tests and production
build passed; prior fixture-recovery evidence does not certify that latest script.
