# ADR: Pagination — shared offset model, strategy chosen per endpoint

Date: 17 Oct 2026
Status: accepted
PRs affected: user-service review feature (R3 list endpoints)

## Intent

1. Every list endpoint needs pagination, but endpoints have different access
   patterns. The shared value lives in the common pieces — defaults, bounds,
   offset math, total-page math, validation — while the *strategy* and *wire
   shape* belong at the endpoint.
2. Supplier-service already uses offset pagination (`page`/`page_size` +
   `PageInfo`) without a shared type. User-service's review list should follow
   that shape but own its pagination as one shared, self-contained type.
3. `page` and `offset` are a bijection given `page_size`; `total_pages` is
   derivable from `total_items` and `page_size`. Carry one position field and
   the totals — nothing more on the wire.

## Decision

Shared offset pagination lives in `pkg/pagination` as one self-contained model:

- **Request model** carries `page` and `page_size`.
- It **computes and exposes its own `offset`** (`(page - 1) * page_size`) and
  **validates itself** — defaults (page 0 → 1, page_size 0 → 20), bounds
  (page_size maximum 100), and an offset-overflow guard — as model methods, not
  per-handler logic.
- **Response model** carries `page`, `page_size`, `total_pages`, `total_items`;
  `total_pages = ceil(total_items / page_size)`.
- `page` is the canonical wire field (supplier-service precedent); `offset` is
  computed server-side. `page` and `offset` are never both transmitted.

The proto `Pagination` request (`proto/user/review/v1/review.proto`) mirrors the
request model; list responses return a `ReviewPage` with the four response
fields. Supplier-service's `PageInfo` proto message is intentionally **not**
imported — it is supplier-internal and importing it would couple the two
services' contracts. The four response fields are defined locally in the same
shape.

### Strategy is chosen per endpoint, not forced

| | Offset / skip (`page`/`page_size` + totals) | Cursor / token |
|---|---|---|
| Best for | non-sequential access: admin tables, search, "jump to page N" | infinite scroll, prev/next, unbounded feeds |
| Volume | bounded / modest | unbounded / growing |
| Deep pages | fine at small offset; `OFFSET` is O(n) | always O(1) seek |
| Stable under writes | no | yes |
| Total count | usually yes (page controls) | usually no (COUNT is expensive at scale) |

Reviews use offset pagination (bounded per-user volume, admin UI wants page
controls). When an unbounded feed appears it should adopt the cursor pattern;
the package is structured so a second pattern can be added without disturbing
the offset one.

## Why

1. One shared type removes the duplicated normalization/validation/offset math
   that currently exists in each supplier subdomain, without dictating the
   strategy, which depends on volume, growth and access pattern.
2. Encapsulating offset and validation in the model keeps endpoints free of
   pagination plumbing and makes the offset/overflow behaviour testable in one
   place.
3. `page` over `offset` on the wire matches supplier-service and is the
   human-facing abstraction; offset stays an implementation detail.
4. `total_pages` is a trivial, cheap derivation that page controls render, so it
   is worth transmitting even though it is derivable.

## History

- 17 Oct 2026: accepted — shared offset model in `pkg/pagination`, strategy
  chosen per endpoint.
