# PostGIS does not start on Apple Silicon

**Applies when:** `uname -m` prints `arm64` and Compose or Go integration tests use `postgis/postgis:18-3.6`.

**Symptom:**

```text
no matching manifest for linux/arm64/v8 in the manifest list entries
```

**Cause:** `postgis/postgis:18-3.6` does not publish a `linux/arm64` image. Docker cannot run that tag natively on an Apple Silicon Mac.

**Current workaround:**

```sh
DOCKER_DEFAULT_PLATFORM=linux/amd64 docker compose up --build
```

`DOCKER_DEFAULT_PLATFORM` affects every service in this command. The Go services will also build and run as amd64 containers. Intel Macs and amd64 Linux hosts do not need this setting.

**Tracking issue:** [#10 Run only PostGIS as amd64 on Apple Silicon](https://github.com/AY2627S1-CS3219-P1/FoC/issues/10)

**Remove this entry when:** The selected PostGIS image publishes an arm64 variant, or the repository switches to a multi-platform image.

## Integration test startup contention

Five integration-test package binaries starting emulated PostGIS containers concurrently exhausted the default 60-second readiness window. Container logs showed only one of the two expected PostgreSQL readiness messages. Serial package runs reached readiness in approximately 20 seconds with the same image and wait strategy.

Run package fixtures serially, and avoid other concurrent PostGIS suites:

```sh
cd supplier-service
go test -p 1 -race -tags=integration ./cmd/server ./internal/location/... ./internal/rpc/... ./internal/idempotency -count=1 -timeout=15m
```

`make test-integration` serializes package execution. Do not increase production timeouts to work around this test-host contention.

**Remove this section when:** integration fixtures no longer emulate this image, or fixture infrastructure guarantees bounded startup independent of package concurrency.
