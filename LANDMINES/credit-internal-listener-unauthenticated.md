# Order's Credit token is not verified by Credit

**Applies when:** configuring Order to call Credit's reservation RPCs, or exposing Credit's internal listener (`INTERNAL_PORT`, 8081 by default) beyond the service network.

**Symptom:** Order requires `CREDIT_SERVICE_BASE_URL` and `CREDIT_WORKLOAD_TOKEN_FILE` together. It sends the file's JWT with subject `order-service` and audience `credit-service`, checking those claims and expiry without verifying the signature. Credit's internal listener ignores the Authorization header, so it does not authenticate Order. Compose leaves both Order settings empty by default.

**Risk:** any caller that can reach the internal listener can invoke Reserve, AdjustReservation, RevertAdjustment, ReleaseReservation and GetReservation without authentication.

**Workaround:** keep the internal listener private. Do not treat Order's sender-side JWT checks or Istio namespace mTLS as Order-only authorization.

**Tracking:** [#214 Order-to-Credit authentication](https://github.com/AY2627S1-CS3219-P1/FoC/issues/214), under [#29 workload identity](https://github.com/AY2627S1-CS3219-P1/FoC/issues/29) and [#157 inter-service auth policy](https://github.com/AY2627S1-CS3219-P1/FoC/issues/157). Kubernetes/Istio groundwork is in [#167](https://github.com/AY2627S1-CS3219-P1/FoC/pull/167); its namespace-wide mTLS does not restrict calls to Order.

**Remove this entry when:** Order's Credit calls work with the chosen service identity, and Credit verifies that identity and authorizes Order on every reservation RPC.
