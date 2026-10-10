# Plan limits on personal spaces

Status: **Implemented** · 2026-10-10 · Consumer and seller: `ctech-billing`
(`ctech-billing/docs/specs/2026-10-10-plans-design.md`, §§ 5 and 6 there are the contract) · Extends
[personal workspaces](2026-10-09-personal-workspaces.md) § 7 ("Plan limits (later)")

## Outcome

ctech-billing sells CTech Finanças plans that limit how many **personal spaces** (`kind: personal`) a person owns
and how many **people** each space holds. This repository enforces them, because the two writes they limit —
creating a space and inviting into one — are its writes. It also tells billing the current levels, so the
on-demand plan can be billed on the month's peak.

Organizations (`kind: organization`) are not counted and not limited by any of this.

## 1. The numbers come from billing

| Plan | `quota_spaces` | `quota_people_per_space` |
|---|---|---|
| Free (implicit, no subscription) | 1 | 1 |
| Basic | 3 | 5 |
| Pro | 10 | 10 |
| Sob demanda | -1 (unlimited, billed) | -1 |

This table is **not** copied into code here. The limits are read from billing on every check:

```
GET {BILLING}/v1.0/entitlements?customer_ref=USER_{owner_sub}&owner_key=finance
```

The answer's entitled subscription carries `metadata.quota_spaces` and `metadata.quota_people_per_space`; with
none, `default.metadata` carries Free's. `-1` is unlimited. A metadata value that is missing or not an integer
is treated as **0** (refuse growth) and logged: a catalogue mistake must not turn into unlimited spaces.

## 2. What is counted

| | Counted | Not counted |
|---|---|---|
| Spaces | `personal` workspaces whose owner is the person | the default *Pessoal* space (it is not a workspace here); organizations; spaces the person is only a member of |
| People in a space | members + **unexpired** pending invitations, both levels alike | the owner |
| People for billing (`finance_people`) | **distinct** people across all of the owner's `personal` workspaces: user ids of members, plus normalised e-mails of unexpired invitations whose address is not already a member somewhere | the owner |

Re-inviting the same address overwrites the pending row (`Invite`), so it is not a second person.

## 3. Where it is checked

| Write | Check | On refusal |
|---|---|---|
| `CreateOfKind(personal, …)` (the `/account/spaces/new` handoff) | owned spaces < `quota_spaces` | 402 `plan_limit` |
| `Invite` on a `personal` workspace | people in that space < `quota_people_per_space` | 402 `plan_limit` |
| `Transfer` on a `personal` workspace | the **new owner's** owned spaces < their `quota_spaces` | 402 `plan_limit` |
| `Accept` | none — the invitation was counted when sent | — |
| `Remove`, leaving, `RevokeInvitation`, `SetRole` | none — reducing or reshaping usage is never refused | — |

`Create` of an organization, and everything on organizations, is unchanged.

**Over the limit, nothing is removed** (plans spec D6). A person on Free with three spaces — created before
limits existed, or before a downgrade — keeps all three, every member keeps access, and only a fourth space or a
new invitation is refused.

### 3.1 Order and failure

1. Read the entitlement — live, **no cache**, so an upgrade lifts the limit at once.
2. Count and compare.
3. Write (the existing transaction), with the guard of § 3.2.
4. Report the levels (§ 4).

- Billing unreachable, timing out (2 s) or answering 5xx at step 1 → **503** `plan_unavailable`, nothing
  written. Retries on the read follow `api-commons`' HTTP client defaults (idempotent GET).
- Over the limit → **402** `plan_limit` with `{limit, used, plan, resource: "spaces"|"people"}`.

### 3.2 Concurrency

A count followed by a write lets two concurrent requests both see 2 of 3 and both write. The write carries a
conditional counter in the same `TransactWrite`:

| Counter | Item | Condition |
|---|---|---|
| Spaces | `pk=OWNER#{sub}`, `sk=PERSONAL_SPACES`, attribute `n` | `n < quota` (or absent), `ADD n 1` |
| People | on the workspace's META item, attribute `people_n` | `people_n < quota` (or absent), `ADD people_n 1` |

The counters are guards, not the source of truth for display or billing; the counts in § 2 are. They are
re-derived from the source rows (owned spaces; members + unexpired invitations) at every check, written with the
transaction, and decremented by remove, leave, revoke and transfer-away. A drifted counter is corrected by the
next check, which sets it to the real count before comparing.

With `-1` (unlimited), the counter is still maintained, with no condition.

## 4. Level reports to billing

