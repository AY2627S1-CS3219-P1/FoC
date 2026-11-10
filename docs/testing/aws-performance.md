# AWS performance and resilience harness

The private harness in [`scripts/perf`](../../scripts/perf) implements the API,
contention, event and fault-test plan. Deployment access is deliberately
configurable. It never provisions resources, changes replica counts, or installs
an application test endpoint. Local harness checks are not AWS acceptance results.

## Configure a run

Install Go, Python 3, Node, k6 **2.3.0**, AWS CLI, kubectl and the frontend's
locked npm dependencies. Install Chromium with `npm exec --prefix frontend --
playwright install chromium`. Run the generator on a separate machine from EKS.

```sh
make perf-build
mkdir -p .local/perf
cp scripts/perf/config.example.json .local/perf/config.json
python3 scripts/perf/run.py --config .local/perf/config.json preflight
```

Fill in `baseUrl` (the public HTTPS origin), region, AWS profile, Kubernetes
context, namespace, API Gateway ID, RDS instance ID and worker instance IDs.
Use a **fresh `out` directory for each experiment**; keep fixtures and sessions
outside it. Empty defaults produce blocked requirements and a report.

Supply private connections through environment variables, not configuration
files or command arguments:

| Variable | Connection |
| --- | --- |
| `PERF_USER_DB` | User PostgreSQL URL |
| `PERF_SUPPLIER_DB` | Supplier PostgreSQL URL, with PostGIS |
| `PERF_ORDER_DB` | Order PostgreSQL URL |
| `PERF_CREDIT_DB` | Credit PostgreSQL URL |
| `PERF_COMMUNICATION_DB` | Communication PostgreSQL URL |
| `PERF_KAFKA_BROKERS` | Comma-separated private broker addresses |

Use the existing private network or operator port-forwards, with the deployment's
TLS settings. Broker advertised addresses must also be reachable. The helper uses
the deployed demo's plaintext private Kafka transport and fixed consumer group
names (`credit-service`, `communication-service`, `user-service`). Environments
with Kafka SASL/TLS or renamed groups require adapting that connection first.
No private listener needs a public Gateway route.

Preflight captures source revision, dirty state, tools, image digests, nodes,
replicas, resource limits, RDS sizing/configuration, gateway stages/quotas and EC2
credit mode. During load, generator `/proc` samples, Kubernetes usage and
CloudWatch resource/gateway/credit series are saved. Supply read-only argument
arrays in `environmentCommands` for `deployedRevision`, `brokerConfiguration`,
`databaseParameters` and `startingCpuCredits`. These must read the actual deployed
revision, broker configuration, active database parameters and initial CPU credit
series; missing/failed/empty captures keep environment acceptance blocked. Add
`telemetryCommands` for gateway throttling logs and organization-specific
dashboards. Terraform defaults are not proof of live sizing. Inspect returned
CloudWatch series for actual datapoints rather than treating an empty response
as a reading.

## Fixtures and sessions

```sh
python3 scripts/perf/run.py --config .local/perf/config.json fixtures
python3 scripts/perf/run.py --config .local/perf/config.json seed
python3 scripts/perf/run.py --config .local/perf/config.json verify
```

Defaults generate 20,000 funded users, 1,000 suppliers, 10,000 open requests and
100,000 historical requests, plus 10,000 staged event inputs across all four
topics. `fixtureSeed` and `fixtureAt` reproduce identities and content. Generated
SQL uses each owning schema, application constraints and append-only ledger rules.
Every funded account has a matching grant, holds and ledger; each historical
request has audit history and a released reservation. Extra event users await
their first grant. Extra event orders have matching lifecycle/audit/assignment
facts and holds awaiting the intended consumer effect.

Seeding is additive and repeatable, commits separately in each service database,
and preserves unrelated records. It does not reset already-exercised fixtures.
The verification command checks fixture counts, grants/ledger balance sums and
reservation holds before accepting the dataset. Synthetic seed grants fund the
test pool with 1,000,000 credits per account; they do not benchmark user creation
or the real outbox producer. Run failure-boundary tests against real application
outboxes separately.

