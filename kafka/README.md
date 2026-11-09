# Kafka contracts

Kafka message contracts live here, separately from the Connect RPC contracts
under `proto/`. Files use `<service>/events/v1/<topic>.proto`, with package
`<service>.events.v1`. The service is the producer/owner. Use `.proto`, and
declare messages and enums without RPC services.

| Topic | Value message | Key |
| --- | --- | --- |
| `order.status_changed` | `order.events.v1.OrderStatusChanged` | Order UUID as UTF-8 text |
| `order.outcome` | `order.events.v1.OrderOutcome` | Order UUID as UTF-8 text |
| `order.courier_warning_requested` | `order.events.v1.CourierWarningRequested` | Order UUID as UTF-8 text |
| `order.offer_made` | `order.events.v1.OrderOfferMade` | Order UUID as UTF-8 text |
| `users.create` | `users.events.v1.UserCreated` | User UUID as UTF-8 text |
| `users.review_submitted` | `users.events.v1.ReviewSubmitted` | Review UUID as UTF-8 text |
| `moderation.review_moderation_finished` | `moderation.events.v1.ReviewModerationFinished` | Review UUID as UTF-8 text |
| `credit.account_changed` | `credit.events.v1.AccountChanged` | User UUID as UTF-8 text |
| `order.changed` | `order.events.v1.OrderChanged` | Order UUID as UTF-8 text |

Credit publishes account changes with each committed nonzero ledger posting.
Order publishes read-model changes through its existing outbox, including offers
and workflow-only changes. Communication's separate `communication-realtime`
group turns these events into advisory browser invalidations. Duplicates are
safe; do not deduplicate Order changes solely by aggregate revision. See
[the live-update ADR](../docs/adr/realtime-invalidation.md).
