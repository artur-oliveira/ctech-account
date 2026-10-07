# Spec: Account deletion — ctech-account implementation

- **Status:** Draft (2026-10-06)
- **Parent:** [Account deletion overview](2026-10-06-account-deletion-overview.md)
- **Projects:** `api/`, `ui/`, `cdk/`
- **Depends on:** [saga protocol](2026-10-06-account-deletion-saga-protocol.md),
  [data inventory §3](2026-10-06-account-deletion-data-inventory.md)

## 1. Scope

ctech-account is the entry point and orchestrator. This spec covers the request/cancel
flow, anti-fraud, the user lock, token cut-off, the orchestrator worker, the purge of
ctech-account's own data, the tombstone, UI, and infra.

## 2. New domain: `internal/domain/deletion`

- `model.go`: `Request`, `ServiceStatus`, states
  (`pending_deletion`, `locked`, `purging`, `blocked`, `purged`, `cancelled`), scopes
  (`account`, `service`).
- `repository.go`: table `{env}_account_deletion_requests` (schema in protocol §6),
  conditional transitions only.
- `service.go`: request, cancel, lock, dispatch, record ack, finish.
- Reuses: `session` service (revoke-all), `apikey`, `audit`, `email`, `cache` (Valkey),
  `organization` (ownership checks), `oauth/consent` (service unlink).

## 3. User state

`User.IsEnabled` already exists. Add `DeletionState string` (`""` | `pending` | `locked`
| `purged`) and `DeletionRequestID`. The **single gate** is in the code path that issues
tokens and sessions (login, MFA challenge, passkey login, refresh, authorization code
exchange, client credentials for API keys): any non-empty `DeletionState` other than
`pending`-with-cancel-scope is refused.

`pending` users may log in only to a **restricted session** (scope `account:deletion:cancel`)
that can call `GET /deletion` and `POST /deletion/cancel`, nothing else. This needs the same
MFA as a normal login.

## 4. API

All under `/v1.0/account`, self client only (`RequireClientID(SelfClientID)`), like the
other account-management endpoints.

| Method & path | Purpose |
|---|---|
| `GET /deletion/eligibility?scope=account\|service&service=wallet` | Fan-out to participants' eligibility endpoints + local blockers. Returns the aggregated blocker list for the UI. |
| `POST /deletion` `{scope, service?, confirmation_phrase}` | Create the request. Requires **step-up auth** (§5). Returns `request_id` (protocol number) and `grace_until`. |
| `GET /deletion` | Current request + per-service status (for the status page). |
| `POST /deletion/cancel` | Cancel during grace. Allowed from the restricted session or from the e-mail link. |
| `GET /deletion/confirm?token=` | E-mail confirmation link (see §5). |

Internal (client-credentials, protocol §3):

| Method & path | Purpose |
|---|---|
| `POST /internal/erasure/ack` | Participant acks. Scope `account:erasure:ack`; `service` must match the caller's client. |
| `GET /internal/erasure/purged-since?ts=` | Backup-restore replay list (protocol §8). |

Support/admin (gated by `SupportRole` = `admin`, reusing the support-tickets guard):

| Method & path | Purpose |
|---|---|
| `POST /admin/deletion/{request_id}/legal-hold` `{on, reason}` | Pause/resume. Needs **two different admins** to turn a hold off (four-eyes). |
| `POST /admin/deletion` `{sub, reason}` | Support-initiated deletion (e.g. a request received by e-mail to the DPO). Needs approval by a second admin before it enters `pending_deletion`. |
| `POST /admin/deletion/{request_id}/redrive` `{service}` | Re-dispatch a `failed` service after a fix. Never marks it acked. |

## 5. Anti-fraud on the request

1. **Step-up authentication**: password re-entry, or `auth_time` within the last 5 minutes
   (`max_age=300`), **plus** TOTP/passkey if MFA is enabled. Google-only users repeat the
   Google login (`prompt=login`).
2. **Typed confirmation** ("EXCLUIR MINHA CONTA") and an explicit checklist of
   consequences in the UI, listing per service what is erased vs retained (from the
   inventory).
