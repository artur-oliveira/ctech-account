# Spec: Manual-only KYC with Selfie

- **Status:** Approved (2026-07-15)
- **Source plan:** `docs/plans/kyc.md`
- **Project:** ctech-account (Go OAuth/OIDC IdP) + `ui/` (Next.js)
- **Supersedes:** `docs/specs/2026-07-10-kyc-design.md` (PIX-based design, now dead — Pix webhook hides payer CPF)

## 1. Background

KYC via Pix deposit is **unsupportable by design**: the Pix webhook payload
(`E10573…`) carries only `infoPagador` (free text, e.g. `ARTUROLIVEIRA`),
never the payer CPF. The wallet therefore cannot match a deposit's CPF against the
declared one, so the PIX verification path (`MethodPIX` + `internal:kyc/confirm`)
is dead. KYC becomes **document-only, manually reviewed by a human**.

## 2. Goals (in scope)

1. Remove the automated PIX verification path and its `internal:kyc` service scope.
2. Collapse KYC levels to **`none` (`""`) or `verified`** — `basic` is deleted.
3. Make **four short head-pose selfie shots** (up/down/left/right, captured
   in-browser as short video clips) *required* submission artifacts — a single
   static photo is trivially defeated by a printed photo, whereas an on-command
   head turn is not, and this stays cheap (no server-side ML) because the
   *reviewer* still watches the clips and decides.
4. Move the human approve/reject decision out of the HTTP API into a **`cmd/kyc`** CLI.
5. Bump ToS + Privacy to **`3.0`** so existing users re-consent (biometric/selfie collection).

## 3. Non-goals (out of scope)

- **No automated liveness / anti-spoofing.** Liveness is *light*: a blink/head-turn
  hint may help the user aim the camera, but the **reviewer** judges real-vs-photo.
- **No new KYC OAuth scope.** The `kyc` OIDC claim scope stays (downstream
  `ctech-wallet`/`ctech-dfe` read `kyc_level` from tokens). Only the *service*
  `internal:kyc` scope is removed.
- **No admin UI.** Review happens in the terminal CLI; the reviewer opens presigned
  S3 URLs in a browser.
- **No PIX re-enablement.**

## 4. Functional requirements

| ID  | Requirement                                                                                                                                                   |
|-----|---------------------------------------------------------------------------------------------------------------------------------------------------------------|
| FR1 | `POST /account/kyc` (submit identity) is **rejected** unless `id_front`, `id_back`, and all four `selfie_{up,down,left,right}` clips are already uploaded for that user. |
| FR2 | A submission's `kyc_level` stays `""` until a reviewer approves; then it becomes `verified`. No intermediate `basic`.                                         |
| FR3 | `GET /account/kyc` returns the derived `state`: `not_started`, `awaiting_files`, `under_review`, `rejected`, `verified`.                                      |
| FR4 | `cmd/kyc list` shows pending (`under_review`) submissions; `show <id>` prints raw CPF + presigned doc URLs; `approve`/`reject` flip status.                   |
| FR5 | The raw-CPF read (`GET /internal/kyc/:user_id`) is retained for `ctech-wallet` withdrawal-key validation, re-guarded under `internal:wallet:confirm-deposit`. |
| FR6 | ToS and Privacy versions move `2.0` → `3.0`; users whose stored versions differ are re-gated on next `/authorize`.                                            |
| FR7 | All errors remain RFC 7807 `apierror.*` via `problem.Send`.                                                                                                   |

## 5. Data model (user DynamoDB item — KYC fields, unchanged shape)

```
kyc_level           "" | "verified"          // was: "" | "basic" | "verified"
kyc_method          "document"               // was: "" | "pix" | "document"  (now always "document")
kyc_doc_status      "" | "awaiting_files" | "pending_review" | "rejected"
kyc_submitted_at    RFC3339
kyc_expires_at      RFC3339                    // stale pending unlocks re-submission
kyc_verified_at     RFC3339 | ""
kyc_rejection_reason string
kyc_documents       [ {id, type:"id_front"|"id_back"|"selfie_up"|"selfie_down"|
                        "selfie_left"|"selfie_right", key, uploaded_at} ]
cpf / legal_name / birth_date / address        // unchanged
```

