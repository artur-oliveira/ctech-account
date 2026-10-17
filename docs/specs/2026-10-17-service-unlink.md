# Spec: Leaving one CTech product (service unlink, `scope: service`)

- **Status:** Draft (2026-10-17)
- **Projects:** `ctech-account` (api, ui, cdk config), `ctech-go-common` (`erasure`), and, by version bump only,
  `ctech-wallet`, `ctech-dfe`, `ctech-billing`, `ctech-poker`
- **Parent:** [Account deletion — overview](2026-10-06-account-deletion-overview.md) § 4 ("Two scopes, one mechanism"),
  [saga protocol](2026-10-06-account-deletion-saga-protocol.md) § 4.5 (`Store.Clear`)
- **Plan:** [`docs/plans/2026-10-17-service-unlink.md`](../plans/2026-10-17-service-unlink.md)

## 1. Outcome

A person can leave **one** CTech product (wallet, billing, dfe, poker) without closing their CTech account. That
product erases or anonymizes the person's own data (same treatment as full deletion, same retention rules), the person
stays signed in everywhere else, and they can come back to the product later as a brand-new user.

## 2. Decisions

| # | Decision | Why |
|---|---|---|
| U1 | **The unit is the person's own data in that service.** Workspaces (organizations, personal workspaces) are never erased by a service unlink: `workspaces[]` and `organizations[]` are empty in every message of this scope, and the ownership assessment does not run. | A workspace belongs to the account, not to a product. Erasing dfe's data of an organization because its only member left dfe would tombstone `ORG#{id}` in dfe, and the organization could never use dfe again. Whoever wants the workspace gone deletes the workspace, or the account. |
| U2 | **Blockers are the participant's own**, asked through the same eligibility route with no `workspaces` parameter. No account-local blockers apply (an owned OAuth client or a shared workspace has nothing to do with leaving wallet). | A blocker exists to stop us destroying something we must keep or that someone is owed. The participant is the only one who knows. |
| U3 | **The same lifecycle as account deletion**: e-mail confirmation, 7-day cancelable grace (D8), the same states and worker. Same anti-fraud (§ 4). | One mechanism, one set of tests. A stolen session must not be able to wipe someone's wallet history either. |
| U4 | **The cut-off is the participant's lock, not a global one.** At the start of grace account publishes `user.locked` with `scope: service`, `services: [svc]`. It does **not** mark the user pending, revoke sessions or API keys of other products, or write the `jwtverify` revocation entry (that list is keyed by `sub` alone and would sign the person out of every product). It revokes the refresh tokens the person holds for that product's login clients, and the API keys holding a scope of that product. | D1 says "the account and every participant are locked from the start of grace". For this scope the lock is the service's own `SUB#{sub}` lock (`Store.Blocked`), which the services already enforce on every write path. |
| U5 | **One open request per user, as today.** An open service request blocks starting another request (service or account) until it ends. | The `OPEN` marker is per user. Leaving two products at once is rare, and a queue costs a new concept. |
| U6 | **Coming back is explicit and published.** Account records the unlinked service on the user (`unlinked_services`). When the person next signs in to a login client of that service, account publishes `user.relinked`; the participant's consumer calls `Store.Clear(sub)` and the person is a new user there. Sign-in fails with 503 if the publish fails; nothing is cleared locally until it succeeds. | A tombstone is terminal by design (late events must not resurrect data). Re-consent is the only event that may reactivate it, and only account knows it happened. Failing closed means the person retries; failing open would let them in while the service still refuses them. |
| U7 | **`user.relinked` is a new message type in `api-commons/erasure`**, handled inside the shared `Consumer`. No service writes code for it, only bumps the module. | The consumer is shared on purpose (overview decision 7). |
| U8 | **Account's own data is untouched** except: the consent grants for that service's login clients are deleted when the request reaches `purged`, and the user's `unlinked_services` gains the service. No tombstone of the user: the user is not erased. | The account stays ACTIVE (overview § 4). |
| U9 | **Which products exist, and which OAuth clients belong to each,** comes from `ERASURE_PARTICIPANTS` (new optional field `login_clients`). The UI lists every configured participant; it does not infer "linked" from consents, because first-party clients skip the consent screen (`handler/authorize.go`) and leave no grant. Leaving a product the person never used acks `done` with empty counts. | A registry of links would be a new thing to keep right for no behavior the user can see. |
| U10 | **Billing's tenant-zero customer is the person, not a workspace.** "Leaving billing" cancels the person's subscriptions and anonymizes their customer (same as the account purge, minus the workspaces). | Reuses `Eraser.Purge` with an empty workspace list. |