After every committed change to an owner's levels — space created, transferred in or out, invitation sent,
revoked or expired, member removed or left — this repository reports **both** levels for that owner:

```
POST {BILLING}/v1.0/usage/levels
{ "customer_ref": "USER_{owner_sub}", "meter": "finance_spaces", "value": 4,
  "occurred_at": "…", "idempotency_key": "lvl:{owner_sub}:finance_spaces:{occurred_at_unix_ms}" }
```

- Reported **whatever the plan**, Free included: billing must already know the level on the day the person
  moves to Sob demanda.
- `value` is the whole current count, never a delta.
- **A report never undoes the write.** It is delivered through a small durable queue:
  1. the change writes `pk=LEVEL_DIRTY`, `sk={owner_sub}` with `due_at = now` (a put; several changes collapse
     into one row);
  2. the request then tries to report inline; on success it deletes the row (conditional on its `due_at`, so
     a newer change is not lost);
  3. a worker drains due rows every minute, on the deletion worker's pattern (`RunWorker`, a Valkey lock per
     tick), recounting from the source rows at send time.
- **Invitation expiry** has no event (DynamoDB TTL reaps the row later). Sending an invitation also writes a
  `LEVEL_DIRTY` row due at its `expires_at`, so the level drops on the day it expires, not whenever the owner
  next changes something. The count excludes expired invitations whether or not TTL has removed them.

## 5. Authenticating to billing

No new secret. This repository is the issuer: it signs its own client-credentials access token in process, with
`jwtSvc.SignAccessToken`, for an OAuth client **`account-billing`** registered here (`cmd/createclient`) with the
scopes `billing:entitlements:read` and `billing:usage:write` and audience billing. The token is cached until a
minute before it expires.

On billing's side `account-billing` is a credential of tenant `ctech`, scoped to owner `finance`
(plans spec § 4).

## 6. Counts for billing's plan screen

`GET /internal/users/:user_id/organizations` adds, on each `personal` workspace the user **owns**:

```json
{ "people": 3, "pending_invitations": 1 }
```

