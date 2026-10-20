# Cancellation and disposition verification

## Scope

B3 integrates cancellation/withdrawal from PR171 (`59dd1b9`) with confirmed
revision checks, participant pickup/receipt, audited final location and the shared
Order detail UI. Suspension and timer-worker implementation remain separate.

## Observed checks

- Buf lint and generation passed. Compatibility against archived `59dd1b9`
  schemas passed. Generated Go/TypeScript contracts accompany their sources.
- Repository formatting and lint passed across the configured Go modules.
- Full Order Go suite passed with `-race`.
- Cancellation, withdrawal, disposition and progress integration tests passed
  against fresh disposable PostgreSQL18 databases, with `TEST_DATABASE_URL` unset.
- Frontend check passed with zero errors and warnings; all 85 tests in 14 files
  passed; production build succeeded.
- Production-preview browser scenario passed eight checks with no page errors.
  It covers 360/768/1440px, accessible labels and focused Material controls.

The mounted Connect/PostgreSQL scenario deliberately loses a withdrawal reply,
recovers the durable operation, reassigns the order and verifies redacted replay.
It also exercises direct pickup during pending withdrawal, stale requester
confirmation, pickup, courier post-pickup failure/warning, location retry and
correction. Separate database tests cover eight concurrent identical location
submissions, retained original/corrected audit entries and rollback when the audit
insert fails after updating the order.

The browser scenario loses replies, reloads, checks identical saved key/reason/
revision, verifies requester cancellation during withdrawal, verifies definitive
refusal releases the local intent, injects browser-storage failure before sending,
retains location input after uncertainty, corrects it, and confirms pickup/receipt.

Repeat from the repository root:

```sh
make buf-lint buf-generate fmt lint
cd order-service
GOWORK=off go test -mod=readonly -race ./...
env -u TEST_DATABASE_URL GOWORK=off go test -mod=readonly -race -tags=integration ./internal/order ./internal/rpc/order -run 'Cancellation|Withdrawal|Disposition|Progress' -count=1 -v -timeout=10m
cd ../frontend
npm ci
npm run check
npm test
npm run build
```

For browser proof, start `npm run preview -- --host 127.0.0.1 --port 5193 --strictPort`
in `frontend/`, then run the following from the repository root with Python
Playwright and Chrome installed:

```sh
python3 frontend/tests/order-cancellation-ui-e2e.py --url http://127.0.0.1:5193 --out .local/order-cancellation-evidence
```

It prints `"passed": 8` and `"pageErrors": []`. Stop only the preview process
started for this check. Test databases are private Testcontainers instances that
are terminated by their test fixtures. No shared or production data is used.

## Independent review

Two independent source-only reviews ran through Devin using Claude Opus 5.5
(high effort), one for Standards and one for Spec. Both assistant result metadata
and provider session state recorded `claude-opus-5.5`. Reviewers did not execute
tests; execution evidence above is separate.

Final reviewed source bundle SHA256:
`c30926689eec8455253364c08bb885e6ef636ce112ab60110e24839b69bab35e`.
Subsequent changes were Go literal layout, a test-fixture injection wrapper and
this evidence/documentation, not production behavior.

| Finding | Disposition |
| --- | --- |
| Requester cancellation disabled during courier recovery | Fixed. Only the courier's cancellation button is disabled by pending withdrawal. Browser regression passed. |
| Unconfigured Credit refusal presented as an unknown admitted operation | Fixed. New admission returns FailedPrecondition without a write. Database and browser regressions passed. |
| Form convention and storage-failure feedback | Fixed with Superforms/Zod, Material inputs/dialog and explicit not-submitted feedback before any RPC. |
| Suspected missing pickup fence | Refuted by the existing Transition guard. Added direct mounted pickup regression, which passed. |
| Suspected replay visibility loss after reassignment | Refuted by F0 historical membership and safe projection. Real mounted replay passed for a suspended former courier. |
| Suspected previous-courier removal flag | Refuted by the schema: the field means courier before the change, not removal by itself. |
| Long modified Go literals | Expanded without behavior changes. |
| Suggested clearing saved intent on PermissionDenied | Not adopted. Authorization can deny an attempt before the durable ledger is read, so it cannot disprove an earlier admission. Retaining the immutable key is the conservative Q15 behavior. No success or duplicate command is inferred. |

Final Spec review reported no actionable findings. Final Standards review reported
no blockers; its minor notes are disposed of above. No actionable review item is
left open.

## Artifacts and limits

Screenshots, recording and the machine-readable browser report live in
`assets/order-cancellation/`. All identities, locations, roles and auth responses
in the media are synthetic Connect fixtures. The browser uses the actual mounted
production frontend, not a separately implemented demonstration page.

Real PostgreSQL and server-side Connect are tested separately with scripted Credit
and authentication seams. These checks do not prove live workload authorization,
live Credit settlement, User Service warning consumption, B2 proof persistence,
Kafka broker delivery or Telegram delivery. Warning facts and outcome persistence
are not claims that downstream consumers have completed their effects.