Authenticate through the normal `AuthService/RequestLink` and email flow. For
`@perf.foc.test` fixture addresses, configure Communication Service with a private recording SMTP mailbox in
the dedicated deployment; SES cannot deliver to that test domain. Capture fresh
magic links in the private `challenges` file:

```json
[{"userId":"fixture-user-uuid","token":"single-use-token-from-real-login-email"}]
```

```sh
python3 scripts/perf/run.py --config .local/perf/config.json sessions
```

The helper exchanges these challenges through the public Login RPC, verifies
identity and keeps real access tokens/refresh cookies in the private session
file. It does not mint JWTs or insert authentication/session rows. Challenges
expire; obtain and exchange them in bounded batches when needed. Keep one
exclusive session pool per concurrent tool, including Playwright. Refresh rotates
cookies; never reuse a stale copy of a consumed pool. Generate a fresh pool for
each k6 run, which deliberately does not export its in-memory rotated tokens.

Set `users >= vus * accountsPerVU`. Increase the account bank after the pilot if
per-account creation demand could exceed the existing **10 creations/minute**
limit. A worker runs about one creation per 20 calls; size the bank using the
observed calls/minute and retain headroom. There is no rate-limit bypass, think
time, hidden refresh or automatic balance refill during measurement.

## Pilot and measured API run

```sh
python3 scripts/perf/run.py --config .local/perf/config.json pilot
python3 scripts/perf/run.py --config .local/perf/config.json api
```

Review the pilot's CPU, memory, network, descriptors/connections and measured
concurrency before choosing `vus` and account count. Store the pilot in its own
output directory and record a `generator.json` requirement with that reviewed
evidence; the runner leaves generator acceptance blocked automatically. The
default 20,000 workers is only an initial calibration value. Generator health,
bandwidth and separation from deployment cannot be inferred from VU count.

The measured scenario has 120 seconds of warmup, **600 uninterrupted seconds**
of measurement and a 30-second graceful drain. Every worker has one outstanding
HTTP call and no think time. A deterministic 100-call schedule yields 70 reads,
20 authentication/profile calls and 10 mutations. Refresh calls consume their
own authentication slots. Searches, pages, locations and orders rotate; create
and cancellation use per-account state and UUID retry keys.

`full_call_ms` surrounds the synchronous HTTP call, including DNS, connection,
TLS and response receipt. `http_req_duration` remains diagnostic. Timeouts,
transport failures, HTTP/RPC errors, throttling, malformed responses, wrong
identities and non-completed mutations fail. All attempted measured calls count
in the denominators, including their completions during drain. Interrupted or
unbalanced calls fail completeness.

Raw signed start/end points reconstruct the **time-weighted mean number of
actual outstanding measured HTTP calls in each measurement second**, excluding
warmup calls. This scenario requires every one-second mean to be at least 10,000; active
VUs are never substituted. This measures 10,000 outstanding API calls, which
is stronger than 10,000 active users with pauses between their actions.
State the chosen concurrency definition when reporting results. This is an interval mean, not a proof of an
instantaneous 10,000-call minimum at every microsecond. The report exposes all
600 interval values. Full-call p95 must be at most 500 ms, at least 95% of
attempts must succeed within 500 ms, and failure rate must be strictly below 1%.
This broader harness uses an exact 70/20/10 read/authentication/mutation mix.
That is a stricter experiment than the read/search-only NFR-01; report them
separately, and do not present a mixed-workload failure as a measured failure
of the narrower read/search requirement.
Finite cycle tails can make actual shares differ slightly; the report exposes
this and fails the mix check instead of silently adding a tolerance.

## Contention and events

```sh
python3 scripts/perf/run.py --config .local/perf/config.json contention
python3 scripts/perf/run.py --config .local/perf/config.json events
```

