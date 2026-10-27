# Credit's reservation listener accepts unauthenticated calls

**Applies when:** Credit's internal listener (`INTERNAL_PORT`, 8081 by default) is reachable from outside the service network. Examples are publishing it in `compose.yaml`, exposing it through a Kubernetes Service or ingress, or merging it into the public listener.

**Symptom:** anyone who can reach the port can call `credit.v1.CreditReservationService` (Reserve, AdjustReservation, RevertAdjustment, ReleaseReservation, GetReservation) without a credential. They can then hold, release or reassign any user's credits. No error is logged, because the call succeeds.

**Cause:** Order sends a workload JWT (`sub=order-service`, `aud=credit-service`), but Credit has no way to verify it yet. The workload identity mechanism is undecided (#29, #157). `credit-service/internal/router.Internal` mounts the reservation RPCs with validation only and ignores the `Authorization` header.

**Current workaround:** keep the internal port unpublished. Compose publishes only Credit's public listener (`CREDIT_PORT`), and Order reaches the internal one at `http://credit-service:8081` on the Compose network. Shared deployments must restrict the port to Order, for example with a network policy.

**Remove this entry when:** Credit verifies the workload credential's signature, issuer, audience, subject and lifetime on every reservation RPC, as #157 requires of the receiver. Tracked under #157 and #29.
