# Data export — end-to-end test in prod

Spec: `docs/specs/2026-10-17-data-export.md`. Run with **one throwaway account that has a little data in every product**
(see "Seed"), never a real customer's. All commands assume `AWS_PROFILE=ctech AWS_REGION=us-east-1`.

State when this was written (2026-10-17): the account API, billing, dfe, poker and wallet are deployed; the four are marked
`"export": true` in SSM `/ctech-account/prod/erasure-participants` (v7) and hold `internal:account:export-ack`; the four DLQs
(`prod-ctech-{billing,dfe,wallet}-erasure-dlq`, `prod-poker-user-erasure-dlq`) were empty; every subscription on
`prod-account-user-erasure` has a `{"services":["<name>"]}` filter and raw delivery.

## 0. Seed (so each part has something to show)

Sign in to <https://accounts.aoctech.app> with the test account and make sure it has:

| Product | Minimum data |
|---|---|
| account | a second session (another browser), an API key, a connected app if any, a few activity rows |
| wallet | a wallet with at least one ledger entry (a sandbox purchase is enough; avoid real money) and a self-exclusion or a limit set |
| poker | played at least one sandbox hand (so `player_hands` has a row) and an avatar or profile edit |
| billing | opened the billing portal once (tenant-zero customer) and, if possible, created a bill or an account in the personal finance space (live or test) |
| dfe | opened DF-e once (user row) and is a member of a company (`memberships.json`) |

A product with no data simply produces no file; the part is still `ready`. That is a valid result, but then that product is not tested.

## 1. Before you start (2 minutes)

1. Get the test account's `sub`: signed in, open the browser devtools, `GET /v1.0/userinfo`, copy `sub`. Export it: `SUB=<sub>`.
2. Baseline of the four DLQs, expect all `0`:
   ```bash
   for q in prod-ctech-billing-erasure-dlq prod-ctech-dfe-erasure-dlq prod-ctech-wallet-erasure-dlq prod-poker-user-erasure-dlq; do
     echo -n "$q: "; aws sqs get-queue-attributes --queue-url https://sqs.us-east-1.amazonaws.com/868899309401/$q \
       --attribute-names ApproximateNumberOfMessages --query Attributes.ApproximateNumberOfMessages --output text; done
   ```
3. The bucket prefix and the row do not exist yet:
   ```bash
   aws s3 ls s3://prod-ctech-account-data-exports/exports/$SUB/ --recursive      # empty
   aws dynamodb get-item --table-name prod_account_data_exports --key "{\"pk\":{\"S\":\"SUB#$SUB\"},\"sk\":{\"S\":\"CURRENT\"}}"   # {}
   ```

## 2. Happy path

1. Open `/account/export`. Click **Solicitar cópia**, enter the password (or use a fresh MFA step-up), submit.
   - Expect: `202`, the page shows "Estamos preparando a sua cópia…" and polls every 5 s.
   - Expect an e-mail "Exportação dos seus dados solicitada" **without any link to a file**.
2. Within ~1 minute the page should list the parts. Watch the row while you wait:
   ```bash
   aws dynamodb get-item --table-name prod_account_data_exports --key "{\"pk\":{\"S\":\"SUB#$SUB\"},\"sk\":{\"S\":\"CURRENT\"}}" \
     --query 'Item.{state:state.S,parts:parts.M}' --output json
   ```
   Expected sequence: `state=building`, `parts.account=building` and `billing|dfe|poker|wallet=dispatched` (attempts 1) → `account=ready`,
   `state=ready` → each product flips to `ready` as it acks (`size` > 0, set from the bucket).
3. When all five are `ready` the page shows five download buttons and an "Disponível até" date (7 days). Expect the second e-mail
   "Sua exportação de dados está pronta" **only after the last part is final** (not when the account part alone is ready), and exactly once.
