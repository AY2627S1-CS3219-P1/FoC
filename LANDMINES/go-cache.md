# Recreated services rebuild Go dependencies

**Applies when:** Compose replaces a Go service container, including after `docker compose down` or `docker compose up --force-recreate`.

**Symptom:** Air recompiles dependencies on its first build after a container is recreated. Startup can take several minutes.

**Cause:** Compose shares a persistent `go-modules` volume for downloaded modules, but each container stores its compiled Go build cache in its writable layer. Removing the container removes that build cache.

**Current workaround:** Keep the service containers running during normal development. Let Air rebuild the application without recreating the containers. Compiled package caching across recreation is intentionally not configured.

**Tracking issue:** [#12 Persist Go caches across Compose container recreation](https://github.com/AY2627S1-CS3219-P1/FoC/issues/12)

**Remove this entry when:** Recreated service containers no longer rebuild dependencies on their first build, or this behavior is no longer a concern.
