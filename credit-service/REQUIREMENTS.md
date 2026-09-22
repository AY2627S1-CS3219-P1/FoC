# Credit Service — Requirements

Source: `Group 1 Product Backlog Document.pdf` §1 Functional Requirements (Credit Service: C1).
Scope: owned by `credit-service/`. Closed-credit economy: no purchase/withdrawal/exchange; credits circulate internally only.

## C1 — Closed-Credit Economy

### C1.1 Balance Checks

| ID | Requirement | Priority | Planned |
|----|-------------|----------|---------|
| C1.1 | Check the requester's available credit balance before reserving credits. | — | — |
| C1.1.1 | A reservation is created only if it does not exceed available balance (available excludes reserved credits). | High | Week 9 |
| C1.1.2 | Ensure available credits never become negative. | High | Week 9 |

### C1.2 Reserve, Pay, Return

| ID | Requirement | Priority | Planned |
|----|-------------|----------|---------|
| C1.2 | Reserve the request reward and later pay or return those credits according to the request outcome. Credits are checked/reserved at creation, and checked/reserved again upon counteroffer. | — | — |
| C1.2.2 | A courier counteroffer may exceed the requester's available balance, but the requester must not be able to accept a counteroffer exceeding available credits. | High | Week 9 |
| C1.2.3 | If a request fails after reservation, return the reserved amount to the requester. | High | Week 9 |
| C1.2.4 | For delivery within the allowed period, payable courier reward equals the reserved reward. | High | Week 9 |
| C1.2.5 | After the allowed delivery period, calculate payable reward from the configured lateness rule. | Low | Week 10 |
| C1.2.6 | The requester's final cost equals the courier's payable reward. | Low | Week 10 |
| C1.2.7 | At twice the allowed delivery duration, payable courier reward becomes zero. | Low | Week 10 |
| C1.2.8 | When a request becomes Completed, transfer its payable reward to the courier. | High | Week 9 |
| C1.2.9 | After transferring the payable reward, return the unused reservation to the requester. | High | Week 9 |
| C1.2.10 | Expiry before acceptance returns the reservation to the requester. | High | Week 9 |
| C1.2.11 | Cancellation before pickup returns the reservation to the requester. | High | Week 9 |
| C1.2.12 | Requester cancellation after pickup transfers the reserved reward to the courier. | High | Week 9 |
| C1.2.13 | When a request becomes Failed, return the reserved credits to the requester. | High | Week 10 |
| C1.2.14 | Credits for a request are paid or returned at most once. | High | Week 9 |
| C1.2.15 | Opening a report or appeal must not reverse or repeat a completed request settlement. | High | Week 10 |

Mapping to Order outcomes: Completed → C1.2.8+C1.2.9; Expired → C1.2.10; Cancelled pre-pickup → C1.2.11; requester-cancel post-pickup → C1.2.12; Failed → C1.2.13. Failed reservation → request never opens (R1.1.9).

### C1.3 Initial Allocation

| ID | Requirement | Priority | Planned |
|----|-------------|----------|---------|
| C1.3 | Allocate users an initial credit amount (100) upon successful registration. | — | — |
| C1.3.1 | Successful registration allocates exactly 100 initial credits at most once. | High | Week 9 |

Triggered by User Service registration (U1).

## Relevant NFRs

- NFR-04 / NFR-04.2 (idempotent credit events; no duplicate balance changes), NFR-20 (pub-sub with Order Service for settlement).
- NFR-09.5–09.7 (detect Order/Credit mismatches within 5 min of recovery; reconciliation checks at startup and ≥ every 5 min).
- NFR-10.3 / NFR-10.3.1 / NFR-10.3.2 / NFR-10.4 (audit credit settlement + admin adjustments; alert admin on unexplained mismatch).
- NFR-11.7 / NFR-11.18 / NFR-11.19 (tests: repeated credit events, async terminal-outcome settlement, Credit downtime without losing pending settlement).