## 3. Lifecycle

```
ACTIVE ──request(service=S)──▶ awaiting_confirmation ──e-mail link──▶ pending_deletion (lock at S)
   ▲                                                                         │ cancel → unlock at S
   │                                                             grace ends  ▼
   └─── account stays ACTIVE ◀────────── purged ◀── purging ◀── locked  (user.erase to S only)
```

- `Request.Scope` is `account` (today) or `service`; a service request also stores `Service`.
- States and timings are the existing ones. The differences are in what each transition does:

| Transition | `scope: account` (today) | `scope: service` |
|---|---|---|
| request | assess ownership + every participant | assess **only S**, no workspaces (U1, U2) |
| lock (start of grace) | mark user pending, revoke sessions, API keys, `jwtverify` entry, `user.locked` to all | `user.locked` to `[S]`; revoke refresh tokens of S's login clients and API keys with an S scope (U4) |
| cancel | unmark user, `user.unlocked` to all | `user.unlocked` to `[S]` |
| end of grace (`locked`) | re-check blockers, freeze workspaces, `user.erase` to all | re-check S's blockers, `user.erase` to `[S]`, `workspaces: []` |
| `purging` | wait for every participant's ack, then purge account data | wait for S's ack, then § 6 |
| sign-in | refused while pending | unaffected (the person is ACTIVE) |

## 4. Anti-fraud

Unchanged from the account request (account spec § 5): recent MFA or password, typed confirmation, e-mail confirmation
before anything happens. Two additions:

- The typed phrase for this scope is `SAIR DO <PRODUTO>` in capitals, e.g. `SAIR DO WALLET`, so a phrase learned for
  account deletion cannot be replayed into a service unlink.
- Every e-mail names the product and says plainly that the account itself stays.

## 5. Contract changes

### 5.1 `api-commons/erasure` (additive, message version stays 1)

- `TypeRelinked Type = "user.relinked"`.
- `Message.Validate` (or `Decode`) accepts it only with `Scope == ScopeService`, a single entry in `Services`, and no
  workspaces.
- `Consumer.handle`: for `TypeRelinked`, call `Store.Clear(ctx, m.Sub)` and return; no ack is sent, because nothing is
  owed to account.
- Rollout: a consumer built before this type exists rejects the message and it lands in the DLQ. **Every participant
  bumps `api-commons` before account sets `login_clients`** (which is what enables relink publishing).

### 5.2 ctech-account API

- `POST /v1.0/account/deletion` accepts `scope` (`account` default, `service`) and `service`. Blockers come back as the
  existing `deletion-blocked` problem. An unknown `service` is a 422.
- `GET /v1.0/account/deletion` reports `scope` and `service`, and per-service status as today.
- `GET /v1.0/account/deletion/services` returns the configured participants:
  `[{service, can_leave: bool}]`. `can_leave` is false while any request is open. Names and copy are the UI's.
- Messages published for this scope carry `Scope: service`, `Services: [S]`, empty `Workspaces`/`Organizations`.
- `config.ErasureParticipant` gains `LoginClients []string` (`json:"login_clients"`). Empty means that participant has
  no relink trigger and cannot be left (it is omitted from `/deletion/services`), which keeps the feature dark per
  participant.
- Scopes: the same `account:deletion:write` as the account request; no new scope.

### 5.3 Participants

No code. They bump `api-commons` (§ 5.1). Their `PurgeFunc` already receives `Message` with `Scope == service` and no
workspaces and does the person's own data only (U1). Each participant's plan states which of its steps depend on the
workspaces list; with an empty list those steps are no-ops.

## 6. What `purged` does for this scope (account side)

1. Delete the consent grants for S's login clients (`consent.Service.Revoke(user, client)`).
2. Add S to `User.UnlinkedServices` (a string set).
3. Audit event `account.service_unlinked` with `{service, request_id}`.
4. Release the `OPEN` marker.
5. Send the "you left {product}" e-mail.

