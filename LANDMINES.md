# Landmines

This file records non-obvious failures that can affect future work. Each entry states when the failure occurs, how to recognize it, and how to proceed.

## L1: PostGIS does not start on Apple Silicon

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

## L2: Both services exit without Firebase credentials

**Applies when:** `FIREBASE_CREDENTIALS_JSON` is empty and the containers have no Application Default Credentials.

**Symptom:** Air builds the application and prints `running...`. The application then exits:

```text
google: could not find default credentials
Process Exit with Code: 2
```

HTTP requests may fail with `Recv failure: Connection reset by peer` because the application server is not running.

**Cause:** Both services initialize Firebase Admin while constructing the router. The health endpoint cannot serve requests until that initialization succeeds.

**Current workaround:**

1. Create a non-production Firebase project.
2. Download a development Admin SDK service-account key.
3. Store the key outside the repository.
4. Load the key into the shell that will start Compose:

   ```sh
   chmod 600 ~/.config/foc/firebase-service-account.json
   export FIREBASE_CREDENTIALS_JSON="$(jq -c . ~/.config/foc/firebase-service-account.json)"
   docker compose up
   ```

Do not commit the key, copy it into `.env`, or expose it in chat or logs.

**Tracking issue:** [#11 Add Firebase Auth Emulator to local Compose](https://github.com/AY2627S1-CS3219-P1/FoC/issues/11)

**Remove this entry when:** A fresh checkout can serve both health endpoints through Compose without a service-account key:

```sh
curl --fail http://localhost:8081/api/health
curl --fail http://localhost:8082/api/health
```
