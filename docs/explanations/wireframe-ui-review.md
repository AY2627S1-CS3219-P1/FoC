# PR #241 feedback audit

Audited 4 November 2026 against PR head `c18c582` and the completed local UI work. Fetched all 30 review threads (including nested replies), 51 review submissions and zero conversation comments; GitHub reports no further pages. All threads were marked resolved before this audit. Two automatic resolutions described unrelated changes, so their original findings were checked and fixed.

The committed browser check reproduces both failures on the prior code: no Recent section for pickups outside the first supplier page, and no sign-in link on anonymous My orders. Both pass with the fixes. It also checks ordinary/archived/unavailable pickup handling and preserves the onboarding, drawer-focus and proof-selection regressions.

| Review finding | Assessment against current code |
| --- | --- |
| [Withdrawn couriers see active delivery instructions](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4464779144) | Already addressed: server-side active_only filters current participation before pagination (#231). |
| [Recent suppliers vanish outside first page](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4464779264) | Valid; fixed in this follow-up: fetch recent pickup IDs outside the loaded supplier page, retain ordinary pickups, label archived ones without a Request action. |
| [Ongoing list hides older active orders](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4464779375) | Already addressed: additional pages expose See all your orders. |
| [Home-only map lives in shared components](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4464779618) | Does not apply: CampusMap is consumed by Home, Suppliers, request creation and the request route map. |
| [Sidebar navigation depends on internal M3 class](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4464779817) | Already addressed: navigation uses app-owned nav-link elements. |
| [Request link does not preselect pickup](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4464779927) | Already addressed: request creation reads and resolves the pickup query parameter; verified with real Supplier links. |
| [Order descriptions inject map tooltip HTML](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4464780101) | Already addressed: Leaflet receives a span populated with textContent. |
| [Wrong request remains actionable during selection](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4464905093) | Already addressed: a new unlisted selected ID clears the previous selection while its read is pending. |
| [Add photos cannot open file picker](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4464905215) | Invalid: M3 Button with label renders a label wrapping the input; real photo creation and the filechooser check succeeded. |
| [Cleared pickup still filters requests](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4464905342) | Already addressed: Change/Clear call onclear, removing pickup from the URL. |
| [Unresolvable pickup stays loading forever](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4464905543) | Already addressed: a failed prefilled pickup lookup displays an error and Clear. |
| [M3 internal selectors violate styling convention](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4464905666) | Already addressed: compact location fields use a wrapper and the app-owned density variable. |
| [Single-route map placed in shared library](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4464905883) | Already addressed: OrderRouteMap lives beside the orders page. |
| [Browse panel also displays closed requests](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4464905982) | Intentional: direct links show current status even after a request closes; acceptance remains guarded by status and expiry. |
| [Supplier map mirrors the current page](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4464906126) | Intentional: the supplier list and its map share the same filtered page. |
| [Route preview uses building center](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4464906266) | Intentional: the pre-assignment destination is the building centre, preserving exact-location privacy; the dashed line is not presented as a walking route. |
| [Delivery window hidden from prospective couriers](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4464972508) | Already addressed: pre-pickup delivery duration is independent of the expiry countdown. |
| [Delivered payout falsely appears to decline](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4464972690) | Already addressed: deliveredAt freezes the estimate and the false declining-payout caption was removed. |
| [Single-route components placed in shared library](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4464972874) | Already addressed: OrderProgress and PayoutChart live beside the sole consuming route. |
| [Progress dates mislabel today across time zones](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4464973021) | Already addressed: calendar-day comparison and time formatting both use Asia/Singapore. |
| [Delivered payout remains an estimate](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4464973201) | Already addressed: the amount is labelled estimated until Completed. |
| [Projected payout uses reduced time precision](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4464973419) | Already addressed: payout arithmetic uses integer nanoseconds; tests mirror Credit boundary cases. |
| [Selected request stays stale after refresh](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4465491809) | Already addressed: Refresh increments the revision observed by the selected-request read. |
| [Listed request cannot recover from detail error](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4465492061) | Already addressed: the recovery effect uses a later listed copy when the direct read failed. |
| [Chat guidance disappears without bot URL](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4465556610) | Declined per existing product decision: keep the chat button conditional on the configured bot URL; do not restore the removed /chat paragraphs. |
| [Info: Payout deadline now relies on the graph](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4465556847) | Already addressed: final graph tick and its accessible label explain the zero-payout point. |
| [Ongoing requests hidden on later pages](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4465906813) | Already addressed: backend active_only filters prior assignments before paging; See all remains outside the nonempty branch. |
| [Signed-out visitors see empty order history](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4466483353) | Valid; fixed in this follow-up: signed-out My orders renders a working Sign in link, without a page-level auth redirect. |
| [Active courier requests appear delivered](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4466717549) | Already addressed: both histories use the neutral Requested/Courier/Former courier role labels. |
| [Removed couriers appear to have withdrawn](https://github.com/AY2627S1-CS3219-P1/FoC/pull/241#discussion_r4466998638) | Already addressed: Former courier does not infer withdrawal versus admin unassignment. |


## Follow-up on 8 November 2026

Checked all 34 threads, 58 review submissions and four conversation comments;
GitHub reports no additional pages or nested replies. The PR now targets main
after #231 merged. Three threads remained open:

| Feedback | Disposition |
| --- | --- |
| Verification image links | Replaced the five missing local image paths with immutable URLs on published screenshot commit `55505e0`; all five returned HTTP 200. |
| Drawer / map-switch overlap | Confirmed with a browser screenshot regression. Page content now creates its own stacking context, keeping map switches and Leaflet controls below the drawer and scrim. The regression failed before the fix and passes afterward. |
| Previous / Next scrolling | Pagination now uses normal navigation scroll/focus reset. Search/filter changes retain scroll and focus. Both pagination directions are exercised against a long list. |
| Supplier filter icon suggestion | Replaced the rotating chevron with the existing Material filter-list icon. The control exposes Show/Hide filters and its expanded state. |
| Open-order live updates / SSE discussion | Kept as a separate follow-up, as proposed in the conversation; this patch does not add a streaming API or change refresh behavior. |

`npm run check`, all 221 unit tests, the production build and the complete
`test:wireframe` browser suite passed. Browser checks use mocked Connect
responses and blocked map tiles. Set `WIREFRAME_SCREENSHOT_DIR` to retain the
fixed drawer/filter captures from that suite.
