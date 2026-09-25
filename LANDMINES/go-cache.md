# Recreated services download Go dependencies again

**Applies when:** Compose replaces a Go service container, including after `docker compose down` or `docker compose up --force-recreate`.

**Symptom:** Air prints many `go: downloading` lines before its first build. Startup can take several minutes.

**Cause:** Each container stores its Go module and build caches in its writable layer. Removing the container removes those caches.

**Current workaround:** Keep the service containers running during normal development. Let Air rebuild the application without recreating the containers.

**Tracking issue:** [#12 Persist Go caches across Compose container recreation](https://github.com/AY2627S1-CS3219-P1/FoC/issues/12)

**Remove this entry when:** Recreated service containers reuse persistent Go module and build caches.
