# PR #241 screenshots

Refreshed on 4 November 2026.

- `before-*`: baseline checkout `422bb11`, mocked Connect responses.
- `after-*`: wireframe worktree based on `c18c582`, with the local onboarding,
  login wordmark, drawer focus and proof-preservation changes. These local
  implementation changes had not been pushed to the PR branch at capture time.
- Before/after comparisons use the same mocked order and supplier fixtures.
  Map tiles come only from an existing local cache; unavailable tiles are blocked.
- `real-*`: actual Compose services, seeded development accounts/suppliers,
  Mailpit and Floci. No live Telegram bot was configured. The late-payout image
  precedes final proof submission; the completed order settled at 13 credits,
  matching the frontend estimate at the eventual delivery time.

The branch is screenshot hosting only and can be deleted after PR #241 merges.

## Review follow-up, 8 November 2026

`review-drawer-fixed.png` and `review-supplier-filters.png` show the changes in
PR commit `b9172917`, using mocked Connect responses and blocked map tiles.
The drawer capture verifies that map controls stay behind the drawer and scrim.
