# Order participant lookup is declared but not mounted

**Applies when:** User Service submits reviews through `ConnectOrderGateway`, but Order Service has not implemented and mounted `/order.v1.OrderInternalService/GetOrderParticipants` on its private listener.

**Symptom:** an otherwise valid review reaches Order, gets HTTP 404, and fails before any review write. The gateway maps every non-NotFound RPC failure to `errOrderUnavailable`, so it fails closed. `TestInternalParticipantServiceIsNotMounted` reproduces the missing route.

**Cause:** the Order protobuf contract and User-side client exist, but Order's RPC composition currently mounts only Health, Order, and Admin. Order also binds the same handler to public and internal listeners until those routes are split, so the participant lookup must be mounted on the internal listener only.

**Current workaround:** keep the User Service eligibility check fail closed. Valid review submissions remain unavailable until the Order method is implemented. Do not bypass the lookup or mount the method on the public router. Implementation is tracked in [#301](https://github.com/AY2627S1-CS3219-P1/FoC/issues/301), with listener separation tracked in [#287](https://github.com/AY2627S1-CS3219-P1/FoC/pull/287).

**Remove this entry when:** Order reads the persisted requester, assigned courier, and current status for the requested order, mounts that method only on its private listener, and tests prove the public listener returns 404 while the private listener serves the participant response.
