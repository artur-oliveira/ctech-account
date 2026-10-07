# Spec: Account deletion — data inventory & retention matrix

- **Status:** Draft (2026-10-06, decisions D1–D7 applied). **Needs legal validation before any purge code ships.**
- **Parent:** [Account deletion overview](2026-10-06-account-deletion-overview.md)

## 1. Purpose

List every place each service stores personal data keyed (directly or indirectly) by a
user, and decide per item: **Erase**, **Anonymize** or **Retain** (with legal basis and
expiry). It also lists each service's **blockers**. Purge code implements exactly this
table, nothing more.

Each service owner must complete and sign off its section. Rows marked `TODO` were not
confirmed from code when this draft was written. The table lists come from each repo's
CDK/Terraform as of 2026-10-06.

Retention periods below are **proposals**. "5 y" means five years from the end of the
relevant fact (transaction, fiscal year, relationship), not from the deletion request.

## 2. Cross-cutting stores (every service)

| Store | Personal data | Treatment |
|---|---|---|
| CloudWatch Logs | IPs, e-mails, sometimes request bodies | No per-user delete possible. Enforce a log retention ≤ 90 days on every log group, and make sure logs never contain CPF/address/phone (audit with grep for log fields). Documented in the privacy policy. |
| DynamoDB PITR / backups | full copies | Not edited. Expire naturally (PITR ≤ 35 days). **Restoring a backup requires re-applying tombstones** (runbook item, sub-spec 2 §8). Documented in the privacy policy. |
| S3 versioned buckets | old object versions | Purge must delete **every version and delete marker**, not just the current key. |
| Shared Valkey | session/cache entries keyed by `sub` | Delete by key pattern at purge; TTL covers the rest. |
| SES | addresses in suppression list / sending logs | Nothing kept by us beyond logs. |

## 3. ctech-account

Tables: `{env}_account_*`. Buckets: KYC documents (versioned, 5 y expiry), logs.

| Item | Location | Treatment | Basis / note |
|---|---|---|---|
| Profile: name, display name, avatar, e-mail, Google sub, password hash | `account_users` | Erase | — |
| CPF, legal name, birth date, address, phone (live profile copy) | `account_users` | Erase from the user item (the retained copy lives in the KYC retention item below). Keep `HMAC-SHA256(kms_key, cpf)` in the tombstone | Fraud prevention (legitimate interest), expiry 5 y |
| KYC record: legal name, CPF, birth date, address, phone, level, status, verified-at, review decision, reviewer id, risk score/signals | moved out of `account_users` into a retention item (`KYCRET#{sub}`, same table or a dedicated restricted table) | **Retain** (D6), not readable by any app endpoint, only by support `admin` under audit | Lei 9.613 art. 10, 5 y after the relationship ends (expiry = `purged_at` + 5 y, via DynamoDB TTL) |
| KYC documents (images) | KYC S3 bucket (versioned) | **Retain** (D6): move/copy to a `retention/{sub}/` prefix with an object tag `retain-until`, delete the originals (all versions). App role has no read on `retention/` | same. The bucket's current lifecycle expires 5 y after **upload**, not after closure: add a lifecycle rule on the `retention/` prefix keyed by tag (or expire by a scheduled job reading `retain-until`) |
| ToS/Privacy acceptance version + timestamp | `account_users` | Retain in the tombstone | Proof of consent (art. 8 §2), 5 y |
| Sessions, refresh tokens, OAuth consents (`CONSENT_*`) | `account_sessions` | Erase | — |
| MFA secrets, backup codes | `account_mfa` | Erase | — |
| Passkeys | `account_passkeys` | Erase | — |
| API keys | `account_api_keys` | Erase | — |
| OAuth clients the user registered (`owner_user_id`) | `account_oauth_clients` | **Blocker** if the client is used by other users; otherwise Erase. `TODO`: confirm whether any non-internal client exists | — |
| Organizations owned (`owner_user_id`) | `account_organizations` | **Blocker** if it has other members (transfer ownership). If the user is the **sole member: Erase** the organization with the account, irreversibly (D4), and dispatch it to dfe for a full org purge (§5) | Data processed on the organization's behalf; the 5 y fiscal duty is the taxpayer's |
| Organization ↔ company links of an erased organization | `account_memberships` / `account_companies` edges | Erase the links. The company (CNPJ) row itself stays if another organization still references it; if no organization references it anymore, Erase it | — |
| Memberships | `account_memberships` | Erase | — |
| Invitations sent/received | `account_invitations` | Erase those received; anonymize `invited_by` on those sent | — |
| Companies (CNPJ registry) | `account_companies` | A company row is erased when no surviving organization references it. **MEI companies are personal data** (D12): the business name carries the owner's name and the CNPJ is tied to their CPF. So an orphaned MEI row is always erased, never just left in the registry. An MEI row still referenced by another organization (e.g. the accountant's) stays: it is that organization's data about its client | — |
| Support tickets + messages | `account_support_tickets` | Anonymize requester fields, erase message bodies the user wrote; keep agent-side metadata | Erase unless a ticket is part of a dispute (then legal hold) |
| Support metrics | `account_support_metrics` | Aggregates; verify no `sub` is stored | `TODO` |
| Audit log | `account_audit` | Anonymize IP/user-agent/geo on the user's rows; **retain** the deletion-request rows | Proof of compliance, 5 y |
| Deletion request record | `account_deletion_requests` (new) | Retain (no PII: sub + statuses + timestamps) | Proof of compliance, 5 y |

