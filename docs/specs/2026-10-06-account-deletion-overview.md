# Spec: Account deletion (LGPD) — overview

- **Status:** Draft (2026-10-06)
- **Projects:** `ctech-account` (api, ui, cdk), `ctech-go-common`, `ctech-wallet`, `ctech-dfe`,
  `ctech-billing`, `ctech-poker`
- **Sub-specs:**
  1. [Data inventory & retention matrix](2026-10-06-account-deletion-data-inventory.md)
  2. [Erasure saga protocol (cross-service contract)](2026-10-06-account-deletion-saga-protocol.md)
  3. [ctech-account implementation](2026-10-06-account-deletion-ctech-account.md)

## 1. Outcome

A user can ask for their CTech account to be deleted. After a short, cancelable grace
period, every CTech service erases or anonymizes that user's personal data. Only the
minimum the law obliges us to keep is retained, segregated, and with an expiry date.
The user can also leave **a single service** without closing the account.

The process must survive partial failure (a service down for hours, a crashed worker, a
poisoned message) and abuse (stolen session, evading a debt or a fraud investigation).

## 2. Legal frame — what "delete everything" actually means

LGPD art. 18, VI grants the right to have data **eliminated**. Art. 16 also **obliges**
retention when there is a legal basis. So the result is never "zero bytes". Each datum
falls into one of three treatments:

| Treatment | Meaning | Example |
|---|---|---|
| **Erase** | Hard delete, including S3 object versions and caches | profile, address, phone, passkeys, avatars, chat prefs |
| **Anonymize** | Keep the record, replace personal fields with irreversible values; the `sub` stays as an opaque key | DF-e audit log rows, poker hand history, support tickets |
| **Retain (legal hold)** | Keep as is, segregated, access-restricted, not used operationally, with an expiry | wallet ledger + KYC for PLD (5 y), issued fiscal documents (5 y), our own deletion audit trail |

Legal bases for retention (to be validated by legal counsel — **blocking**):

- Art. 16, I / art. 7, II — legal or regulatory obligation: fiscal (CTN art. 173/174),
  anti–money laundering (Lei 9.613/1998, art. 10), Bacen/BaaS provider obligations,
  fixed-odds betting rules (Lei 14.790/2023) for poker money, which is recorded in the
  wallet ledger (D5).
- Art. 7, VI — regular exercise of rights in judicial/administrative proceedings (open
  disputes, chargebacks).
- Art. 7, IX / art. 10 — legitimate interest, limited to fraud prevention (keyed hash of
  the CPF, see sub-spec 3).

Also required by LGPD and in scope:

