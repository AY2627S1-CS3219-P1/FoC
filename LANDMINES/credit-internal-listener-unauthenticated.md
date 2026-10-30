# Credit's reservation listener accepts unauthenticated calls

**Applies when:** Credit's internal listener (`INTERNAL_PORT`, set to 8081 by Compose and Kubernetes) is reachable by an unauthorized caller. Examples are publishing it in `compose.yaml`, exposing it through ingress, omitting the Kubernetes principal policy, or merging it into the public listener.

**Symptom:** anyone who can reach the port can call `credit.v1.CreditReservationService` (Reserve, AdjustReservation, RevertAdjustment, ReleaseReservation, GetReservation) without a credential. They can then hold, release or reassign any user's credits. No error is logged, because the call succeeds.

**Cause:** `credit-service/internal/router.Internal` mounts the reservation RPCs with validation only and ignores the `Authorization` header. Application-level workload JWT verification remains tracked under #29 and #157.

**Current workaround:** Credit fails configuration unless `INTERNAL_PORT` is set. Keep that port unpublished. Compose publishes only Credit's public listener (`CREDIT_PORT`), and Order reaches the internal one at `http://credit-service:8081` on the Compose network. Kubernetes keeps the internal Service inside the cluster and applies `credit-internal-order-only`: Istio mTLS must identify `cluster.local/ns/foc/sa/order-service` for port 8081. `make k8s-smoke` checks the public HTTPS ingress route only; it does not exercise the internal-port authorization policy.

**Remove this entry when:** Credit verifies the workload credential's signature, issuer, audience, subject and lifetime on every reservation RPC, as #157 requires of the receiver. Tracked under #157 and #29.