**Blockers:** sole owner of a multi-member organization; registered OAuth client in use;
`legal_hold`.

## 4. ctech-wallet (CTech Ledger)

Tables: `wallet_users`, `wallets`, `wallet_ledger_entries`, `wallet_holds`,
`wallet_pix_deposits`, `wallet_withdrawals`, `wallet_transfer_intents`,
`wallet_settlement_legs`, `wallet_med_receivables`, `wallet_product_purchases`,
`wallet_sandbox_purchases`, `wallet_baas_accounts`, `wallet_idempotency`, `wallet_audit`.
External: **Asaas** sub-account (BaaS custody).

| Item | Treatment | Basis |
|---|---|---|
| Ledger entries, deposits, withdrawals, transfers, settlement legs, MED receivables | Retain (segregated: copy to a restricted retention table/bucket or tag + IAM deny for the app role), expiry 5 y after account closure | Lei 9.613 art. 10; BaaS contract with Asaas |
| PIX keys / counterpart bank data on withdrawals | Retain with the transaction (same basis) | same |
| `wallet_users` profile copy (name, CPF, etc.) | Anonymize, except fields required by PLD retention (name + CPF linked to transactions) — those move with the retained records | same |
| Product/sandbox purchases | Retain if money moved, else Erase | fiscal |
| Holds | Must be empty (blocker) | — |
| Idempotency keys | Erase (TTL already) | — |
| `wallet_audit` | Anonymize IP/UA, retain events | PLD |
| Responsible-gambling settings (self-exclusion) | **Retain** (D13) as `SELFEXCL#{cpf_hmac}` with `until`, nothing else. Timed self-exclusion: kept until its end date. Indefinite/permanent: kept 5 y after PURGED (same horizon as the PLD retention). Limits (deposit/loss/time) are Erased. On a new wallet/poker signup, KYC Basic checks the HMAC and re-applies the remaining period | Lei 14.790/2023 and its responsible-gambling regulation; protects the player against using deletion to escape their own self-exclusion. Legal to confirm the 5 y horizon |
| Asaas sub-account | Close through the Asaas API after balance is zero. Asaas keeps its own records under its own obligation; we record the closure request id | art. 18 §6 |

**Blockers:** balance ≠ 0 (any wallet of the user); pending PIX deposit or withdrawal;
open transfer intent; active hold (poker); open MED / dispute; Asaas sub-account in a
non-closable state.

Balance policy: the user must **withdraw to an account of the same CPF**. No automatic
transfer, no forfeiture. The UI shows the balance and links to withdrawal. A
**closing withdrawal has no minimum** (D7): the withdrawal is PIX, so any amount
≥ R$ 0,01 is accepted. Product-level minimum withdrawal rules must not apply when the
user has an open deletion request, otherwise the user could never reach zero.

Wallet is also the **system of record for poker money** (D5): buy-ins, cashouts, holds,
rake and paid poker purchases are ledger entries here and are retained with the rest of
the ledger. Poker itself keeps nothing for retention.

## 5. ctech-dfe

Org-scoped tables (`organization_*`, `nfes`, `nfces`, `ctes`, `mdfes`, `nfses`, `*_events`,
`*_distributions`, `worker_outbox`) belong to the **organization**, not to the person.
Buckets: documents (XMLs/DANFEs), certificates.

Two purge modes, both driven by the `user.erase` message (protocol §3):

- **Person purge** (always): the table below.
- **Organization purge** (for each id in the message's `organizations[]`, i.e. orgs where
  the deleting user was the only member — D4): **Erase everything keyed by the org**:
  every org-scoped table above, all objects and versions under the org's prefixes in the
  documents and certificates buckets, the org's `audit_logs` rows, outbox rows, and the
  `organizations` item itself. No retention. Must also stop any in-flight
  emission/distribution job for the org (check the tombstone in the worker before each job).

