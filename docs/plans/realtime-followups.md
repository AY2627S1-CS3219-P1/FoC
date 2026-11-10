# Live-update follow-ups

The first release covers credits, orders, offers, and their background
operations. These are intended follow-ups, not implemented features. Reuse
domain publication, Communication's stream, and authorized read APIs.

| Priority | Owner | Triggers | Views | Dependencies and acceptance |
| --- | --- | --- | --- | --- |
| Medium | Supplier | Location create/edit/archive/restore | Directory/map, details, admin lists, new-order pickers | Reflect another session's changes while preserving filters/drafts and existing orders' location snapshots. |
| Medium | Supplier | Disablement create/edit/end/cancel | Availability notices, details, pickers, admin intervals | Also refresh at scheduled start/end boundaries: time can change availability without a write. |
| Medium | Supplier | Addition request submit/edit/withdraw/approve/reject | Admin queue/detail; directory after approval | Preserve review drafts and require fresh confirmation after revision changes. No user-facing submission screen currently exists. |
| Medium | Communication | Telegram link redeemed/replaced/unlinked or removed after blocking | Onboarding/profile Telegram card | Replace waiting polls; cover bot-originated changes, another tab, and chat reassignment. |
| Medium | User | Role change, suspension, reinstatement | Account details, navigation, admin user views | Coordinate with centralized token revocation/refresh. Hints must never enforce authorization. |
| Low | User | Profile edit | Other tabs/devices showing the profile | Never silently overwrite a dirty form. |
| Low | User | Allowed email domain added/removed | Admin configuration | Login/refresh authorization stays server-owned. |
| Later | User/review | Review submitted/removed | Review/moderation lists | Backend exists; review UI has not been implemented. |

For each addition, record events at shared transaction boundaries so RPCs,
consumers, workers, and administrative actions have the same coverage. Define
recipients through the owning service's visibility rules. Keep browser payloads
limited to scopes, and retain reconnect/focus/fallback reads.