Nothing else of the account is erased. `Purger.Purge` (account data) is **not** called.

## 7. Coming back (U6)

In the authorization-code exchange, after the user and client are resolved and before any token is minted:

```
for each S in user.UnlinkedServices where client ∈ S.login_clients:
    publish user.relinked {sub, request_id: new id, scope: service, services: [S]}   // fail ⇒ 503, mint nothing
    remove S from user.UnlinkedServices (conditional write)                          // fail ⇒ retry next sign-in
```

- It sits in the **code exchange**, not in `/authorize`, because exchange is the one place every sign-in to the client
  goes through, and the user record is already loaded there.
- Publishing before removing makes a crash between the two safe: the next sign-in publishes again, and
  `Store.Clear` is a no-op on a record that is not erased.
- Until the participant's consumer processes the message the product answers 403 `account-locked`; the UI of each
  product already shows that state. The delay is SQS latency (seconds).
- A late duplicate of the old `user.erase` after `Clear` is recognised as stale by the existing consumer logic
  (`apply`: active record newer than the erase). `TestStaleEraseAfterClear` in go-common pins it.

## 8. UI

- Settings → "Privacidade e dados" → "Sair de um produto": one card per entry of `/deletion/services`.
- The flow is the account flow (eligibility/blockers → consequences → identity check → typed phrase → "check your
  e-mail"), reusing its components. The consequences screen lists, from the inventory, what the product erases and
  what it retains, and says: "Sua conta CTech e os outros produtos continuam funcionando."
- The login page and the cancel/confirm pages already explain a pending account; service scope reuses the confirm and
  cancel pages (`/account-deletion/confirm`, `/account-deletion/cancel`) with the product named from the request.
- The profile status card shows "Saindo do {produto} até {data}" with a cancel link.

## 9. Retention and legal

Per product the treatment is the data inventory's "person's own data" rows, unchanged. A product that must keep
something under a legal basis (wallet ledger, KYC, issued fiscal documents) keeps it exactly as for full deletion,
segregated and with its expiry. The privacy policy already describes retention per product; it needs one sentence that
a person may leave a single product (version 3.4, same re-acceptance mechanics as 3.3). **Gate:** the owner approves the
sentence before the UI ships.

## 10. Out of scope

- Leaving a product while keeping some of its data (export-then-leave is the portability feature, a sibling).
- Leaving two products at once (U5).
- Erasing a workspace's data inside one product (U1).
- Restoring after `purged`: the person comes back as new (U6).

## 11. Rollout

1. `api-commons` with `TypeRelinked`; tag.
2. Participants bump `api-commons` and deploy (no behavior change; they can already handle `scope: service` messages).
3. Account ships the code dark: no participant has `login_clients`, so `/deletion/services` is empty.
4. Per product, one at a time, simplest first (poker → billing → dfe → wallet), set `login_clients` in the SSM
   `erasure-participants` JSON. That product's card appears.
5. Privacy policy 3.4 before step 4 reaches prod.

## 12. Cross-project impact

- **ctech-go-common:** `erasure` gets `TypeRelinked` and its consumer branch; new tag.
- **ctech-account api:** the request model, the locker, the worker transitions, the purge-for-scope step, the code
  exchange hook. The code exchange is a critical area (token issuance): the hook adds one read of a field already on
  the loaded user and a publish only for users who left a product.
- **ctech-account ui:** one new card list and the scope-aware copy.
- **ctech-account cdk:** none (the SSM JSON is operator-owned).
- **ctech-wallet, ctech-dfe, ctech-billing, ctech-poker:** version bump and a README line each. No schema change.
- **Third parties:** wallet's Asaas sub-account closure is part of wallet's purge for full deletion. Whether leaving
  only the wallet closes it is open (§ 13).

## 13. Open questions

- **Wallet and the Asaas sub-account:** may `wallet` be left while the person's CPF keeps its Asaas sub-account? It
  needs a ruling from the wallet owner. Until then `wallet` has no `login_clients` in any environment, so its card
  never appears (§ 5.2). The other three products do not depend on it.