| Item | Treatment | Basis |
|---|---|---|
| `users` (`USER_{uuid}`, e-mail, username) | Erase | — |
| `organization_users` (`USER_{sub}` membership) | Erase | — |
| `organization_invitations` | Erase received; anonymize inviter | — |
| `audit_logs` rows with the user's `user_id` | Anonymize actor (keep `sub` hash, drop name/e-mail/IP) | Org's fiscal trail |
| Fiscal documents (XML, events, distributions) of an organization that **other members** still use | **Untouched**: they belong to the organization | The organization is the controller of its own fiscal data |
| Digital certificates (A1) — **e-CNPJ** | Belong to the organization; untouched (unless the org is erased) | — |
| Digital certificates (A1) — **e-CPF of the user** (ICP-Brasil certificate whose holder CPF, OID `2.16.76.1.3.1` in the SAN, equals the user's CPF) | **Erase** (D14): table item + every object version of the PFX and its stored password/secret, in every organization, including organizations that survive. Notify the surviving organization's admins that the certificate was removed (emission with it stops) | Personal credential; no retention basis |
| `organization_persons` with `CPF_{cpf}` (recipients/customers) | Data of third parties, controlled by the organization; untouched by this flow | — |

**Before confirmation**, the account UI offers an XML export (zip per organization) for
every organization that will be erased. dfe must expose an export endpoint for this
(`TODO`: check whether a bulk XML download already exists in dfe before building one).

**Blockers:** sole owner of an organization with other members (ownership lives in
ctech-account, so this is checked there); any in-flight emission job in `worker_outbox`
started by the user (`TODO`: confirm whether outbox rows reference the user).

## 6. ctech-billing

Terraform-managed; table names come from `terraform/billing/dynamodb.tf` (`TODO`: list).
Bucket: `*-documents`.

| Item | Treatment | Basis |
|---|---|---|
| Invoices, payments, issued NFS-e for subscriptions | Retain 5 y | fiscal |
| Customer profile (name, CPF/CNPJ, address used on invoices) | Retain only as embedded in issued invoices; erase the live profile | fiscal |
| Payment methods / tokens at the gateway | Erase (ask gateway to delete the customer/token) | art. 18 §6 |
| Subscriptions | Cancel, then Erase metadata not tied to an invoice | — |

**Finance module ("mini ERP",** `ctech-billing/docs/specs/2026-10-07-finance-erp-design.md`,
ADR 0026). Tables `{env}_billing_ledger_accounts`, `ledger_transactions`, `bills`,
`recurrences`, `cards`, `imports`; every partition key starts with the space `S`.

| Item | Treatment | Basis |
|---|---|---|
| **Personal space** (`S = USER#{sub}#live` and `USER#{sub}#test`): ledger accounts, transactions, entries, summaries, bills, recurrences, cards, statements, purchases, import locks, personal-space finance audit rows | **Erase** everything under both prefixes, in every finance table (ADR 0026: no TTL, purge on account deletion) | Management notes of the person about their own money; no legal floor (ADR 0026 "Limits accepted") |
| **Organization space** (`S = {organization_id}#{mode}`) of an organization that **survives** | Untouched: the data is the organization's | — |
| **Organization space** of an organization erased with the user (`organizations[]`, D4) | **Erase** everything under `{organization_id}#live` and `#test` | Same purge path ADR 0026 defines for "organization closed" |
| The seeded *Assinaturas CTech* payable in the personal space | Erased with the space. The CTech invoice it mirrors stays in tenant zero (retained, fiscal) | — |
| Import batches/lines | TTL 90 days already; erased with the space if still present | — |

Billing's purge test must list every finance table and assert each one is covered (ADR 0026
consequence); the `PurgeFunc` reuses that per-prefix job.

**Blockers:** open/overdue invoice; active subscription that cannot be cancelled
immediately (cancel it as part of the confirmation flow); payment in dispute.

## 7. ctech-poker

Tables `poker_*` (36), buckets: avatars, action-log archive.

| Item | Treatment |
|---|---|
| Player profile, avatar (all versions), cosmetic loadouts, chat prefs, wallet alert prefs, daily reward, achievement progress, notes **written by** the user, recent players, social edges/events | Erase |
| Notes **about** the user written by others (`player_notes`) | Erase (they are personal data about the user) |
| Hand history, action log, action-log archive (S3), table state history, hand meta, matchups, leaderboard stats | Anonymize: replace `sub` with a random pseudonym (one per erased user, never stored next to the `sub`) and drop display names. Other players' hand histories stay consistent. No retention here: the money trail is in the wallet ledger (D5) |
| Purchases / entitlements / promo redemptions / hand reveal payments | Erase. The payment itself is a wallet ledger entry, retained there (D5) |
| Player reports (made by or against the user) | Retain anonymized for 1 y (anti-abuse), `TODO` |
| Pending cashouts | Must be empty (blocker) |

**Blockers:** seated at a table / in a tournament; pending cashout; chips held in a game
(wallet hold).

## 8. Third parties (art. 18 §6)

| Processor | Data | Action on PURGED |
|---|---|---|
| Asaas (BaaS) | sub-account, KYC, transactions | close sub-account; they retain under their own obligation |
| Payment gateway used by billing (`TODO`) | customer, card tokens | delete customer |
| Google (social login) | nothing sent by us beyond the OAuth flow | none |
| AWS SES | e-mail address in logs | none (logs expire) |

## 9. Open questions

Resolved 2026-10-06 (see overview §11, D4–D14). Remaining: legal validation of the
whole matrix, in particular the 5 y horizon for indefinite self-exclusion (D13).
