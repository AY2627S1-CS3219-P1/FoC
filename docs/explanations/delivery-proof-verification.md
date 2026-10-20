# Delivery proof verification

The proof path is mounted on the delivered B3 dependency. OrderTimers owns downstream lifecycle startup/expiry/timeout in PR #183. This record distinguishes actual backend infrastructure from explicit external test seams. No PR was merged or deployed.

## Real backend path

Run from `order-service/` with Docker available and ImageMagick/libheif installed:

```sh
env -u TEST_DATABASE_URL GOWORK=off go test -mod=readonly -race -tags=integration ./internal/order -run '^Test(PostgresProofReceipts|ProofGrantRefreshPreservesPendingReceipt)$' -count=1 -timeout=5m -v
env -u TEST_DATABASE_URL GOWORK=off go test -mod=readonly -race -tags=integration ./internal/rpc/order -run '^TestProofsThroughConnectPostgresAndS3$' -count=1 -timeout=6m -v
```

The first command passed 14 PostgreSQL18 leaf scenarios: 13 receipt cases and the separate grant-refresh race regression. The race was first reproduced against `ed03fda` before the fix, when the same valid on-time receipt was incorrectly rejected. The second passed both mounted generated-Connect scenarios with PostgreSQL18, signed/auth-enforced Floci and all five real image codecs. It uploads an actual image, rejects an immutable overwrite with HTTP412, verifies it, loses subsequent storage inspection, commits a pending receipt before the deadline, recovers after the deadline using a fresh controller, loses an accepted acknowledgment, replays after requester completion, checks participant-only downloads and observes one delivery audit and settlement outcome with the original receipt time. Disguised JPEG bytes and an uncertified outage never deliver.

Identity middleware and Credit are scripted external seams. These tests do not claim real login, a live Credit settlement consumer, Kafka/Telegram delivery or production infrastructure. The image requirement is actual decoding, not proof that a photograph depicts a delivery.

## Mounted browser path

The real Svelte route, form/controller and generated Connect adapter run against explicit synthetic API/storage fixtures. The recorded flow uses no real credentials or personal photos.

Start the frontend in one terminal:

```sh
cd frontend
npm ci
npm run dev -- --host 127.0.0.1 --port 5194 --strictPort
```

From the repository root in a second terminal, with Python Playwright and its Chromium installed:

```sh
python3 frontend/tests/delivery-proof-ui-e2e.py --url http://127.0.0.1:5194 --out .local/order-proof-browser
```

For an installed Chrome rather than Playwright's downloaded Chromium, add `--browser-channel chrome`. The browser is isolated and headless. Stop only the frontend process started for this check after the script finishes; its output media stay in the chosen directory.

Observed: four medium-hard scenarios passed at 360/768/1440px with zero page errors. Failed upload/verification retained all three files, rejected premature confirmation and retried the same upload identity without uploading successful files again. An uncertain removal retained the file until retirement was acknowledged. A server-denied outage did not bypass proof. Lost confirmation followed by page reload restored the server's immutable three-photo unattended submission; keyboard retry sent identical input. The requester saw the evidence gallery, while a nonparticipant admin saw neither proof nor confirmation controls.

- [Retained failed-photo state](delivery-proof-retained-mobile.png)
- [Recovered pending confirmation with keyboard focus](delivery-proof-pending-mobile.png)
- [Mobile evidence gallery](delivery-proof-gallery-mobile.png)
- [Desktop evidence gallery](delivery-proof-gallery-desktop.png)
- [Retry/reload interaction recording](delivery-proof-retry-reload.mp4) ([original WebM](delivery-proof-retry-reload.webm))

## Final checks and review

Root `make fmt-check && make lint` reported zero issues. All Order packages passed 320 race-enabled test cases. Frontend `npm run check && npm test && npm run build` reported zero Svelte errors/warnings, 112 tests across 16 files and a successful build. Existing dependency/build-adapter warnings are not reported as zero build warnings.

Native Devin CLI full review covered `ed03fda` against delivered B3 `dea9c7c`, followed by a focused review of the fixes committed through `d5aa5ee`. Exported runtime generation metadata independently shows 40 full-review and 10 follow-up generations, all `claude-opus-5-5-medium`. All 62 source SHA-256 values from the two pinned review manifests match the committed `d5aa5ee` tree. [Sanitized model/source provenance](delivery-proof-review-provenance.json) records those values without personal session identifiers or credentials. Later changes to this report and the plan are readiness/evidence documentation only. All 58 runtime/contract/deployment file hashes still match. From the repository root, verify the same source without depending on a branch name:

```sh
python3 - <<'PY'
import hashlib, json, pathlib
p = json.loads(pathlib.Path("docs/explanations/delivery-proof-review-provenance.json").read_text())
for name, expected in p["runtime_source_sha256"].items():
    assert hashlib.sha256(pathlib.Path(name).read_bytes()).hexdigest() == expected, name
print("REVIEWED RUNTIME SOURCE MATCH:", len(p["runtime_source_sha256"]), "files")
PY
```

Opus's final verdict is **ready on the declared B3+B2+B4 stack, with no blockers**. Both blockers are resolved: renewable grant metadata no longer invalidates immutable proof, and a killed native decoder keeps validation retryable. Public application admission, pending-courier reads, transient outage authority classification and stale contract notes were also corrected.

One nonblocking follow-up, **N1**, remains: a decoder output-cap error can be reported as unavailable rather than a known output-cap rejection. It is code-traced in the review, not separately runtime-reproduced. Such input cannot pass first verification, reserve a receipt or cause false acceptance. No unverified fix was added after the second verification round.

Operational warnings remain explicit: custom-store endpoint identity must stay stable or be deliberately migrated, cleanup revisits retired keys at bounded throughput and a persistent candidate error can delay a batch, proof reads briefly take the parent lock, probe replicas can churn certification generations, runtime package versions are not digest-pinned, and recovery replicas may redundantly inspect the same pending receipt. A normally exiting decoder allocation failure can still appear as a precondition failure; the externally killed process path is covered. These are disclosed constraints, not claims of production HA, queue-scale or peak-RSS validation.

## Deployed codec boundary

From the repository root:

```sh
docker build -f order-service/Dockerfile -t foc-order-proof-local:verification .
docker run --rm --memory=3g --cpus=2 --pids-limit=128 --read-only --tmpfs /tmp:rw,noexec,nosuid,size=64m --entrypoint /proof-image-check foc-order-proof-local:verification
```

Both the build and restricted nonroot runtime printed:

```text
PROOF DECODER PASS: JPEG PNG WebP HEIC HEIF actual pixels
```

The runtime policy disables unapproved coders, external delegates, pixel-cache disk spill and inherited workload credentials. The 3GiB container limit is a hard process-group bound, not a measured peak RSS claim. Large-phone image regressions separately exercise 20MP and the exact 24MP boundary.

Report/appeal closure-dependent retention and revocation-safe nonparticipant case-admin authorization remain the approved deferred dependencies. Admin role alone never grants proof access. These deferrals are not hidden behind the passing checks.
