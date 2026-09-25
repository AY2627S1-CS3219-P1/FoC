# Both services exit without Firebase credentials

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

**Tracking issue:**

[#11 Add Firebase Auth Emulator to local Compose](https://github.com/AY2627S1-CS3219-P1/FoC/issues/11)

Note that we might just end up not using Firebase altogether, in which case would also render this landmine obsolete.

**Remove this entry when:** A fresh checkout can serve both health endpoints through Compose without a service-account key:

```sh
curl --fail http://localhost:8081/api/health
curl --fail http://localhost:8082/api/health
```