Same route, same scope, computed only for owned spaces (a member gets no counts for someone else's space). The
route already reads each workspace; invitations are one `Query` per owned space, bounded by `quota_spaces`.

## 7. UI

- **`/account/spaces/new`** refused with 402: the form is replaced by *"Seu plano permite N espaços e você já
  tem N."* with **Ver planos** → `{BILLING}/finance/plans` and **Voltar** → `return_to?cancelled=1&state=…`.
- **People page, invite** refused with 402: inline, *"Este espaço já tem N de N pessoas do seu plano."* with
  **Ver planos**.
- 503: *"Não foi possível verificar seu plano agora. Tente em instantes."* The roster and everything else on the
  page keep working.
- The people page shows `people + pending of limit` beside the invite button, read from the same entitlement.
- **Transfer** refused with 402: *"{Name} já está no limite de espaços do plano."* — without revealing the
  other person's plan name.

## 8. Tests

1. Free person: second space → 402; first created; Pessoal never counted.
2. Basic: fourth space → 402; sixth person in one space → 402 counting pending invitations; owner not counted.
3. Expired invitation not counted, even with the row still present.
4. Two concurrent creations at 2 of 3 → exactly one succeeds (the guard), the other 402.
5. Billing down → 503 and nothing written; organizations unaffected.
6. Over the limit after a downgrade: existing spaces, members and roles all work; remove/leave/revoke work.
7. Transfer to a person at their limit → 402; the space stays with the current owner.
8. Every change writes `LEVEL_DIRTY`; a failed inline report is delivered by the worker; a newer change is not
   deleted by an older success; an invitation's expiry produces a report at `expires_at`.
9. `finance_people` counts a person in two spaces of one owner once; an invited address that is already a
   member is not double-counted.
10. Malformed quota metadata → treated as 0, logged.
11. The internal route returns counts only on owned `personal` workspaces.

## Deploy order

1. ctech-billing: catalogue, entitlements `owner_key`/`default`, `usage/levels`, the `account-billing`
   credential (its § 10 step 1).
2. This repository: the `account-billing` client, enforcement, level reports, counts on the internal route.
3. ctech-billing: the plan screen.

Before step 2 nothing is limited, which is today's behaviour. If step 2 ships before step 1, every create and
invite on a `personal` workspace answers 503 — so step 2 checks billing's `owner_key` support at startup and
logs loudly, and the deploy waits for step 1.

## Not in this spec

- Limits on organizations.
- Deleting a space.
- Anything about what happens inside a space (billing's).

## Amendment, planning (2026-10-10)

Recorded from `docs/plans/2026-10-10-space-plan-limits.md` ("Decisions"), where this spec is silent or does not
fit the code:

- **P1 — Queue keys.** `pk=LEVEL_DIRTY`, `sk=NOW#{owner}` (collapsing, due now, deleted conditionally on
  `due_at`) and `sk=AT#{unix_seconds:012d}#{owner}` (scheduled, e.g. an invitation's expiry + 1 s). One row per
  owner (`sk={owner_sub}`) cannot hold both a "due now" and a "due at `expires_at`" row.
- **P2 — The spaces guard compares `max(owned spaces, counter)`.** Owned spaces are read from `lookup-index`
  (a GSI, eventually consistent); the counter is read consistently. The counter is corrected upward at every
  check and downward by the worker, conditionally, once the index has settled. The people guard reads
  base-table queries with `ConsistentRead` and re-derives the counter exactly as § 3.2 says.
- **P3 — Decrements are best-effort writes after the commit**, never inside the transaction (rows written before
  plan limits have no counter, and reducing usage is never refused). A failed decrement schedules a reconcile.
- **P4 — Quotas are read from `subscriptions[].items[].metadata`**, the first item carrying `quota_spaces`
  (a Sob demanda subscription carries `-1`/`-1` on both its items); several entitled subscriptions → the most
  generous. No entitled subscription and no `default` → 503 (billing without `owner_key` support), not 0.
- **P5 — No shared retrying HTTP client exists in api-commons.** The entitlement GET retries once on a transport
  error or 5xx inside its 2 s budget; level POSTs are not retried in the request (the queue retries them).
- **P6 — New route `GET /v1.0/organizations/:id/plan-usage`** (owner of a space): `{people,
  pending_invitations, limit, plan}`, for the people page's counter.
- **P7 — Transfer's 402 carries `resource` only** — no `limit`, `used` or `plan`, which are the other person's.
- **P8 — `Accept` reports levels** (never refused): `finance_people` can change when an invited address becomes
  a user already in another of the owner's spaces.
- **P9 — Three guard conflicts in a row answer 409** "try again", never a 402 for a limit not reached.
- **P10 — `{BILLING}` is two hosts:** `BILLING_API_URL` (`billing-api[-env].aoctech.app`) for the account;
  `NEXT_PUBLIC_BILLING_URL` (`billing[-env].aoctech.app`) for *Ver planos*.
- **P11 — Marking dirty happens right after the commit**, not inside the write's transaction. A process killed
  between the two loses that report until the owner's next change; the next report carries the whole level.

Also settled: problems carry `code` `plan_limit` / `plan_unavailable` (types `…/problems/plan-limit`,
`…/plan-unavailable`); the internal route's counts cost two queries per owned space (members and invitations);
everything stays off until `BILLING_API_URL` is set. A switch from Basic/Pro to Sob demanda takes effect at the
end of the paid period, so entitlements keep returning the current plan until then — nothing here depends on it.

## Amendment, implementation (2026-10-10)

Departures from P1–P11:

- **A 409 `concurrent_update` from billing is not delivered.** Billing's level store answers it when another
  report moved the latest level first and nothing was recorded; the queue keeps the row and retries. Only
  other 409s (`idempotency_key_reused`, the same key with another body) count as delivered.
- **The CDK sets `BILLING_API_URL` only with `PLAN_LIMITS=on`.** Setting it unconditionally would have switched
  enforcement on with the first deploy, before ctech-billing's seed created the `account-billing` credential
  and before the client was registered here — every create, invite and transfer of a space would answer 503.
  `PLAN_LIMITS=on` is the deploy switch; leaving it out is the rollback.
- **"Ver planos" opens `{BILLING}/finance/plans`.** ctech-billing moved Finanças to its own area (`/finance`)
  on the same day.
- **A change's report is sent twice: right away, and again once the membership index has settled.** The
  count reads an eventually consistent index; milliseconds after the commit it can miss the space just
  created. The `NOW#` row is kept after the inline report, and the worker re-reports it once it is at least
  30 s old and only then clears it, so billing never keeps a level below the real one. An invitation's
  `AT#` row is reported when due, as before.
- **Billing's 409 is read by its `code`:** only `idempotency_key_reused` counts as delivered.
- **The internal route never fails for one space's counts:** a space whose counts cannot be read is listed
  without them (billing reads absent counts as unavailable).
