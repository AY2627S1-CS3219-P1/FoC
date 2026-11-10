# Local performance and resilience run

On 10 November 2026, the harness exercised the real five services at revision
`ae4ee3cf3ca415d1784150eb3de6ed3b8f1bb9d7`. The local API pilot was fast, but
mutation load exposed delayed event publication and credit settlement. Map
failure handling also lacks the required unavailable message. These application
failures are recorded in issues; this change implements the test harness.

The [machine-readable results](aws-performance-local-results.json) record every
requirement as pass, fail or blocked. AWS acceptance remains incomplete. Raw
evidence, logs, commands and screenshots are private under
`.local/perf/local/results/`; the disposable local containers were removed after
collection. Deployment settings remain configurable in the
[runbook](aws-performance.md).

## Environment and data

The isolated Docker network contained PostgreSQL 18/PostGIS 3.6, Kafka 4.2.2,
the five compiled Go services, the Vite frontend, Mailpit and the private Telegram
recorder. Kafka topics had three partitions and one replica. HTTPS terminated at
a localhost Vite proxy with the deployed gateway's public RPC allowlist.
Authentication used real RequestLink emails and Login/Refresh RPCs. No application
authentication bypass or public test endpoint was added.

Applications, broker, database and generator shared a Linux host reporting 22
logical CPUs and approximately 31 GiB RAM. Each application had one instance;
Docker CPU and memory limits were unset. This does not represent AWS sizing or a
separate load-generator machine. `local-environment.json` retains runtime image
IDs, binary hashes and limits; `generator.jsonl` contains pilot resource samples.

Tools were Go 1.27.3, Python 3.14.9, Node 24.15.0, k6 2.3.0 and Playwright 1.64.1.
The services used kafka-go 0.4.49. Fixture seed 93219 supplied 20,000 funded users,
1,000 suppliers, 10,000 open requests and 100,000 historical requests, plus staged
event source facts. Initial verification found no inconsistent accounts or
missing reservations.

## API pilot

Twenty workers, with ten exclusive accounts each, ran a ten-second warmup and
30-second measurement. Every iteration made one real HTTP call with no think
time. Latency surrounds the complete call, including response receipt.

| Measurement | Observed |
| --- | ---: |
| Measured attempted/completed calls | 13,849 / 13,849 |
| Throughput | 461.63 calls/s |
| Failures | 0 |
| Successful within 500 ms | 100% |
| p50 / p90 / p95 / p99 / max | 13 / 159 / 258 / 301 / 435 ms |
| Minimum one-second mean measured concurrency | 18.57 |
| Read / authentication-profile / mutation calls | 9,692 / 2,771 / 1,386 |
| Actual workload shares | 69.9834% / 20.0087% / 10.0079% |

k6's latency/error thresholds passed. The overall API requirement correctly
failed: this was a pilot with fewer than 10,000 concurrent calls, shorter warmup
and measurement, and finite schedule tails that did not achieve exact 70/20/10
shares. No workload tolerance or capacity assumption was substituted.

The original raw points remain in `k6-pilot.json.gz`. The final harness also
classifies missing/non-rotating refresh cookies as failures; that refinement was
verified with the real k6 runtime against a controlled TLS server after this
pilot. The full service pilot predates that refinement.

## Settlement failure

After load, a public create/cancel workflow exceeded its 30-second deadline:
Order was cancelled but Credit still held the reservation. At 06:08:10 UTC,
Order had 2,626 unpublished status events and 877 unpublished outcome events;
the oldest pending event was 437 seconds old. Consecutive publication gaps were
approximately 1.016–1.030 seconds. Credit's outcome consumer had zero broker lag
when checked, so broker lag alone concealed the unpublished backlog.

The shared synchronous Kafka writer leaves the one-second batching timeout and
100-message batch size at their defaults. Order's outbox relay submits one
message and waits for acknowledgement before submitting the next, forcing the
batch timer to expire for each message. User's relay shares this structure;
its sustained publication rate was not measured.

The same core workflow passed after starting Order on a separate, freshly
migrated database with the same fixtures and no accumulated outbox. The original
database and its backlog were retained during the comparison. Checks covered
release before pickup, acceptance/pickup/cancellation after pickup, one-credit
payout, and account/ledger consistency. This isolated the backlog; it did not
repair or reconcile the original producer queue.

