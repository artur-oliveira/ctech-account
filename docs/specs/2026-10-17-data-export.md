# Spec: Personal data export (LGPD art. 18, V — portability)

- **Status:** Draft (2026-10-17)
- **Projects:** `ctech-account` (api, ui, cdk), `ctech-go-common` (new `dataexport` package), `ctech-wallet`, `ctech-dfe`, `ctech-billing`, `ctech-poker`
- **Parent:** [Account deletion overview](2026-10-06-account-deletion-overview.md) § 8, which names this feature as a sibling that "should ship before or with" deletion.
- **Plan:** none yet; write it after the open questions in § 9 are answered.

## 1. Outcome

A person can ask for a copy of their personal data in every CTech product and in the account, in a structured, machine-readable form,
and download it. It is also offered on the screens of account deletion and of leaving a product ("baixar meus dados antes").

## 2. Decisions (proposed, to be confirmed)

| # | Decision | Why |
|---|---|---|
| E1 | **Same orchestration shape as the erasure saga:** account owns a durable request with a per-service status; each participant does its own part and says when it is done. | The saga's failure handling (a service down for hours, redelivery, ack) is already proven and tested. |
| E2 | **No personal data on the wire.** The message carries only the `sub`, the request id and, per service, **a presigned S3 PUT URL** (short-lived) for that service's part. The participant uploads its file there. | SNS/SQS messages must stay free of personal data (overview decision 5), and parts can be large. |
| E3 | **One file per product, no re-zip.** Each part is `{service}.zip` containing JSON (full fidelity) and CSV (tables), plus a `README.txt` explaining the fields. Account adds its own part (`account.zip`). The export page lists the parts with presigned GET links, valid 7 days; the objects are deleted after 14 days (S3 lifecycle). | Re-zipping costs time and memory for no gain; per-product files also let a slow product not block the rest. |
| E4 | **Scope of a part:** the person's own data and what they produced, as a data subject: profile, addresses, consents, sessions list, audit, KYC status, their transactions, their orders/invoices, their game history, their certificates metadata. **Not** other people's personal data (counterparties in a ledger entry appear as an opaque reference, a recipient on an NF-e is part of the fiscal document the issuer controls). Secrets, password hashes, TOTP seeds, API key hashes and private keys are never exported. | Portability is the subject's own data. |
| E5 | **KYC documents:** metadata and a presigned link to the person's own uploaded images, inside the account part, valid as long as the export. | They are the person's own data and the most-asked item. |
| E6 | **Safeguards:** recent MFA or password (same check as deletion), one open export per person, at most one new export per 24 h, an e-mail on request and on ready (the e-mail never contains the link, only "it is ready in your account"), audit rows `account.export.{requested,ready,downloaded}`, the links are only shown to the signed-in person (never e-mailed). | A stolen session must not be able to quietly take the whole life of a person out. |
| E7 | **Not blocked by, and not blocking, deletion.** A locked or pending-deletion account can still request an export while signed in (during grace the person can sign in only to cancel; export from that state is **not** offered, to keep the lock simple). | Keep the lock rules unchanged. |
| E8 | **A participant that cannot produce its part in time** (24 h) is shown as "delayed" with a retry; the rest stays downloadable. The request is never auto-closed as complete with a missing part. | Silence is "not done", as in the saga. |

## 3. Protocol (new, in `api-commons/dataexport`)

```
account ── user.export {request_id, sub, service, put_url, expires_at} ──▶ SNS (filter: services) ─▶ participant queue
participant: builds its part for sub  ─▶ PUT put_url  ─▶ POST /v1.0/internal/export/ack {request_id, service, result, size, ts}
account: per-service status (dispatched / ready / failed) → export page; reconciler re-publishes to silent services (same backoff as erasure)
```

- Reuses the same SNS topic and the same per-participant queue **only if** the message type is distinguishable and the filter policy still
  routes by service; otherwise a sibling topic `{env}-account-user-export` with the same CDK construct. (Open question 2.)
- Participant side: a `PartFunc(ctx, sub) (io.Reader, error)` handed to a shared `Consumer`, like `PurgeFunc`; the package handles
  the PUT, retries, idempotency (a redelivery overwrites the same key) and the ack.
- Scopes: `internal:account:export-ack` (participants' dedicated client, like erasure-ack), and `internal:{service}:export` is **not** needed
  because account never calls the participant's API: it uses the queue.

## 4. Account surfaces

- `POST /v1.0/account/export` (step-up, owner only), `GET /v1.0/account/export` (status, per-service, links when ready), `DELETE` to discard early.
- UI: Settings → "Privacidade e dados" → "Baixar meus dados", the per-product status list, and links in the deletion consequences screen.
- Table `{env}_account_data_exports` (request + per-service rows, TTL 30 days), S3 bucket `{env}-account-exports` (private, SSE-S3, lifecycle 14 days, no public access).

## 5. Per-product parts (what each owner must list)

Each participant plan lists its tables against the data inventory (`2026-10-06-account-deletion-data-inventory.md`): the **exported** set is the
inventory's "erase" and "anonymize" rows that concern the person, plus the "retain" rows the person has a right to see. A test per participant
asserts that every table in its inventory is classified as exported or excluded with a reason (the same classification test pattern as the
purge), so a new table cannot be added without choosing.

## 6. Retention and security

- Exports are personal data at rest: SSE, no public ACL, access only through presigned URLs minted per request for the signed-in owner.
- Presigned PUT urls: 15 minutes, one object key, content-length limit.
- The export is itself covered by the deletion saga: when an account is purged, its export objects and rows are deleted with it (account part of the purge).

## 7. Out of scope

- Export of other people's data, or of data of organizations (a company's documents are the company's; dfe already offers the XML zip per organization).
- A machine-to-machine export API for third parties (portability to another controller on request is handled case by case by the DPO).
- Importing data back.

## 8. Rollout

1. `api-commons/dataexport` + account request/status + the account part, with **no services subscribed** (proves request → part → download for the account alone).
2. Participants one at a time: poker → billing → dfe → wallet (simplest to hardest), each as its own plan.
3. The deletion and leave-a-product screens link to the export once at least the account part exists.

## 8.1 Status

Step 1 (account part) is implemented: `api/internal/domain/dataexport` (model, service, repository, zip
builder with the section registry), routes `POST/GET/DELETE /v1.0/account/export`, scope
`account:export:write`, e-mails without links, UI `/account/export`, CDK `ExportsStack` (bucket, 8-day
lifecycle, no versioning, no CORS) and `{env}_account_data_exports`. The defaults proposed below were taken:
JSON per product plus a README, 1 export per 24 h (a failed one retries at once), no export while the
account is locked or pending deletion. The `api-commons/dataexport` protocol and the product parts are
still to do (step 2).

## 9. Open questions

1. **Format:** JSON + CSV per product is proposed. Is a single standard schema across products wanted (more work, easier for the person), or per-product schemas with a README each?
2. **Topic:** reuse `{env}-account-user-erasure` with a new message type, or a sibling topic? (A sibling is cleaner for IAM and alarms; reuse is less infrastructure.)
3. **Wallet ledger:** a transaction has a counterparty. Export the counterparty's opaque id only, or also the display name the person saw? (Legal.)
4. **Frequency and size limits:** 1 per 24 h and a size cap per part are proposed; confirm.
5. **Export during grace (E7):** keep "not offered while pending deletion", or allow it because the person has the strongest reason to take their data then?