`kyc_level` is `""` throughout the pending phase; `state()` derives the UI state
from `kyc_doc_status` + `kyc_submitted_at`, **not** from `kyc_level`.

## 6. API contract (before → after)

| Method & path                          | Before                 | After                                             |
|----------------------------------------|------------------------|---------------------------------------------------|
| `GET /account/kyc`                     | status                 | **unchanged** (state derived w/o `basic`)         |
| `POST /account/kyc`                    | submit (pix\|document) | submit (**document only**; requires 3 docs)       |
| `POST /account/kyc/documents`          | presign upload URL     | **unchanged**                                     |
| `POST /account/kyc/documents/confirm`  | record upload          | **unchanged**                                     |
| `POST /internal/kyc/confirm`           | PIX CPF match (wallet) | **DELETED**                                       |
| `POST /internal/kyc/review`            | human decision (HTTP)  | **DELETED** → `cmd/kyc`                           |
| `GET /internal/kyc/:user_id`           | raw CPF (wallet)       | **kept**, scope `internal:account:kyc` |
| `GET /internal/kyc/:user_id/documents` | doc URLs               | **DELETED** (CLI uses `cmd/kyc show`)             |
| `internal:kyc` scope                   | service scope          | **DELETED**                                       |
| `kyc` OIDC scope                       | claim scope            | **kept**                                          |

## 7. KYC state machine (after)

```
            submit (3 docs present)
 none ──────────────────────────────► awaiting_files ──(docs uploaded)──► under_review
   ▲                                            │  │  reject                         │ approve
   │                                            │  └──────────────────────────────┘ └──────────► verified
   │ expired / rejected                                                                  │
   └───────────────────────────────────────────────────────────────────────────────────┘
```

Re-submission is allowed only from `rejected` or an expired `under_review`.

## 8. Selfie capture (frontend)

- New `<SelfieCapture/>` uses `getUserMedia({video:true})` and `MediaRecorder` to
  record **four short clips (~1–2s each)**, prompting the user in turn to turn their
  head **up, down, left, right**. Each clip is a small `video/webm` blob — no
  canvas/JPEG downscale step needed since these replace the single still photo.
- This *is* the liveness signal: a printed photo or a paused video cannot turn on
  command, so the reviewer watches the four clips instead of one flat frame. No
  MediaPipe/ML dependency — the human still makes the call, only with better
  evidence.
- Each clip uploads via the existing `uploadKYCDocumentAPI(file, type)` with
  `type` ∈ `selfie_up|selfie_down|selfie_left|selfie_right`. `IdentityForm` keeps
  Submit disabled until `id_front` + `id_back` + all four selfie clips are present
  in `status.documents`.

## 9. ToS / version bump

`internal/legal/version.go`: `CurrentToSVersion` and `CurrentPrivacyVersion` → `"3.0"`.
`PendingFor` already does exact-version match, so every stored `tos_version`/`privacy_version`
of `2.0` becomes pending and re-gated at next `/authorize` (the `ni`/`privacy` nibump).

## 10. Cross-project impact

- **ctech-wallet**: loses `/internal/kyc/confirm` (PIX — moot) and
  `/internal/kyc/:user_id/documents`; **keeps** `/internal/kyc/:user_id` (raw CPF) under
  `internal:account:kyc`. Wallet must stop calling the deleted endpoints.
- **ctech-dfe**: unaffected — still reads `kyc_level` claim (now only `""`/`verified`).
- **cdk**: no infra change (no new GSI; `cmd/kyc list` uses a Scan — see plan §1 note).
