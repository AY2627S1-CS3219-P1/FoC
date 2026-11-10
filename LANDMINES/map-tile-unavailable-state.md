# Map tile failures have no explicit unavailable state

**Applies when:** Testing optional map outages or interpreting a rendered map
container as evidence that the map dependency is healthy.

At `ae4ee3cf`, `CampusMap.svelte` renders Leaflet controls and attribution even
when every tile connection fails. It does not expose a visible or accessible
unavailable message. A local Playwright test aborted 18 real tile requests,
successfully created and cancelled a request through real RPCs, then observed
six successful tile responses after restoration. The unavailable-state
requirement still failed; enabled controls and a map container do not prove it.

Keep the missing state visible as a failure in resilience reports. Test core
controls and recovery independently so an absent warning does not hide their
results. Tracked in [issue #322](https://github.com/AY2627S1-CS3219-P1/FoC/issues/322);
see the [local run report](../docs/testing/aws-performance-local.md).

Remove this entry when browser tests demonstrate an explicit accessible state
for connection failures, server errors and timeouts, usable core workflows
during each outage, and correct recovery after the dependency is restored.