Contention releases 50 distinct authenticated couriers through one shared
barrier on each of 100 fresh open requests. Database checks require one eventual
winner, assignment-history row, assignment transition, successful attempt,
applied Credit assignment and successful ADJUST operation. Equal-price
acceptance can legitimately produce no nonzero ADJUST ledger entry. Original
attempt IDs, including losers, are replayed and durable assignment, financial
and audit snapshots must remain unchanged. Actual release/start timestamps and
RPC results are retained per round.

The generated corpus contains 2,500 events for each deployed topic. Throughput
publishes 1,000 events/minute for ten minutes with bounded concurrent publishers.
Inputs must match staged source state and have an absent intended consumer
effect; already-applied events cannot establish throughput. Each minute must
contain 1,000 acknowledged publications. At least 99% of acknowledged events
must reach their expected database-visible state within five seconds, and every
acknowledged event must reconcile. Uncertain publications are retained separately.
Credit accounts are reconciled against their ledger and active holds afterward.

Timing uses one generator clock, from publish start to observed committed state,
as a conservative upper bound. Receipts retain acknowledgements, last absent
observations and observation times; the polling interval defaults to 100 ms.
Queries themselves have duration/visibility uncertainty. Polling adds private
database load, so inspect generator/database utilization during the pilot.
Status-event throughput verifies Communication's participant projection;
notification sends require the separate linked-user/recorder cases.

Use a **different fixture seed and private fixture directory** for replay (effects
must again start absent). Point `corpus` and `fixtures` at those files, seed them,
then run:

```sh
python3 scripts/perf/run.py --config .local/perf/replay-config.json replay
```

The default four-event replay covers every topic, ten concurrent identical
duplicates, an older status revision and a terminal outcome with a different
event ID. The helper waits for group offsets to cross the post-publication
high-water marks before comparing committed snapshots. Offsets alone never
establish correctness. Keep event, replay, API and contention measurements
separate. Do not run competing generators against this deployment concurrently.

## Dependency and crash cases

Populate `cases` using [`fault-case.example.json`](../../scripts/perf/fault-case.example.json).
Each hook is an argument array executed without a shell. Hooks are private
operator programs targeting the configured dedicated deployment. Empty hooks
remain blocked. No deployment patch or fault is inferred from an empty config.

`apply` injects the condition; `injectionEvidence` must output a JSON object with
`status: "pass"` and concrete `checks`; `core` executes the workflow; `restore`
always runs in `finally`; `recovery` verifies recovered state. The private Go
`assert` command executes SQL in read-only transactions and compares actual
scalar/JSON values with expected values. Its JSON output meets the evidence
interface; see [`assertions.example.json`](../../scripts/perf/assertions.example.json).
A command exiting zero or merely killing a pod does not establish a boundary.

```sh
.tools/bin/perf assert --assertions .local/perf/boundary.json --out .local/perf/case-evidence
.tools/bin/perf workflow --fixtures .local/perf/fixtures.json \
  --base "$PERF_BASE_URL" --sessions .local/perf/fault-sessions.json --out .local/perf/core
python3 scripts/perf/run.py --config .local/perf/config.json cases
```

The core helper covers create, accept, pickup, cancellation before/after pickup,
credit reads, release, payout and account/ledger consistency through real APIs
and private assertions. It persists refreshed sessions. Use Playwright for actual
browser workflow completion too:

```sh
node frontend/scripts/perf-browser.mjs .local/perf/browser-case.json
```

Copy [`browser.example.json`](../../scripts/perf/browser.example.json), supply the
actual route, UI selectors/actions, unavailable message, completion state and
recovered control. The runner takes screenshots and saves action evidence. It
can inject browser-side map connection failures, 503s or timeouts; server-side
faults belong in deployment hooks. `browser` and `browserRecovery` commands are
mandatory for outage acceptance. Recovery configurations must omit the outage
route interceptions. The current map has no explicit tile-failure message and
the repository has no identified AI integration; these are coverage gaps to
measure/report, not substitute controls or assumed successes.

