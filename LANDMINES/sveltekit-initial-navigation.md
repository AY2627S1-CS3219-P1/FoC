# Initial navigation callbacks precede shallow-routing readiness

**Applies when:** frontend code calls SvelteKit `pushState` or `replaceState`
during initial hydration, including an initial `afterNavigate` callback.

**Symptom:** opening `/login?token=...` directly leaves the page on Signing you
in and raises `Cannot call replaceState(...) before router is initialized`.
Registration token handling is affected by the same pattern.

**Reproduced:** rendering authentication routes during root session restoration
makes the login/register page part of initial hydration. Calling `replaceState`
directly from `onMount` reproduced the failure. Moving it directly into
`afterNavigate` also reproduced it in Chromium against the installed SvelteKit.

**Cause:** in `@sveltejs/kit/src/runtime/client/client.js`, initial hydration
invokes `after_navigate_callbacks` before assigning `started = true`, while
`replaceState` checks `started` in development. An initial callback alone is
therefore insufficient to establish shallow-routing readiness.

**Workaround implemented:** login/register use an async `afterNavigate` callback
and await its `complete` promise before removing the token. The initial promise
is resolved; awaiting it yields until initialization finishes. A teardown guard
prevents a callback from acting after the page is destroyed. Forms stay disabled
until their client handlers are ready. No upstream dependency is patched.

**Verification:** in a fresh browser context, open `/login?token=...` and
`/register?token=...` directly. Check that tokens are removed, login/registration
remain usable, and no router-initialization error occurs. Also test failed
session refresh; authentication pages must stay available.

**Remove when:** the pinned SvelteKit initialization marks shallow routing ready
before initial callbacks, and the direct token-entry cases pass without waiting
for navigation completion.
