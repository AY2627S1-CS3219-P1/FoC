# User Service — Requirements

Source: `Group 1 Product Backlog Document.pdf` §1 Functional Requirements (User Service: U1–U7).
Scope: owned by `user-service/`. Cross-service effects noted inline.

## U1 — Registration

| ID | Requirement | Priority | Planned |
|----|-------------|----------|---------|
| U1 | Allow users to register an account. | — | — |
| U1.1 | Allow a user to register using an emailed registration link. | High | Week 6 |
| U1.1.1 | Reject a syntactically invalid registration email address. | High | Week 6 |
| U1.1.2 | Restrict registration only to emails within domain(s) whitelisted by admin. | Medium | Recess Week |
| U1.2 | Validate a registration link before completing registration. | — | — |
| U1.2.1 | Send a registration link to the submitted email address. | High | Week 6 |
| U1.2.2 | Invalidate a registration link 10 minutes after it is sent. | High | Week 6 |
| U1.2.3 | Allow users to set up their profile upon successful registration. | High | Week 6 |
| U1.2.4 | Invalidate a registration link after its first successful use. | High | Week 6 |
| U1.2.5 | Reject an invalid registration link. | High | Week 6 |
| U1.2.6 | Reject an expired registration link. | High | Week 6 |
| U1.2.7 | Reject a previously used registration link. | High | Week 6 |
| U1.2.8 | After rejecting a registration link, state that registration verification failed. | High | Week 6 |
| U1.3 | Reject registration and redirect to login when email is already associated with an account. | High | Week 6 |
| U1.4 | Require a Telegram handle or phone number during onboarding. Onboarding = first-login required info fill. | Medium | Week 10 |

## U2 — Email Login / Logout

| ID | Requirement | Priority | Planned |
|----|-------------|----------|---------|
| U2 | Allow users to login using email. | High | Week 6 |
| U2.1 | Send a login link to the user's verified email. | High | Week 6 |
| U2.1.1 | Log a user in using a valid login link. | High | Week 6 |
| U2.1.2 | A login link remains valid for 10 minutes after issuance. | High | Week 6 |
| U2.1.3 | Reject a previously used login link. | High | Week 6 |
| U2.1.4 | Reject a malformed login link. | High | Week 6 |
| U2.1.5 | Reject an expired login link (10 minutes elapsed since generation). | High | Week 6 |
| U2.1.6 | After rejecting a login link, state that login failed. | High | Week 6 |
| U2.1.7 | Issuing another login link must not invalidate an older outstanding link. | High | Week 6 |
| U2.2 | Allow an authenticated user to log out. | High | Week 6 |
| U2.2.1 | Provide the option to log out of all signed-in devices. | High | Week 6 |

## U3 — Profiles and Favourites

| ID | Requirement | Priority | Planned |
|----|-------------|----------|---------|
| U3 | Allow users to manage their profiles and favorites. | — | — |
| U3.1 | Allow user to manage profile picture. | Low | Week 7 |
| U3.1.1 | Give user a default profile picture. | Low | Week 7 |
| U3.1.2 | Allow user to upload / add profile picture. | Low | Week 7 |
| U3.1.3 | Allow user to replace profile picture. | Low | Week 7 |
| U3.1.4 | Allow user to remove profile picture. | Low | Week 7 |
| U3.2 | Allow users to view another user's profile. | Low | Week 7 |
| U3.2.1 | Viewed profile shows the user's name. | Low | Week 7 |
| U3.2.2 | Viewed profile shows the user's profile picture. | Low | Week 7 |
| U3.2.3 | Viewed profile shows the user's rating. | Low | Week 7 |
| U3.3 | Allow users to change the description in their profile. | Low | Week 7 |
| U3.4 | Allow users to save a list of favourite suppliers. | Low | Week 7 |
| U3.4.1 | Allow a user to add a supplier to favourites. | Low | Week 7 |
| U3.4.2 | Allow a user to remove a supplier from favourites. | Low | Week 7 |
| U3.5 | Allow a user to view the user's own profile. | Low | Week 7 |
| U3.5.1 | Own profile shows display name. | Low | Week 7 |
| U3.5.2 | Own profile shows profile picture. | Low | Week 7 |
| U3.5.3 | Own profile shows description. | Low | Week 7 |
| U3.5.4 | Own profile shows aggregate rating. | Low | Week 7 |
| U3.5.5 | Own profile shows review count. | Low | Week 7 |
| U3.5.6 | Own profile shows registered email address. | Low | Week 7 |
| U3.6 | Allow a user to manage the user's display name. | Low | Week 7 |