4. Files are in the bucket:
   ```bash
   aws s3 ls s3://prod-ctech-account-data-exports/exports/$SUB/ --recursive
   # expect exports/$SUB/<exportId>/{account,billing,dfe,poker,wallet}.zip
   ```
5. Download through the page (the buttons use 15-minute presigned URLs) and unzip **each into its own new empty directory**
   (`mkdir /tmp/x-account && cd /tmp/x-account && unzip ~/Downloads/account.zip`). Do not unzip anywhere that holds scripts.

## 3. What each zip must contain (and must not)

| Part | Expect | Must NOT contain |
|---|---|---|
| `account.zip` | `profile.json` (email, names, CPF, address if filled, KYC level, terms versions), `sessions.json` (the two sessions), `api-keys.json` (name, prefix, scopes), `consents.json`, `activity.json`, `README.txt` | the password hash, any `refresh_token_hash`, an API key's hash or secret, a KYC document's object key |
| `billing.zip` | `customer-live.json` / `customer-test.json` (customer, `tax_id`, subscriptions, invoices) and `finance-{live,test}/{table}.json` for the personal space | another person's rows, anything from a workspace, `byok`/`idempotency` files, any `*secret*`/`*token*` attribute |
| `dfe.zip` | `user.json`, `memberships.json`, `billing.json`, `audit-logs.json` | `modifications` inside audit rows, company documents/certificates, another member's e-mail |
| `poker.zip` | `player_profiles.json`, `player_hands.json` (own hole cards, `commit_hash`/`server_seed`), stats, sessions, achievements, … | opponents' **names/avatars/chat** (they must appear only as ids), `avatar_reporters`, notes others wrote about you |
| `wallet.zip` | `wallets.json`, `ledger-<type>.json` (the whole statement), `deposits.json`, `withdrawals.json`, `settings.json` (self-exclusion/limits), `audit.json` | any `idempotency_key` / `request_hash`; on a deposit that is not `confirmed`, `payer_cpf`/`payer_name` |

Quick scans (run per directory, expect no hits):
```bash
grep -rli -E 'password_hash|refresh_token_hash|key_hash|idempotency_key|request_hash|avatar_reporters' /tmp/x-*/ || echo "clean"
grep -rn '"modifications"' /tmp/x-dfe/ || echo "clean"
```
Cross-check against the product UI: the wallet ledger entry count equals the statement the wallet app shows; the poker hand ids match
"Meu histórico"; the billing customer's CPF matches what you registered.

## 4. Audit trail and links

1. `/account/activity` shows `account.export.requested`, `account.export.ready`, and one `account.export.link_issued` for each time the
   page showed the links (every status read of a ready export mints new links).
2. Wait 16 minutes and click a button from a page you did not reload: the S3 URL must answer `403` (expired). Reload the page: new working buttons.
3. Open a button's URL in a private window: it works (the URL is the credential; it is only ever shown to the signed-in person and never e-mailed).

## 5. Negative and edge cases

| # | Do | Expect |
|---|---|---|
| a | Request again right away | `429` "uma cópia a cada 24 horas" (the page keeps the ready export) |
| b | **Descartar** the ready export, then request again | files gone from the bucket, row `state=expired`, and the new request still `429` (discard does not reset the 24 h limit) |
| c | Request with a stale token and no password | `403 step-up-required`; with a wrong password `401`; 6 wrong passwords in a row `429` |
| d | Call `POST /v1.0/account/export` with a third-party OAuth client's token (or an API key) | `403` (self client only) |
| e | Duplicate/late ack: pick a service's ack from its log and replay is not needed; instead re-run step 2 after (b) and confirm no old `exportId` objects reappear | only the new `exportId` prefix grows |
| f | Locked account: do this only on a **second** throwaway account with a deletion request confirmed (in grace) | request is refused (`409`), no row |

Case (b) needs 24 h to repeat the happy path again on the same account; use a second account if you want to retest the same day.

## 6. Logs and queues to read when something is off

