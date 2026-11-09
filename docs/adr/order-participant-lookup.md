# ADR: Private order participant lookup for reviews

Date: 10 Nov 2026
Status: accepted

Order owns the persisted requester, assigned courier, and lifecycle status.
User Service must read those values before accepting a review; user existence
and a caller-supplied order UUID do not establish an order relationship.

`OrderInternalService.GetOrderParticipants` reads one order row through the
existing PostgreSQL repository, without public projections or assignment-history
membership. It returns only order ID, requester ID, optional current courier ID,
and status. Missing orders return NotFound. Invalid and zero UUIDs are rejected.
The existing contract and generated code are unchanged.

The RPC is mounted exclusively on Order's internal router and listener. Public
Order, Admin, and Health RPCs remain on the public listener. Compose publishes
only the public port. Kubernetes uses strict mTLS and a DENY policy restricting
port 8081 to User and Communication Service principals; no gateway route exposes
this RPC. Local Compose relies on its private network, consistent with the
existing service-to-service trust boundary.

User Service derives the reviewer from authentication and requires the reviewer
and reviewee to be exactly the requester/current courier pair, in either
direction, on a Completed order. It validates the response order ID and UUIDs,
and rejects unavailable or malformed lookup responses before writing reviews,
attaching photos, or recalculating ratings. Completed orders retain their pair
and status, so eligibility cannot be invalidated by a later reassignment.

Tests cover persisted unassigned, reassigned, and completed orders through the
private Connect handler; missing orders, invalid IDs, and database failures;
public/private route isolation; and authenticated review submission through the
Connect client for both directions and rejected participant/availability cases.
The former missing-RPC landmine is removed. Issue #301 remains open for its
separate report authorization work; this change supplies its Order RPC dependency.