Note: aggregate rating / review count are computed by Order Service (R3.6, R3.7); User Service displays them.

## U4 — Roles

| ID | Requirement | Priority | Planned |
|----|-------------|----------|---------|
| U4 | Allow admin to manage user roles. | — | — |
| U4.1 | Allow admin to assign different abilities to different roles. | High | Week 6 |
| U4.1.1 | Allow admin to manage normal users' (requester and courier) abilities. | High | Week 6 |
| U4.2 | Allow users to sign up as system admin on initialization when no existing admin is present. | High | Week 6 |

## U5 — Dual Requester / Courier

| ID | Requirement | Priority | Planned |
|----|-------------|----------|---------|
| U5 | Allow one account to participate as both requester and courier. | High | Week 9 |
| U5.1 | Allow one account to have active requester requests and courier assignments concurrently. | High | Week 9 |

## U6 — Suspension and Reinstatement

| ID | Requirement | Priority | Planned |
|----|-------------|----------|---------|
| U6 | Support account suspension and reinstatement. | — | — |
| U6.1 | Allow an admin to suspend a user account. | Low | Week 10 |
| U6.2 | Require the admin to provide a suspension reason. | Low | Week 10 |
| U6.3 | Disallow a suspended user to create a request. | Low | Week 10 |
| U6.4 | A suspended user is unable to accept a request. | Low | Week 10 |
| U6.5 | A suspended user is unable to submit a review. | Low | Week 10 |
| U6.6 | A suspended user is unable to submit a supplier-addition request. | Low | Week 10 |
| U6.7 | A suspended user retains access to order history. | Low | Week 10 |
| U6.8 | A suspended user retains access to existing reports and appeals. | Low | Week 10 |
| U6.8.1 | Retain access to responses and evidence (image and other attachments) required by an existing report or appeal. | Low | Week 10 |
| U6.9 | Allow an admin to reinstate a suspended user account. | Low | Week 10 |
| U6.9.1 | Require the admin to provide a reinstatement reason. | Low | Week 10 |
| U6.9.2 | Reinstatement restores the user's ordinary abilities. | Low | Week 10 |

Enforcement is shared: Order Service must block create/accept/review (R2, R1.6.12), Supplier Service must block supplier-addition requests.

## U7 — Account Warnings

| ID | Requirement | Priority | Planned |
|----|-------------|----------|---------|
| U7 | Manage account warnings. | — | — |
| U7.1 | Record each warning against the affected account. | Low | Week 10 |
| U7.2 | A warning identifies its originating request. | Low | Week 10 |
| U7.3 | A warning contains a reason. | Low | Week 10 |
| U7.4 | Allow the affected user to view the warning. | Low | Week 10 |
| U7.5 | Retain a warning when its appeal outcome is Upheld. | Low | Week 10 |
| U7.6 | Mark a warning as removed when its appeal outcome is Overturned. | Low | Week 10 |

Producers: Order Service requests warnings (R1.4.1, R1.4.2); Report Service requests warnings (RP5.2). Appeals resolved via Report Service (RP12.1, RP12.2).

## Relevant NFRs

- NFR-05.2.1/05.2.2/05.2.4, NFR-05.4 (auth, session, Secure/HttpOnly/SameSite cookies, suspension revokes sessions within 1 min).
- NFR-06 / NFR-06.1 / NFR-06.6 / NFR-06.10 (public profile exposes only name, picture, description; Telegram handle not visible to another user; no credentials/tokens in logs).
- NFR-10.3 / NFR-10.3.1 / NFR-10.3.2 (audit: suspension, reinstatement, warning removal).
- NFR-18.4 (logs retained 30 days).
