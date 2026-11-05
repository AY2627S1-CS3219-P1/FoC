# Wireframe UI verification

Verified on 4 November 2026 from the `yihao/feat/wireframe-ui` worktree.
PR #241 remains a draft on #231. This is local verification, not evidence of a
merged or deployed change.

## Reproduce the browser regression checks

```sh
cd frontend
npm ci
npx playwright install chromium
npm run dev
# In another terminal, from frontend/:
npm run test:wireframe
```

Set `WIREFRAME_BASE_URL` to check a different frontend origin. These checks mock
Connect responses and block map tiles. They cover registration with Telegram
skipped or selected, drawer focus on opening and dismissal, and proof selection
surviving both an ordinary order refresh and a nonterminal revision change.
They also cover recent pickups beyond supplier page one (ordinary, archived and
unavailable locations), plus a working sign-in link on anonymous My orders.
They do not substitute for the real-stack checks below.

The proof regression failed before the fix on its first forced background read.
The form's effect read `orderId` directly through a parent expression backed by
the entire Order object, so replacing that object recreated its controller even
when the ID was unchanged. A derived ID now stabilizes that dependency. The
panel also keeps evidence mounted across reads of the same order/participant;
previously it cleared evidence on every revision and discarded selected files.
Changing the order or participant still clears the old evidence.

## Local stack

Started `docker compose up -d` from this worktree with a new `.env` copied from
`.env.example`, then ran `make seed-demo`. All application calls used the actual
Compose services, PostgreSQL, Kafka, Mailpit and Floci object storage. Browser
sign-ins used emailed magic links, with separate contexts for Student, Other
Student and Suspended Student. No access tokens or magic links are recorded here.

The browser driver and full-size captures are retained locally under
`/tmp/foc-shots/`. The initial run is `real-stack.mjs`, with results in `real/`;
`real-resume.mjs` continues withdrawal and cancellation in `real-resumed/`.
`real-onboarding.mjs` covers new accounts, with captures in `real-onboarding/`.
`real-proof-fixed.mjs` checks polling and the actual late graph in
`real-proof-fixed/`; `real-proof-valid.mjs` completes that order in
`real-proof-resume/` using the smaller `ANNA.jpeg` proof fixture. The first
24.47 MP fixture was rejected by proof validation; no decoder limit
was changed.
These are local diagnostic scripts, not repository test entry points.

| Flow | Evidence |
| --- | --- |
| Login | Real RequestLink → Mailpit email → Login, for all three seeded roles |
| Registration | New accounts with Telegram skipped, and selected while the bot is disabled; both reach home |
| Create with photo | Supplier's Request link prefilled pickup; JPEG PUT to Floci succeeded; created order displayed its photo |
| Create without photo | A separate order was created through the form with pickup/destination search |
| Accept | Courier accepted the current reward and reached Awaiting pickup |
| Counteroffer | Courier submitted 25 credits; requester accepted and assigned that courier |
| Withdraw | Confirmation dialog submitted withdrawal; requester read Awaiting courier afterward |
| Cancel | Requester's confirmation dialog cancelled the reopened order |
| Suspended user | Create screen explains suspension and exposes no submit action |
| Maps | Seeded Supplier coordinates produced Leaflet markers; selected pickup/destination resolved through Supplier |
| Proof selection | A selected JPEG survived actual background polling after the fix |
| Late delivery | Waited past the actual five-minute full-reward deadline; graph appeared; a valid JPEG was uploaded and verified, then delivery confirmed before the ten-minute final deadline |
| Receipt | Requester confirmed receipt; Order reached Completed; profile credit history loaded |
| Settlement | Credit's courier payout ledger entry was 13 credits, matching the frontend estimate computed from the actual pickup/delivery timestamps in integer nanoseconds |

The settled delivery's [phone payout graph](assets/wireframe-ui/late-payout-phone.png)
was captured against the real services before proof submission. Settlement was
checked after Credit consumed the completion event; immediate reads can precede
account creation/settlement. The local `real-settlement.mjs` driver records the
matching expected and paid values under `real-settlement/`.

## Comparison captures

These phone captures use the same mocked data on the baseline and current UI.

| Screen | Before | After |
| --- | --- | --- |
| Login | [Before](assets/wireframe-ui/before-login-phone.png) | [After](assets/wireframe-ui/after-login-phone.png) |
| Registration | [Before](assets/wireframe-ui/before-register-phone.png) | [After](assets/wireframe-ui/after-register-phone.png) |

## Limits and release preparation

- `TELEGRAM_BOT_TOKEN` and `PUBLIC_TELEGRAM_BOT_URL` are unset. The real optional
  unavailable path passed; opening/linking a live bot and the order chat button
  still require the actual bot configuration. No placeholder public bot URL was
  saved.
- No logo asset exists yet. Login uses the existing FoC wordmark.
- Public map tiles were blocked in real-flow tests and served only from the
  existing local cache for comparison screenshots. This verifies map markers
  and route data, not tile-provider availability or geolocation prompts.
- Comparison screenshots use mocked Connect data, with before at `422bb11` and
  after at the current worktree. Refreshed captures live in
  `/tmp/foc-shots/refreshed/`. The refreshed images and captions were published in PR #241 on 4 November
  2026, pinned to screenshot commit `55505e0`. The captions record that the implementation was local at capture time; those
  changes are now committed to the PR branch.