Cover map, chat, AI and notifications independently for connection failures,
server errors and timeouts, plus a combined outage. For producer crashes, prove
broker acknowledgement occurred while outbox confirmation remained absent.
For consumer crashes, prove either rollback before commit or committed state
before offset acknowledgement. Restore consumption and compare effects after
the relevant event is redelivered. The repository has no generic deterministic
crash gates: supply real boundary instrumentation/locking hooks or leave the
case blocked.

```sh
.tools/bin/perf record --listen 127.0.0.1:9098 --out .local/perf/telegram-recorder
```

Point Communication's existing **`TELEGRAM_API_URL`** at the private recorder;
link dedicated fixture users first. A private `record-mode.json` file selects
`healthy`, `connection_failure`, `server_error`, `timeout` or `hold_after_send`.
The latter records an accepted external send and withholds its response. Prove
the delivery row is absent, crash the consumer, switch to healthy and replay.
Record resulting duplicate sends explicitly: Telegram's send-before-record
window currently permits them. Never report exactly-once notification delivery
from ledger/projection deduplication. Restore the real endpoint afterward.

For overdue visibility, stall consumption for over 60 seconds. Supply
`operationalTelemetry: {"source": "actual operational system", "command": [...]}`
returning `{"alerts":[{"eventId":"...","topic":"...","consumer":"...",
"ageSeconds":61,"failureOrBacklog":"..."}]}` from that system's native evidence.
Query the historical outage interval if collected after restoration. Reconcile
every accepted event in the recovery hook. The test observer and its timestamps
do **not** satisfy this requirement. The current Kafka wrapper does not provide
this per-event overdue alert by itself; missing operational telemetry stays
blocked or fails the live acceptance test.

## Evidence and local verification

CI runs the Go, Python and Node harness checks through `make perf-test`.
Real PostgreSQL, Kafka and k6 runtime integrations require the explicit test
variables below; their absence is a skipped integration check, not evidence
that a deployment meets its performance requirements.

```sh
python3 scripts/perf/run.py --config .local/perf/config.json report
make perf-test
make fmt GO_MODULES=scripts
make lint GO_MODULES=scripts
```

Every run produces `report.md` and `results.json` with pass/fail/blocked for every
requirement. Raw k6 points, p50/p90/p95/p99/max, errors by endpoint/cause,
concurrency, shares, environment, commands, telemetry, event receipts and browser
screenshots remain alongside them. Keep artifacts private: session files,
mailbox contents and screenshots can contain credentials or personal data.

For local integration verification set `TEST_PERF_DATABASE_URL` to a disposable
PostgreSQL+PostGIS database and `TEST_K6_BINARY` to k6, then run `make perf-test`.
The database test applies the actual service migrations in unique schemas, seeds
twice and checks consistency. The k6 test uses an isolated TLS RPC fixture and
checks cookie rotation and create/cancel execution. Neither establishes deployed
throughput, browser behavior, fault boundaries or AWS thresholds.

For the full default dataset, set `TEST_PERF_FULL_DATASET=1`; the integration test
then seeds all 20,000 users, 1,000 suppliers, 10,000 open and 100,000 historical
requests twice. `TEST_KAFKA_BROKERS` and `TEST_KAFKA_TOPIC` enable a real-broker
replay-barrier regression; provide a fresh empty three-partition topic.
The browser runner supports `press` actions (`key: "Enter"`) for location searches.
A disposable localhost HTTPS server can set `localSelfSigned: true`; the runner
rejects that option for every non-loopback origin. See the
[local run findings](aws-performance-local.md) for actual integration results.

References: [k6 duration metrics](https://grafana.com/docs/k6/latest/using-k6/metrics/reference/),
[k6 custom metrics](https://grafana.com/docs/k6/latest/javascript-api/k6-metrics/),
[Playwright contexts](https://playwright.dev/docs/api/class-browsercontext),
[Kafka guarantees](../../pkg/kafka/README.md),
[Telegram delivery ADR](../adr/telegram-chat-delivery.md).
