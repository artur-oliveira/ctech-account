# Spec: Account deletion — erasure saga protocol

- **Status:** Draft (2026-10-06)
- **Parent:** [Account deletion overview](2026-10-06-account-deletion-overview.md)
- **Owner of the contract:** `ctech-go-common` (`erasure` package). ctech-account is the
  orchestrator. wallet, dfe, billing and poker are participants.

## 1. Why a protocol

Five services on DynamoDB, no distributed transactions. A deletion is correct only if
**every** participant finishes, and we need to **prove** it. The protocol gives:

- at-least-once delivery, idempotent and resumable execution,
- an explicit ack per participant, persisted by the orchestrator,
- a way to refuse up front (blockers) instead of failing halfway,
- protection against resurrection (tombstones) and against tokens still in flight
  (revocation list).

## 2. Participants and identity

- The only identifier exchanged is the account **`sub`** (the `user_id` in the JWT).
  Services that use their own internal id (e.g. dfe `USER_{uuid}`) resolve it locally.
- Each participant registers a **service id** (`wallet`, `dfe`, `billing`, `poker`) and
  its OAuth `client_id`(s). That mapping lives in ctech-account config, so the
  orchestrator knows which services to address for a `service`-scoped request.

## 3. Message flow

```
account                       SNS user-erasure topic           SQS <svc>-erasure (+DLQ)        <svc>
  │ GET  /internal/erasure/eligibility/{sub}  (sync, every svc) ─────────────────────────────▶ │
  │ ◀──────────────────────────────────────────────────── {eligible, blockers[]} ───────────── │
  │ publish user.locked {sub, request_id, scope, services[]} ─▶ fan-out ─▶ consume ─▶ block writes
  │ publish user.erase  {sub, request_id, scope, services[], attempt} ─▶ fan-out ─▶ purge(sub)
  │ ◀──────────── POST /internal/erasure/ack {request_id, service, result, counts, ts} ──────── │
  │ (reconciler re-publishes user.erase for non-acked services; same request_id)                │
```

- One **SNS topic** owned by ctech-account (`{env}-account-user-erasure`). Each
  participant owns its **SQS queue + DLQ** subscribed with a filter policy on
  `services` (so a `service`-scoped request only reaches that service).
- Messages are signed implicitly by IAM (only the account role can publish). The ack
  endpoint is authenticated with the participant's **client-credentials token** with
  scope `account:erasure:ack`. A participant may only ack for its own service id.

### Message schema (v1)

```json
{
  "version": 1,
  "type": "user.locked | user.erase | user.unlocked",
  "request_id": "01J...ULID",
  "sub": "user id",
  "scope": "account | service",
  "services": ["wallet"],
  "organizations": ["org id"],
  "attempt": 3,
  "issued_at": "2026-10-06T12:00:00Z"
}
```

`organizations` lists the organizations where the user is the **only member**. They are
erased in full with the user (overview D4). It is frozen when the request reaches LOCKED
(computed by ctech-account, which owns tenancy) and never recomputed by participants.
Participants that hold org-scoped data (today: dfe) erase everything keyed by those ids,
and the consumer tombstones each org (`ORG#{org_id}` in the erasure-state table, §4.5) so jobs and webhooks for it are dropped.
Empty for `service` scope.

No personal data, ever. `user.unlocked` is sent when a request is cancelled during grace
(for services that cached the lock).

## 4. Participant obligations

### 4.1 Eligibility endpoint

`GET /internal/erasure/eligibility/{sub}` (client-credentials, scope
`erasure:eligibility`), responding in under 2 s:

```json
{ "eligible": false,
  "blockers": [ { "code": "wallet.balance_nonzero", "detail": { "amount_cents": 1250 },
                  "action_url": "https://ledger.aoctech.app/withdraw" } ] }
```

- Blocker `code`s are stable and translated by the account UI.
- An unknown `sub` returns `eligible: true, blockers: []` (nothing to delete).
- Errors/timeouts are **not** "eligible". The orchestrator treats them as a transient
  blocker and retries.

### 4.2 Lock handler (`user.locked`)

- Persist the `sub` in the local erasure state (`LOCKED`) and refuse new state-changing
  operations for it, **especially money-moving ones**, even with a valid JWT.
- The lock state lives in the participant's `{prefix}_erasure_state` table (partition key
  `pk` only, TTL attribute `ttl`), whose schema and transitions are owned by
  `ctech-go-common/erasure.Store`. Each participant's infra creates that table.
