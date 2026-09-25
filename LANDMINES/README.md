# Landmines

This directory records non-obvious failures that can affect future work. Each entry states when the failure occurs, how to recognize it, and how to proceed.

| Entry | Applies when |
| --- | --- |
| [PostGIS does not start on Apple Silicon](postgis-apple-silicon.md) | `uname -m` prints `arm64` and Compose uses `postgis/postgis:18-3.6`. |
| [Both services exit without Firebase credentials](firebase-credentials.md) | `FIREBASE_CREDENTIALS_JSON` is empty and the containers have no Application Default Credentials. |
| [Recreated services download Go dependencies again](go-cache.md) | Compose replaces a Go service container, including after `docker compose down` or `docker compose up --force-recreate`. |