See [issue #321](https://github.com/AY2627S1-CS3219-P1/FoC/issues/321),
`outbox-backlog.json`, `core-failure/assertions.json` and
`core-isolated/workflow.json`.

## Contention and events

The verified contention run passed all 100 fresh requests with 50 distinct
couriers each. Every request had exactly one winner, assignment-history row,
assignment transition, successful workflow, applied Credit assignment and ADJUST
operation. Replaying all original retry keys left the durable snapshots
unchanged. The maximum spread between the 50 call-start timestamps was 0.924 ms.
Evidence is in `contention-verified/contention-rounds.jsonl`.

An earlier run exposed a harness error: it compared the client's retry key with
the server's separate workflow ID. Its database effects were correct, but its
response validation failed. The corrected validator verifies the stored
caller/key/workflow mapping and stable replay identity; the 100-request rerun
used fresh requests, rather than resetting exercised fixtures.

Replay passed on all four topics: ten concurrent identical duplicates per
original event, an older status revision and a terminal outcome with a different
event ID. The helper waited for consumer offset barriers, then compared actual
committed source/consumer snapshots, balances and ledger effects. No duplicate
effects or inconsistent accounts were found.

A separate six-second pilot published 100 fresh events at 1,000/minute, 25 on
each topic. All 100 broker-accepted events reconciled within five seconds.
Publish-start-to-observed-commit latency had a 1,105 ms p95 and 1,205 ms maximum,
including 100 ms polling uncertainty. The event requirement correctly failed
the full ten-minute/10,000-event acceptance checks. This helper publishes
directly to Kafka; it does not establish production outbox throughput. Final
event and replay checks also passed reconciliation using corrected UUIDv8
fixtures (seed 93220); their evidence is in `event-pilot-v8/` and `replay-v8/`.

## Browser outage

Playwright aborted 18 real OpenStreetMap tile connections. Core controls remained
usable, and a request was created and cancelled through real RPCs. After
restoration, six real tile responses returned HTTP 200. No browser runtime
errors or application RPC failures were recorded.

The map displayed controls and attribution without an explicit unavailable
message, so outage acceptance failed. The configurable browser runner reproduced
the failure and then passed a real create/cancel workflow after restoration.
See [issue #322](https://github.com/AY2627S1-CS3219-P1/FoC/issues/322),
`browser/local-browser.json`, `browser/map-outage-form.png`,
`browser/map-outage-cancelled.png` and `browser-harness/recovery-pass.png`.

Browser testing also caught fixture UUIDs that PostgreSQL accepted but the UI
rejected. The generator now produces deterministic UUIDv8 values with the RFC
variant in both Go and SQL. Browser checks used corrected seed 93220. A separate
integration test verified the corrected generator with the full default dataset
(seed 3219), applied actual service migrations, seeded twice and reconciled
accounts and event source facts.

## Commands and verification

Private database/broker environment variables, separate session pools and the
localhost CA were configured as described in the runbook. Representative commands
actually executed were:

```sh
make perf-build
TEST_PERF_FULL_DATASET=1 GOWORK=off go -C scripts test -mod=readonly -race -count=1 -v ./perf
PERF_VUS=20 PERF_ACCOUNTS_PER_VU=10 PERF_WARMUP_SECONDS=10 PERF_MEASURE_SECONDS=30 \
  /tmp/foc-k6 run --out json=.local/perf/local/results/k6-pilot.json.gz scripts/perf/api.js
.tools/bin/perf contention --fixtures .local/perf/local/fixtures/contention-fresh.json \
  --base https://localhost:18443 --sessions .local/perf/local/courier-sessions.json \
  --out .local/perf/local/results/contention-verified
.tools/bin/perf replay --fixtures .local/perf/local/fixtures-v8/fixtures.json \
  --corpus .local/perf/local/fixtures-v8/events.json --count 4 --out .local/perf/local/results/replay-v8
.tools/bin/perf events --fixtures .local/perf/local/fixtures-v8/fixtures.json \
  --corpus .local/perf/local/event-pilot-v8.json --count 100 --rate 1000 --duration 6s \
  --out .local/perf/local/results/event-pilot-v8
node frontend/scripts/perf-browser.mjs .local/perf/local/browser-outage-config.json
node frontend/scripts/perf-browser.mjs .local/perf/local/browser-recovery-config.json
make perf-test
make fmt GO_MODULES=scripts
make lint GO_MODULES=scripts
```

The Go integration command also had `TEST_PERF_DATABASE_URL` and `TEST_K6_BINARY`
set privately. A separate real Kafka regression verified replay barriers with
uncommitted empty partitions. Final Go tests, Python evidence/restore checks,
Node workload checks, formatting, lint and `git diff --check` passed.

AWS metadata/telemetry, a separate generator, the full API/event durations,
remaining independent/combined dependency outages, proven crash boundaries,
Telegram's send-before-record crash window, and native overdue alerts remain
unverified. The report keeps those requirements failed or blocked.

## Recovered harness verification

The unfinished harness was recovered onto main `7029d19b` and verified again
on 10 November 2026. Its historical pilot results above remain tied to
`ae4ee3cf`; this verification did not rerun the full application pilot.

- All six Go harness tests passed with race detection, including actual k6
  execution, a fresh three-partition Kafka replay barrier, and the full default
  PostgreSQL/PostGIS fixture dataset seeded twice against current migrations.
- Five Python reporting/fault-restoration tests and two Node workload checks
  passed. The complete Go tooling module, formatting and lint checks passed.
- CI now runs the basic harness checks. Live integrations remain opt-in and
  their absent environment variables produce explicit skips.

The Kafka/outbox runtime changes are reviewed separately under issue #321.
The map unavailable-state defect remains tracked in #322. No AWS resources,
existing local deployment, credentials or authentication behavior changed.