- **Messages can arrive out of order** (standard SQS, retries, reconciler re-publishes).
  Each state record keeps the `issued_at` of the last lock/unlock it applied, and an
  older lock/unlock is ignored. `erased` is terminal: nothing after it changes the record.
  Without this, an `unlocked` delivered before its `locked` would leave a cancelled user
  locked forever.
- Inbound money that cannot be refused (a PIX deposit arriving) is accepted and turns into
  a blocker that the next eligibility check reports.

### 4.3 Purge handler (`user.erase`)

- Implements the service's section of the
  [data inventory](2026-10-06-account-deletion-data-inventory.md) exactly.
- **Idempotent**: running it twice, or after a crash at any point, ends in the same state.
  Erase = delete-if-exists. Anonymize = overwrite with deterministic values.
- **Resumable**: work in pages/batches (`BatchWriteItem` ≤ 25, `TransactWriteItems`
  ≤ 100). No step depends on in-memory state from a previous step. Re-query from scratch on
  retry.
- **Re-checks eligibility first.** If a blocker appeared, ack `result: "blocked"` with the
  blockers. The orchestrator decides; the participant never half-purges.
- S3: delete all versions + delete markers for the user's prefixes.
- Valkey: delete the user's keys.
- Writes the tombstone **before** acking.
- Acks with per-store counts (`{"wallet_users": 1, "ledger_entries_retained": 340}`), so
  the audit trail shows what happened.

### 4.4 Subject lookup (only participants that must match by CPF)

Messages never carry PII, but dfe must find **e-CPF certificates** whose holder CPF is
the user's (inventory §5, D14). It calls
`GET /internal/erasure/{request_id}/subject` (client-credentials, scope
`erasure:subject`, allow-listed per service — only `dfe` today), which returns
`{ "cpf": "..." }` **only while the request is in PURGING**, and writes an audit event per
call. The CPF is used in memory for matching and never persisted or logged.

### 4.5 Tombstone

Each participant's `{prefix}_erasure_state` table (owned by `erasure.Store`) holds one
record per key: `SUB#{sub}` and `ORG#{org_id}`, with `erasure_state`
(`active`/`locked`/`erased`), `request_id`, `seq_ns` (newest applied `issued_at`) and
timestamps, no PII. The `erased` TTL is the participant's longest retention (`NewStore`'s
`erasedTTL`; 0 keeps it forever). Every write path that creates per-user data calls
`Store.Blocked(sub)`, and every async consumer (webhooks, outbox, jobs) checks
`Blocked`/`OrgErased` and drops work for an erased key. This is what prevents
resurrection by late messages.

For `service` scope, a returning user is a new user: when the user re-consents through a
fresh OAuth flow, the service calls `Store.Clear(sub)`. `Clear` does **not** delete the
record. It reactivates it with a sequence newer than the old erase, so a late duplicate
of that erase (SQS duplicate, reconciler re-publish) is recognised as stale: the consumer
skips the purge and acks `done` instead of wiping the returning user's new data.

## 5. Token cut-off — revocation list in `jwtverify`

Access tokens are stateless RS256 JWTs with a 15-minute TTL. Without a check, a locked
user keeps working for up to 15 minutes, which is unacceptable for withdrawals.

- On lock, ctech-account writes `ctech:jwt:revoked_sub:{sub}` (value: cut-off unix
  seconds) with TTL = access-token TTL + clock skew (20 min, `jwtverify.RevocationTTL`),
  through `jwtverify.Revoke`. Cancel removes it (`jwtverify.Unrevoke`).
- **Which Valkey DB.** The family shares one Valkey server, but each service selects its
  own logical DB (`account`, `dfe`, `poker`: DB 0, the base URL; `wallet`: DB 2;
  `billing`: DB 3). A key written by account in DB 0 is invisible from DB 2. Revocation
  entries therefore live in **DB 0**, and services on another DB build a second
  `cache.RedisBackend` on the base URL (`/ctech/{env}/valkey/url` with no DB suffix) and
  pass it to `Verifier.WithRevocation`.
- `ctech-go-common/jwtverify` gains `Verifier.WithRevocation(cache.Backend)`. When set,
  `VerifyClaims` rejects tokens whose `sub` is in the list and whose `iat` is at or before
  the cut-off (a token without `iat` is rejected too). Cost: one Valkey GET per
  verification, the same order as the JWKS cache read already done per request.
- **Failure mode:** if Valkey is unreachable, `jwtverify` **fails open** by default (auth
  must not depend on cache availability). Money-moving endpoints call
  `VerifyClaimsStrict`, which **fails closed**, and also check the local lock state from
  §4.2. These are two independent layers.