```bash
# did the participant receive and answer? (look for dataexport / export lines)
aws logs tail /ctech-poker/prod/app   --since 20m --filter-pattern dataexport
aws logs tail /ctech-billing/prod/app --since 20m --filter-pattern dataexport
aws logs tail /ctech-dfe/prod/app     --since 20m --filter-pattern dataexport
aws logs tail /ctech-wallet/prod/app  --since 20m --filter-pattern dataexport
aws logs tail /ctech-account/prod/app --since 20m --filter-pattern dataexport     # dispatch/ack side
# the DLQs (section 1) must still be 0; a part stuck in "dispatched" with a non-empty DLQ is the usual symptom of a bad scope or a 4xx on the ack
```

| Symptom | Likely cause | Fix |
|---|---|---|
| A part stays `dispatched` and the DLQ grows | the service's ack client lacks `internal:account:export-ack` (ack answers 401/403) | `createclient -grant -client-id <id> -scopes internal:account:export-ack`; the account re-asks after 20 min (up to 6 times) while the page is open |
| `dispatched` and nothing in the service log | the message never reached its queue: SNS filter, or `"export": true` missing, or the API was not restarted after the SSM change | compare the subscription filter (section 1), re-read the SSM value, restart `app` then `app2` |
| `dataexport: put url expired, dropping` in a service log | the queue sat longer than 15 minutes | harmless; the account re-asks with a fresh URL |
| `dataexport: refusing put url outside https S3` | a message with a non-S3 URL reached the service | **stop and investigate**: the queue policy should make this impossible |
| `dataexport: upload rejected: status 403` | presigned URL signed for the wrong content type or expired mid-upload | retry (redelivery); if persistent, check the account role's `s3:PutObject` on `exports/*` |
| a part `failed` after 24 h | the service could not build/upload for a day | read its log, fix, then ask for a new copy (a failed export can be retried immediately) |
| `ack` answers `409` "not in the bucket yet" | the upload did not land | the service redelivers and uploads again; if it loops, check its egress to S3 (poker is IPv6-only: the presigned host must be the dual-stack one) |

## 7. Pass criteria

- All five parts reach `ready`; two e-mails arrive (requested, ready), neither with a link; the activity page shows the three event types.
- Every "must contain" is present and every "must NOT" scan is clean.
- The four DLQs are still `0`.
- Cases (a)–(d) behave as listed.

## 8. Rollback and cleanup

- **Stop asking a product:** remove `"export": true` from its entry in `/ctech-account/prod/erasure-participants` (the full JSON is one SSM value; keep every other field), then restart account `app`, then `app2` (25 s apart) with SSM RunCommand on the account instance. The export keeps working with the remaining parts.
- **Turn the whole feature off:** unset `EXPORTS_BUCKET` in the account's userdata (CDK) and redeploy; the routes are then not mounted.
- **Clean the test data:** use **Descartar** on the page (deletes the files, keeps a tombstone for the 24 h limit). The bucket also expires anything under `exports/` after 8 days.

## 9. Deferred check: the purge of a deleted account

A deletion has a 7-day grace, so the purge cannot be seen the same day. Do it once, with the throwaway account, after the walk-through:

1. Leave one export **un-discarded** (do not click Descartar), then request the account deletion and confirm it by e-mail.
2. After the purge finishes (the deletion request reaches `purged`; the grace is 7 days), check:
   ```bash
   aws s3 ls s3://prod-ctech-account-data-exports/exports/$SUB/ --recursive      # empty
   aws dynamodb get-item --table-name prod_account_data_exports --key "{\"pk\":{\"S\":\"SUB#$SUB\"},\"sk\":{\"S\":\"CURRENT\"}}"   # {}
   ```
3. If the purge stalls in account (`purging`), look at the account log for `dataexport: listing exports of the purged account` (an IAM `s3:ListBucket` problem on the exports bucket).