3. **E-mail confirmation**: the request starts in `awaiting_confirmation` (≤ 24 h) and only
   enters `pending_deletion` after the link sent to the **verified e-mail** is clicked. A
   stolen session without e-mail access cannot delete the account. Unconfirmed requests
   expire silently.
4. **Notifications** on every channel at request, at 24 h before lock, at lock, at purge:
   e-mail and, if verified, SMS (existing `sms` package). Each contains the cancel link
   (until lock).
5. **Rate limits**: one open request per `sub` (conditional write); at most 3
   request/cancel cycles per 30 days (prevents using it to bounce sessions or spam
   notifications).
6. **Risk signals**: a request made within 24 h of a password reset, e-mail change, new
   device in a new country (geoip notification feature) or MFA removal keeps the same
   7-day grace (D8) but is **flagged for support review**: support must clear the flag
   before LOCKED (it behaves like a legal hold until cleared). A recent takeover is the
   scenario this guards against.
7. **Legal hold** blocks progression past LOCKED. The user sees that the request is
   suspended by a legal hold and how to reach support/DPO, never the reason (D9).
8. **Evasion**: wallet/billing blockers make "delete to dodge a debt or a chargeback"
   impossible; open fraud investigations use legal hold.

## 6. Lifecycle implementation

**On confirmation (enter `pending_deletion`):**
1. Conditional update request → `pending_deletion`, user → `DeletionState=pending`.
2. Revoke all sessions and refresh tokens (`session` revoke-all), disable API keys.
3. Write the revocation entry to Valkey (protocol §5).
4. Publish `user.locked` (participants block writes during grace too; cancel sends
   `user.unlocked`).
5. Audit event `account.deletion.requested`.

> Design note: participants are locked already in grace, not only at LOCKED, because
> grace is when a hijacked account would try to drain money.

**On cancel:** conditional `pending_deletion → cancelled`, user `DeletionState=""`,
publish `user.unlocked`, audit, notify. Revoked sessions stay revoked; the user logs in
again normally.

**Worker tick (grace expired):**
1. Re-run eligibility for every target service + local blockers.
2. Blockers → `blocked`, notify the user with the list, stop. A locked user cannot fix a
   blocker (no tokens, wallet refuses withdrawals), so the only way out is **cancel**
   (allowed in `blocked` because nothing was erased yet): everything unlocks, the user
   fixes the blocker, then files a new request with a new 7-day grace.
3. No blockers → `locked` (irreversible), `DeletionState=locked`, audit. In the same
   conditional write, freeze `organizations[]`: every organization the user owns where
   they are the **only member** (overview D4). Re-checked here because a member may have
   joined or left during grace. An invitation accepted during grace is impossible
   (the account is locked), but invitations **sent** by the user are revoked at
   `pending_deletion` so no one joins an org that is about to be erased.
4. → `purging`: create `SVC#` rows, publish `user.erase` (with `organizations[]`).

**On all acks:** for `account` scope, purge ctech-account itself (§7), then
`purged`. For `service` scope, revoke that client's consent grant and sessions for that
client, mark `purged` (account stays usable).

**Worker:** runs inside the existing API process as a leader-elected background loop
(`ctech-go-common/lock`, same approach as other periodic jobs), polling
`gsi_state_due`. No new compute. `ponytail:` single loop, move to a scheduled Lambda only
if the API ASG scaling makes leader election noisy.

## 7. Purging ctech-account's own data

Last step, only after every participant acked, following inventory §3 in this order
(each step idempotent):

1. Organizations in the frozen `organizations[]`: erase the organization item, its
   memberships, invitations and company links; erase a company (CNPJ) row only when no
   other organization references it (inventory §3). Irreversible, no retention (D4).
   Their DF-e data was already erased by dfe before its ack.
2. Memberships, invitations, consents, sessions, MFA, passkeys, API keys: delete by
   `USER_{sub}` / GSIs.
3. Support tickets: anonymize.
4. Audit rows: anonymize IP/UA/geo, keep `account.deletion.*` events.
5. KYC (retained, D6): write the `KYCRET#{sub}` retention item (TTL = `purged_at` + 5 y)
   **before** stripping the user item; copy KYC documents to `retention/{sub}/` with the
   `retain-until` tag, then delete all versions of the originals. Copy-then-delete is
   idempotent: re-running skips objects already present in `retention/`.
