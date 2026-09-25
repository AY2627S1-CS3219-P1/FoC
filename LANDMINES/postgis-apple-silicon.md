# PostGIS does not start on Apple Silicon

**Applies when:** `uname -m` prints `arm64` and Compose uses `postgis/postgis:18-3.6`.

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
