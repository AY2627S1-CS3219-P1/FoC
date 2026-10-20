# ADR: Order read privacy and durable assignment history

Date: 18 Oct 2026
Status: accepted policy

Order history must survive courier withdrawal or administrative unassignment
(R1.8), but history membership must not grant the current courier's operational
permissions or sensitive data. Persist assignment history separately from the
current assignment, and enforce caller-specific delivery projections in the
backend rather than relying on frontend hiding (NFR-06.2).

## Read boundaries

Authenticated, non-suspended couriers may browse eligible Awaiting Courier
Orders. Browsing exposes an explicit approximate destination building/area, not
the exact destination Location ID, Location name, floor or delivery instructions.
Pickup snapshots remain visible to browsing couriers, consistent with the
contract's exact `pickup_location_id` filter. The restricted location is the
delivery destination. Destination filtering uses the same coarse granularity so
filter membership and counts cannot reveal a hidden exact destination. Missing area data must not fall
back to an exact Location name. Stored pickup and delivery snapshots remain intact.

The requester and current assigned courier retain exact delivery access. A former
courier who withdrew or was unassigned before pickup retains redacted Order
history and detail access, including while suspended, but gains no access to later
proof, exact destination or instructions. An offer alone is not an assignment and
does not establish participation. Requester/current-courier history and detail
reads also survive suspension, without widening mutation permissions.

Admin role alone does not authorize sensitive delivery data. That access requires
authoritative assignment to an associated open report or appeal. Until the
external authorization dependency is available, admin-only sensitive access fails
closed. Apply the projection consistently to read and mutation responses; hiding
fields in a list or page alone is insufficient. Proof authorization remains a
separate NFR-06.3 concern, and this decision does not implement Reports or Appeals.

## Assignment history

Record assignment and removal durably in the same local transaction as the
corresponding Order change. Retain courier identity, assignment identity/time and
removal time/reason across reassignment. My Orders, role filtering, counts and
single-Order visibility must agree about historical participation. Never infer
assignment from an offer or widen `Order.HasParticipant` to authorize historical
couriers for lifecycle actions.

The selected approach keeps the existing Order and its location snapshots rather
than creating a duplicate full-Order snapshot per courier. Migration 00004 backfills
from durable assignment/removal facts and the current courier snapshot. Missing
historical assignment times/revisions remain unknown. The central Postgres Apply
hook maintains future episodes atomically, without feature-specific history SQL.
Do not fabricate past assignments when evidence is absent.

## Ownership and consequences

OrderUI owns safe read/history design, shared frontend services, routes and detail
composition. Feature owners supply feature-local controls and integrate through
acknowledged contracts and exporter/consumer commit handoffs. Acceptance,
withdrawal and administrative unassignment owners consume the agreed durable
assignment write interface. OrderAdmin owns the case-assignment authorization
dependency. Publishing frontend exports does not transfer shared-file ownership.

This supersedes the browsing/admin visibility and current-courier-only history
assumptions in the Reads section of [the aggregate foundation ADR](order-aggregate-foundation.md).
It requires coordinated API, persistence and writer changes before complete
history/privacy compliance can be claimed. It does not introduce lifecycle
controls, a generic action framework, a review system or a Reports/Appeals service.
