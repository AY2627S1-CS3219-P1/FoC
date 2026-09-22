# Supplier Service — Requirements

Source: `Group 1 Product Backlog Document.pdf` §1 Functional Requirements (Supplier Service: S1–S3).
Scope: owned by `supplier-service/`.

## S1 — Campus Location / Supplier Info

| ID | Requirement | Priority | Planned |
|----|-------------|----------|---------|
| S1 | Provide campus location information, including suppliers. | — | — |
| S1.1 | Allow users to search suppliers or locations by keyword (e.g. "super snack"). | High | Week 6 |
| S1.2 | Allow users to filter suppliers or locations by campus region (PGP, COM, Science, UTown). | Medium | Recess Week |
| S1.3 | Allow users to sort suppliers or locations by campus region. | Medium | Week 6 |
| S1.4 | Provide information or instructions for purchasing items remotely from a supplier. | Low | Week 7 |
| S1.5 | A supplier is represented as a location that can supply items. | High | Week 6 |
| S1.6 | A supplier record contains zero or more location photos. | Low | Week 7 |
| S1.7 | A supplier record contains supplier contact information. | Low | Week 7 |
| S1.8 | A supplier record contains map coordinates. | Medium | Week 10 |
| S1.9 | Start with a list of locations. | High | Week 6 |
| S1.9.1 | Initial list includes the supplier and location data supplied with the project. | High | Week 6 |
| S1.9.2 | Initial list includes additional campus locations added by the team. | High | Week 6 |

## S2 — Supplier Administration

| ID | Requirement | Priority | Planned |
|----|-------------|----------|---------|
| S2 | Support supplier administration. | — | — |
| S2.1 | Allow admins to manage suppliers. | — | — |
| S2.1.1 | Allow an admin to create a supplier. | Medium | Recess Week |
| S2.1.2 | Allow an admin to view all suppliers. | Medium | Recess Week |
| S2.1.3 | Allow an admin to view a supplier in detail. | High | Week 6 |
| S2.1.4 | Allow an admin to update a supplier record. | Medium | Recess Week |
| S2.1.5 | Allow an admin to archive a supplier. Archive = prevent user from selecting this supplier when making a delivery request. | Medium | Recess Week |
| S2.1.6 | Allow an admin to unarchive a supplier. | Medium | Recess Week |
| S2.1.7 | Reject deletion of a supplier referenced by a request. | Medium | Recess Week |
| S2.2 | Allow an admin to configure opening and closing hours (display hours on supplier detail page; user can still select a closed supplier). | Low | Week 7 |
| S2.3 | Allow admins to disable suppliers. Disable = warn user closed/not operating, but still allow selection. | — | — |
| S2.3.1 | Allow an admin to disable an enabled supplier immediately. | Low | Week 7 |
| S2.3.2 | Allow an admin to enable a disabled supplier immediately. | Low | Week 7 |
| S2.3.3 | Allow an admin to schedule when a supplier is disabled. | Low | Week 7 |
| S2.3.4 | Re-enable a supplier when its scheduled disablement ends. | Low | Week 7 |
| S2.3.5 | When a supplier is disabled, warn users that it is not in operation. | Low | Week 7 |
| S2.4 | Allow a user to request the addition of a supplier. Suspended users cannot (U6.6). | Low | Week 7 |
| S2.4.1 | Allow admin to approve a supplier addition request. | Low | Week 7 |
| S2.4.2 | Allow admin to reject a supplier addition request. | Low | Week 7 |
| S2.4.3 | Allow admin to edit a supplier addition request before approval. | Low | Week 7 |

## S3 — Map of Suppliers and Open Requests

| ID | Requirement | Priority | Planned |
|----|-------------|----------|---------|
| S3 | Provide a map of supplier locations and open requests. | — | — |
| S3.1 | Map displays supplier locations. | Medium | Week 10 |
| S3.2 | Map displays open requests. | Medium | Week 10 |
| S3.3 | Before acceptance, an open-request marker shows the named approximate pickup area. | Medium | Week 10 |
| S3.4 | Before acceptance, an open-request marker shows the named approximate destination area. | Medium | Week 10 |
| S3.5 | Selecting an open request in a list centres the map on that request's approximate areas. | Medium | Week 10 |
| S3.6 | Centre the map on the selected supplier when users select a supplier search result. | Medium | Week 10 |
| S3.7 | Allow users to toggle the supplier layer (layer = map layer showing all suppliers). | Medium | Week 10 |
| S3.8 | Allow users to toggle the open-request layer. | Medium | Week 10 |

Privacy: exact delivery locations stay hidden pre-acceptance (NFR-06.2); only approximate areas on map.

## Relevant NFRs

- NFR-01 (95% sync API < 500ms under 10k concurrent; read/search workload) — supplier search is read-heavy.
- NFR-03 / NFR-03.1 (map failure must not block core request/credit ops; show unavailable state).
- NFR-05.2 (auth + role/relationship authorization on admin ops).
- NFR-06.2 (exact locations restricted), NFR-13 (invalid-input / action-failure feedback).