- Refresh tokens are already revoked server-side by ctech-account (sessions erased), so no
  new access tokens can be minted.

## 6. Orchestrator state (in ctech-account)

Table `{env}_account_deletion_requests`:

| pk | sk | attributes |
|---|---|---|
| `REQ#{request_id}` | `META` | `sub`, `scope`, `services[]`, `state`, `requested_at`, `grace_until`, `locked_at`, `purged_at`, `legal_hold`, `requested_via` (self/support), `approved_by[]` |
| `REQ#{request_id}` | `SVC#{service}` | `status` (`pending`/`dispatched`/`acked`/`blocked`/`failed`), `attempts`, `last_dispatch_at`, `ack` (counts), `blockers[]` |
| GSI `gsi_sub` on `sub` | | at most one open request per `sub` (conditional put on `SUB#{sub}` / `OPEN` marker item) |
| GSI `gsi_state_due` on `state` + `next_action_at` | | drives the worker |

State transitions are **conditional writes** (`state = :expected`), so two workers or a
cancel racing with the lock cannot both win.

## 7. Retries and reconciliation

- SQS: visibility timeout ≥ 2 × the expected purge time, `maxReceiveCount` 5, then DLQ.
  DLQ depth > 0 raises an alarm (same pattern as dfe `worker-dlq` / poker DLQs).
- Orchestrator reconciler (every 15 min): for each `PURGING` request, re-publish
  `user.erase` to services not `acked` whose `last_dispatch_at` is older than the
  exponential backoff (15 min → 1 h → 6 h → 24 h, cap 24 h). Same `request_id`, `attempt+1`.
- Escalation: alarm at 48 h without ack per service; the request is never auto-closed as
  done. A human resolves `failed` (fix and redrive), not by marking it acked.
- A `blocked` ack during PURGING (rare, e.g. a deposit raced the lock) moves the request
  to `BLOCKED` and pauses the reconciler for it. The user **cannot** resolve it: they have
  no tokens, and cancelling is impossible after LOCKED because other services may already
  have erased. **Support resolves it** (typically the stray deposit is returned to the
  payer through a PIX refund) and redrives the service. Services already acked stay acked; their purge was idempotent and
  stays valid.

## 8. Backup restore runbook (each service)

Restoring a DynamoDB table from PITR/backup can bring back erased users. Every service's
restore runbook gets a mandatory step: replay erasure for every `request_id` whose
`purged_at` is after the restore point. ctech-account exposes
`GET /internal/erasure/purged-since?ts=` (client-credentials) to list them.

## 9. `ctech-go-common/erasure` package

Kept small (shipped in `ctech-go-common` v1.13.0). It provides:

- `Message` (schema v1), `Encode`/`Decode` (accepts raw delivery and the SNS envelope),
  `Blocker`, `Eligibility`/`NewEligibility`, `Ack`.
- `Store` over `{prefix}_erasure_state`: `Apply` (lock/unlock ordered by `issued_at`,
  `erased` terminal), `Blocked`, `OrgErased`, `Clear`.
- `Consumer`: SQS long-poll, **one message per receive** (each message's visibility clock
  starts at receive). It applies lock/unlock itself and calls the service's `PurgeFunc`
  for `user.erase` (after locking the sub, so a lost `user.locked` cannot leave writes
  open). It tombstones only after a `done` purge and deletes the message only after the
  ack POST succeeds. Lock/unlock need no service code, so there is no `Handler`
  interface; the eligibility endpoint is a plain HTTP handler returning `NewEligibility`.
- `AckClient`: POST to ctech-account using `oauth2client` client-credentials.
- `jwtverify`: `Verifier.WithRevocation`, `VerifyClaimsStrict`, `Revoke`/`Unrevoke` (§5).
- No orchestration logic: that stays in ctech-account.

ctech-billing (Terraform) and the TypeScript parts (if any) consume the same JSON contract;
the Go package is a convenience, the schema in §3 is the contract.

## 10. Testing

- Contract tests in `ctech-go-common/erasure`: decode/encode, consumer acks only after the
  handler succeeds, poison message goes to DLQ.
- Each participant: integration test (DynamoDB local) that seeds a user across all
  inventory tables, runs `Purge` twice, and asserts the inventory table exactly (erased
  rows absent, anonymized rows have no PII, retained rows untouched, tombstone present).
  Also a test that a write for a tombstoned `sub` is rejected.
- End-to-end in dev: one request per scope, plus chaos: kill a participant mid-purge and
  confirm the reconciler finishes it.
