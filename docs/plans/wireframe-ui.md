# Wireframe UI: handover

Status as of 4 November 2026. This describes work in progress on open PRs, not landed structure; check the branches before relying on it.

The goal is #201: make the frontend follow the team wireframe (layout, navigation and screen organization on desktop and mobile) without losing existing behavior.

## Where the work is

| PR | Branch | State |
|---|---|---|
| #231 | `yihao/feat/active-orders-filter` | Ready for review. Adds `ListMyOrdersRequest.active_only`. |
| #241 | `yihao/feat/wireframe-ui` | Draft, stacked on #231. The frontend redesign. |

- Merge #231 first. Keep #241 current by merging, not rebasing: both branches already contain merges from `main`, and the stack is shared.
- PR screenshots live on the orphan branch `yihao/pr-241-screenshots`. Delete it after #241 merges.
- Local worktrees: `.worktrees/wireframe-ui`, `.worktrees/active-orders-filter` and `.worktrees/shots-branch`. `.worktrees/` is excluded through `.git/info/exclude`, not `.gitignore`.

## Done

- **Shell:** a sidebar for signed-in users (navigation, credits, Make a request, Ongoing, Sign out). Below 60rem it becomes a drawer behind a menu button. Ongoing lists `ListMyOrders(active_only)` with a role icon and the caller's next step.
- **Home:** a Leaflet/OpenStreetMap map of suppliers or open requests, an open-requests list that draws pickup and destination, and a request-delivery panel with search, recent suppliers and all suppliers.
- **Suppliers** and **Deliver a request:** a list beside the selected item, with the selection in the URL (`?selected=`). Phones show one pane at a time.
- **Make a request:** only the `CreateOrder` fields, grouped by what, where, and reward and timing, beside a route map. `?pickup=` pre-fills the pickup.
- **Order status:** details, a five-step progress list, and a reward panel. Once delivery is late, the panel shows a payout graph that mirrors Credit's lateness rule. It also has a Telegram chat button and a proof-of-delivery dialog for the courier.
- **My orders:** Active (`active_only`) and All tabs, role chips, and compact rows linking to the order page.
- **Profile:** name first, then Credits (available, held, and `ListMyLedgerEntries` history), recent requests, account details and Telegram.

All Devin review threads on both PRs are answered and resolved.

## Not done

1. **Onboarding:** put Connect Telegram on the registration screen, next to name and phone, instead of the separate `/welcome/telegram` page. Keep it optional. The profile picture waits on #234.
2. **Login:** restyle with the logo. The magic-link flow already matches the wireframe.
3. **Drawer focus:** return focus to the menu button when the phone drawer closes.
4. **Real-stack test (most important):** every check so far used mocked Connect responses. Run Compose from the worktree and walk through these flows:
   - create, with and without photos, and from a supplier's Request link;
   - accept, counteroffer, and accept an offer;
   - pickup, then late delivery with proof (check the payout graph);
   - confirm receipt, cancel and withdraw;
   - suspended user.

   Also check real supplier coordinates on the maps and set `PUBLIC_TELEGRAM_BOT_URL`.
5. Before marking #241 ready: refresh the before and after screenshots in the PR body, and remove anything from this note that has landed.

## Waiting on other work

| Wireframe feature | Current UI | Tracked in |
|---|---|---|
| Reviews and Add review dialog | Not shown | #232, needs #155 and #156 |
| Minimum courier stars | Not in the form | #233 |
| Profile pictures | Not shown | #234 |
| Proximity sort | Sorts only the loaded page | #235 |
| Favourite suppliers | Recent suppliers from order history | #236 |
| Per-order Telegram chat link | Chat opens the bot; the user sends `/chat` | #237 (deferred) |
| Bot tells users to contact each other through the app | Unchanged; the app has no way to do that | #243 |
| Payout calculated in Order | Frontend mirrors Credit's rule | #206 |

Dropped by the product owner: viewing other users' profiles. Chat uses the Telegram bot relay; the app never shows another user's Telegram username.

## Design philosophy

**Follow the wireframe's structure, not its placeholders.** The wireframe decides layout and navigation. Its fields are sketches. Forms use only what the contracts accept (`CreateOrderRequest`, `ListMyOrders`, `ListMyLedgerEntries`). A wireframe feature with no backend support gets an issue and a visible stub, never a fake control.

**Keep the existing behavior and replace only the layout.** Submission retries, idempotency keys, saved submissions, offer retry windows, cancellation confirmation and proof upload are hard-won, and their logic is unchanged. The redesign moved these components and restyled them. Each layout change was checked against the flows it touches.

**Each screen has one job, and its primary action is obvious.** Home answers "what's near me". Deliver a request is for choosing a request to take. The order page shows where an order stands and what to do next. Secondary actions are folded away, for example cancellation behind "Cancel order…".

**Say what happens next, in the user's terms.** Ongoing says "Pick it up" or "Confirm you received it", not raw status names. Credit history says "Paid to your courier" and "holds 15", not ledger kinds. Labels never claim more than the data shows, which is why former couriers are labelled "Former courier" rather than "Withdrew".

**Few words, few decorations.** I dropped all-caps labels above headings, dot-joined meta strings, decorative cards and explanatory paragraphs where the control already says it. The map is the one prominent visual; everything around it is plain M3 surfaces, lists and text. The product owner trimmed copy twice, so keep that bias.

**The URL is the state.** Selections, tabs, filters and pages live in query parameters. Back, refresh and shared links work, and phones can show one pane at a time from the same URL.

**The server is authoritative, and the UI only estimates.** The payout graph mirrors Credit's rule exactly, in integer nanoseconds, and is tested against Credit's own `TestPayable` cases. It is still labelled "estimated" until the order completes. Filtering that affects pagination belongs on the server: `active_only` excludes withdrawn assignments in SQL, not in the browser.

**Treat user text as untrusted everywhere, including map libraries.** Leaflet renders string tooltips as HTML, so labels are passed as text nodes.

**Follow repository conventions, and let reviews tighten them.** The conventions require Superforms, m3-svelte components, no styling of M3 internals, and route-only components beside their route. For compact fields, the app-owned `--foc-field-shrink` variable feeds the `--m3-density()` function in `app.css`, instead of overriding `.m3-container`.

**Verify in a browser, not just with the type checker.** Several real bugs only showed up in screenshots or Playwright checks: a refetch loop, an empty map, and a stale panel. Prefer a check that would fail on the old code. The tooltip fix was confirmed that way.

## Gotchas

- **Svelte 5 effects:** an effect that reads state it also writes loops. The request panel did this and refetched forever. Read such values with `untrack`, and drive fetches with explicit revision counters.
- **Nested forms:** `LocationPicker` sits inside other forms, so its search is a `div` that handles Enter, not a `<form>`.
- **OpenStreetMap tiles:** the public tile server rate-limits repeated automated loads (HTTP 429) and the maps turn grey. For screenshots, cache tiles or block them. Production traffic needs a tile provider; swapping one only changes the URL in `CampusMap.svelte`.
- **Tooling scripts are not committed:** the Playwright screenshot and check scripts are in `/tmp/foc-shots` (`shots.mjs`, `verify.mjs`, `handlers.mjs` with mock data). Move them into the repo if they're worth keeping.
- **Pushes:** `git push` over SSH sometimes printed `Permission denied (publickey)` but still updated the branch. Compare the branch on GitHub with the local head before retrying.
- **Agent shell:** the agent shell is fish. Heredocs don't work, and backticks inside double-quoted Python one-liners pick up stray backslashes. Write reply bodies to files first.