6. Valkey keys for the user.
7. Write tombstone in `account_users`: replace the user item with
   `{pk, deletion_state: "purged", purged_at, deletion_request_id, cpf_hmac,
   tos_version, tos_accepted_at, privacy_version, privacy_accepted_at}` — every other
   attribute removed. Keeping the item under the same `pk` means any code path that loads
   the user sees `purged` and refuses. The e-mail GSI entry disappears, so the e-mail can
   register again immediately (D10).

**Re-registration with the same CPF:** KYC Basic computes `HMAC(cpf)` and checks it
against tombstones (new GSI `gsi_cpf_hmac`). A match does not block registration; it is a
risk signal for KYC (`risk` domain) and blocks only while the old request is under legal
hold. The HMAC key lives in KMS/SSM and is never logged.

## 8. UI (`ui/`)

- Settings → "Privacidade e dados":
  - "Sair de um produto" (one card per linked product, from consents) → service unlink.
  - "Excluir minha conta" → full deletion.
- Flow: eligibility screen (blockers with action links, e.g. "Sacar saldo") →
  consequences screen (erase vs retain per product, from the inventory, plain language,
  with retention periods) → step-up auth → typed confirmation → "check your e-mail".
- The consequences screen lists **by name** every organization that will be erased
  (single-member ones) with a "Baixar XMLs" action per organization (dfe export) and a
  warning that the fiscal documents cannot be recovered. Organizations with other
  members show as blockers with a "Transferir titularidade" action.
- The "Sacar saldo" blocker action opens the wallet withdrawal with the full balance
  pre-filled; the wallet waives its product minimum for users with an open request (D7).
- Status page with the protocol number and per-product progress.
- Restricted "your account is scheduled for deletion on X — cancel?" screen for
  `pending` logins.
- Errors from blockers use the shared problem/error components (check `ctech-ui` first).
- New privacy policy version (`ui/src/lib/legal-documents.ts` pattern) describing grace,
  retention and processors.

## 9. Infra (`cdk/`)

- DynamoDB `{env}_account_deletion_requests` (PITR on in prod, GSIs `gsi_sub`,
  `gsi_state_due`); GSI `gsi_cpf_hmac` on `account_users` (sparse).
- SNS topic `{env}-account-user-erasure`, publish allowed only for the account instance
  role; participants' queues subscribe via cross-stack/SSM-exported topic ARN (same
  SSM parameter convention the family already uses for shared ARNs).
- IAM: `s3:ListBucketVersions` + `s3:DeleteObjectVersion` on the KYC bucket;
  `kms:GenerateMac` for the CPF HMAC key.
- Alarms: request in `purging` > 5 days, service not acked > 48 h, worker not ticking.
- Log retention ≤ 90 days on all account log groups (inventory §2).

## 10. Observability & audit

Audit events: `account.deletion.requested|confirmed|cancelled|blocked|locked|purging|
service_acked|service_failed|purged|legal_hold_on|legal_hold_off|support_initiated`.
Metrics: requests by state, time-to-purge, acks by service, DLQ depth (participants).

## 11. Testing

- Unit: state machine, every transition is conditional (races: cancel vs lock, double
  worker).
- Handler tests: step-up required; restricted session can do nothing but cancel; token
  issuance refused for every non-active state across all grant types.
- Integration (DynamoDB local + fake participants): happy path, blocked at LOCKED, a
  participant that never acks (reconciler backs off, alarm metric), a participant acking
  twice, purge re-run after a crash at each step of §7.
- `jwtverify` revocation: token issued before lock rejected, token after a cancel and
  re-login accepted.

## 12. Documentation updates required with the implementation

`api/ENDPOINTS.md` (new endpoints), `README.md`/`PLAN.md` (feature entry),
`docs/resource-server-scope-registry.md` (new scopes `account:erasure:ack`,
`erasure:eligibility`, `account:deletion:cancel`), the privacy policy, and each
participant repo's docs for its consumer and runbook.