- Art. 18, §6 — tell every processor/third party we shared the data with (Asaas,
  billing's payment gateway, etc.) to erase it too, unless they hold it under their own
  legal obligation. KYC is manual and in-house (no KYC provider, D11).
- Art. 19 — answer the request. ANPD's reference deadline is 15 days. The request is
  **acknowledged** at once; **completion** has an internal SLA (§7).
- The privacy policy must state the grace period, what is retained, why, and for how
  long. This needs a new legal version (`ui/src/lib/legal-documents.ts` pattern).

**Fiscal documents are not the user's to delete.** An NF-e XML belongs to the issuer
(the CNPJ / organization) and contains third parties' data (the recipient). Deleting a
person does not delete the fiscal documents of an organization that other people still
use. It removes the person's link to the organization and anonymizes the person's own
fields.

**Exception — organizations where the user is the only member are deleted with the
account, irreversibly** (decision 2026-10-06), including all of their DF-e data
(documents, XMLs, certificates, catalogs). The duty to keep fiscal documents for 5 years
is the **taxpayer's** (the organization), not CTech's: we process those documents on the
organization's behalf. When the relationship ends, the processor deletes the data. The
SEFAZ keeps its own copy. Before confirming, the UI offers a **document export** (XML zip
per organization) and states clearly that the documents will be gone.

## 3. Lifecycle

```
ACTIVE ──request──▶ PENDING_DELETION ──grace expires──▶ LOCKED ──▶ PURGING ──▶ PURGED
   ▲                     │                                            │
   └──────cancel─────────┘                         legal_hold ⇒ paused (stays LOCKED/PURGING)
```

- **PENDING_DELETION** (grace, **7 days**, D8): the account is **already blocked**. No
  new sessions, all tokens revoked. The owner cancels through the link in the
  e-mails. This protects against a stolen session destroying the account.
- **LOCKED**: grace over, now irreversible. Blockers are checked again and the saga starts.
- **PURGING**: services erase/anonymize and ack.
- **PURGED**: only a tombstone is left (sub-spec 3, §6).

"Irreversible" starts at LOCKED, not at the click. This is a deliberate fraud safeguard;
the UI copy must say so clearly.

## 4. Two scopes, one mechanism

| Scope | Trigger | Effect |
|---|---|---|
| `service` (unlink) | user leaves e.g. CTech Ledger | revoke the OAuth consent + sessions for that client, run the saga against **one** service, account stays ACTIVE |
| `account` (full) | user closes the CTech account | run the saga against **all** services, then purge ctech-account itself |

Full deletion is "unlink every service, then erase the account". Same protocol, same
blockers, same acks. If the user comes back to an unlinked service, that service
provisions them from scratch (the old service-side `sub` data is gone; the tombstone
records the unlink so stale events are rejected).

## 5. Blockers (pre-conditions)

Deletion is refused (with an actionable reason) while any blocker exists. Blockers are
checked **at request time** and **again at LOCKED**, because a PIX deposit or an invoice
can appear during grace. If a blocker appears during grace, the request stays pending and
the user is notified; at LOCKED with a blocker, the request goes to `BLOCKED`: the user
cancels, fixes the blocker, and files a new request (a locked user cannot withdraw or pay).
A blocker reported during PURGING is resolved by support (sub-spec 2, §7).

Per-service blockers are listed in sub-spec 1. The important ones:

- **Wallet with balance**: we can neither erase nor keep someone's money. The user must
  withdraw to an account of the same CPF first. Pending deposits/withdrawals, holds,
  MED/chargeback disputes also block.
- **Sole owner of an organization with other members**: ownership must be transferred
  first. The organization (and its fiscal documents) outlives the person.
  Single-member organizations are **not** a blocker: they are deleted with the account.
- Open invoice / active subscription (billing), seat at a table or chips in play (poker).
- `legal_hold` set by support/legal (open fraud investigation, court order, dispute).

## 6. Design decisions

1. **Orchestrated saga, not choreography.** ctech-account owns a durable per-request,
   per-service status table and drives retries. No one has to reconstruct "is user X
   fully deleted?" from logs.
2. **At-least-once delivery + idempotent, resumable purge.** DynamoDB has no
   cross-table transactions and `TransactWriteItems` caps at 100 items, so each service
   purges in batches and must be safe to re-run from scratch.
3. **Explicit ack.** A service is done only when it says so. Silence is "not done".
4. **Tombstones.** A purged `sub` is remembered (without personal data) so late writes,
   retries and replays cannot resurrect it.
5. **Events carry only the `sub`.** Never CPF, e-mail or name.
6. **Immediate token cut-off** through a revocation list checked by
   `ctech-go-common/jwtverify`, not by waiting out the 15-minute access-token TTL.
7. **Shared contract in `ctech-go-common`** (`erasure` package). Five services implement
   the same consumer; it is not written five times.

## 7. SLA and observability

- Request acknowledged synchronously (e-mail with a protocol number).
- Target: PURGED ≤ 15 days after the request, i.e. ≤ 8 days after LOCKED.
- Alert when any service is not acked 48 h after dispatch, and when a request has been in
  PURGING for more than 5 days.
- Every state transition is written to the account audit table (retained, sub-spec 3).

## 8. Out of scope

- Data **portability** (art. 18, V) export. It is a sibling feature and should ship
  before or with this one, because users will ask "can I download my data first?".
- Deleting organizations/companies themselves (a separate flow; this spec only blocks on
  them).
- Restoring a deleted account. There is none after LOCKED.

## 9. Rollout

1. Legal validation of the retention matrix (sub-spec 1). **Gate for everything else.**
2. `ctech-go-common/erasure` + `jwtverify` revocation check (sub-spec 2).
3. ctech-account: state machine, request/cancel endpoints, lock, revocation, orchestrator,
   own purge (sub-spec 3). Ship with **no services subscribed** first: proves request →
   lock → purge of the account alone in dev.
4. Service unlink, one service at a time: poker → billing → dfe → wallet (simplest to
   hardest; wallet last because of money and Asaas).
5. Full account deletion enabled once all four services are acking in prod.
6. New privacy policy version published before step 4 goes to prod.

## 10. Cross-project impact

- **ctech-account api**: new domain, endpoints, worker, token issuance refuses non-ACTIVE
  users, audit events. High blast radius: touches session and token issuance.
- **ctech-account ui**: deletion/unlink screens, blocker list, cancel screen, new legal version.
- **ctech-account cdk**: SNS topic, `deletion_requests` table, IAM, alarms.
- **ctech-go-common**: new `erasure` package, `jwtverify` revocation check.
- **ctech-wallet / ctech-dfe / ctech-billing / ctech-poker**: eligibility endpoint, purge
  consumer, SQS queue + DLQ in each CDK/Terraform, `jwtverify` upgrade.
- **Third parties**: Asaas (sub-account closure), billing's payment gateway. No KYC provider (D11).

## 11. Decisions (2026-10-06)

| # | Decision |
|---|---|
| D1 | The account and every participant are locked from the start of grace (`pending_deletion`). The **only** way back is the user cancelling the request. |
| D2 | A request only starts after confirmation through the verified e-mail. |
| D3 | Immediate token cut-off through the `jwtverify` revocation list (fail open in general, fail closed on money-moving routes). |
| D4 | Organizations where the user is the only member are deleted with the account, irreversibly, including all DF-e data. A document export is offered first. |
| D5 | Poker will move real money, but **every money movement is recorded in the wallet**. The wallet ledger carries the regulatory retention. Poker's own data is erased/anonymized, not retained. |
| D6 | ctech-account **retains** its minimal KYC record and KYC documents (PLD, 5 years after the relationship ends). |
| D7 | No minimum withdrawal for closing the account. Withdrawals are PIX, so any amount ≥ R$ 0,01 is accepted. The balance-must-be-zero blocker stays; there is no "dust" case. |
| D8 | Grace period: **7 days**, fixed. |
| D9 | A user whose request is under legal hold **sees that a hold exists** ("Sua solicitação está suspensa por bloqueio jurídico") and how to contact support/DPO. The reason is not shown. |
| D10 | The e-mail can be re-used for a new account immediately after PURGED. Only the CPF keyed hash survives (fraud prevention). |
| D11 | There is no third-party KYC provider: KYC is manual and in-house. No external KYC erasure request. |
| D12 | An MEI company is an organization (CNPJ) whose data identifies a natural person, so it is treated as **personal data**: erased when orphaned, and never left behind with the person's name after the organization is erased. |
| D13 | Wallet self-exclusion survives account deletion, retained by CPF keyed hash until the declared period ends; indefinite self-exclusion is kept for 5 years after PURGED (inventory §4). |
| D14 | e-CPF certificates (ICP-Brasil personal digital certificate) of the user are **erased** from dfe, including from organizations that survive (inventory §5). |

## 12. Open questions

None beyond the legal validation of the retention matrix (rollout step 1).
