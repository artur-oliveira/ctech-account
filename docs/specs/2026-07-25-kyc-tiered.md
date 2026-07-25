# Spec: Tiered KYC (Basic + Enhanced) with Phone Verification and Risk Scoring

- **Status:** Approved (2026-07-25)
- **Project:** ctech-account (Go OAuth/OIDC IdP) + `ui/` (Next.js) + `cdk/`
- **Supersedes:** `docs/specs/2026-07-15-kyc-manual.md` (manual-only, single-tier `""`/`verified` KYC)

## 1. Background

The current KYC (`docs/specs/2026-07-15-kyc-manual.md`) collapsed everything into a single
document-review gate: `kyc_level` is `""` until a human approves six uploaded files
(id front/back + four head-turn selfie clips), then `verified`. This is too rigid —
`ctech-wallet` already ships `RequireKYC(KYCBasic)` / `RequireKYC(KYCVerified)` gates and a
`Claims.KYCLevel` contract of `""|"basic"|"verified"` (`ctech-wallet/api/internal/middleware/scope.go`),
expecting an intermediate tier that `ctech-account` never actually emits. This spec splits KYC
into two independently-reachable levels, matching what the wallet already expects, and adds a
lightweight, currently-informational risk-scoring hook for future fraud signals.

No production KYC data exists yet — this is a clean schema redesign, not a migration.

## 2. Goals

1. Split KYC into two levels: **Basic** (CPF + name + birth date + phone, SMS-verified) and
   **Enhanced** (identity document + selfie holding it, human-reviewed) — Enhanced requires
   Basic to be verified first.
2. Add mandatory SMS phone verification (AWS SNS) to Basic.
3. Drop `address` and the four-clip video selfie entirely from KYC. Enhanced document set
   shrinks to three files: `id_front`, `id_back`, `selfie_with_document` (single static photo).
4. Reuse the `kyc_level` JWT claim contract ctech-wallet already implements
   (`""|"basic"|"verified"`) — **zero changes to ctech-wallet**.
5. Add a `risk` domain package: a pluggable `Evaluator` producing a score + signals snapshot,
   stored on the user record and surfaced to the human reviewer via `cmd/kyc show`. No behavior
   gate yet — informational only.

## 3. Non-goals

- **No real risk detectors.** VPN/Tor IP-reputation, multi-account correlation, and behavioral
  "suspicious activity" rules are not implemented — only the `Evaluator` interface and storage
  exist. Default implementation is a no-op returning a zero score.
- **No automatic gating on risk score.** It never blocks or holds a submission; only visible to
  the reviewer.
- **No data migration.** No production users exist under the old scheme.
- **No change to ctech-wallet or ctech-dfe code.** Wallet's existing `""|"basic"|"verified"`
  claim contract already matches this design; dfe does not gate on `kyc_level` at all.
- **No automated liveness detection** for the Enhanced selfie — same as before, the human
  reviewer judges real-vs-photo; the difference is it's now one static photo instead of four
  video clips.

## 4. Levels, status, and derived state

```
KYCLevel:  "" (none) | "basic" | "enhanced"
KYCStatus: "" | "pending" | "verified" | "rejected"   (rejected only reachable from enhanced)
```

`GET /account/kyc` derives a single `state` the UI branches on:

| `state`                        | level / status        | financial access                  |
|---------------------------------|------------------------|------------------------------------|
| `not_started`                    | none / —               | none                               |
| `awaiting_phone_verification`    | basic / pending        | none (SMS not yet confirmed)       |
| `basic_verified`                 | basic / verified       | deposit + games                    |
| `under_review`                   | enhanced / pending     | deposit + games (basic retained)   |
| `rejected`                       | enhanced / rejected    | deposit + games (basic retained)   |
| `verified`                       | enhanced / verified    | + withdrawal                       |

### State machine

```
none ──POST /kyc/basic (valid CPF/age, SMS sent)──► basic/pending
basic/pending ──POST /kyc/basic/verify-phone (correct code)──► basic/verified
basic/pending ──POST /kyc/basic/resend-code (60s cooldown)──► basic/pending (new code)

basic/verified ──upload id_front+id_back+selfie_with_document, then POST /kyc/enhanced──► enhanced/pending
enhanced/pending ──cmd/kyc approve──► enhanced/verified
enhanced/pending ──cmd/kyc reject (documents cleared)──► enhanced/rejected
enhanced/rejected ──re-upload 3 docs + POST /kyc/enhanced──► enhanced/pending
enhanced/pending, 30d expiry with no reviewer action ──► reads back as basic_verified (re-submit re-queues)
```

Basic never regresses once verified — rejection/expiry only ever affects the Enhanced
submission on top of it.

## 5. JWT claim mapping — reuses ctech-wallet's existing contract

`kyc.ClaimLevel(level, status string) string` in `api/internal/domain/kyc/model.go`:

```go
func ClaimLevel(level, status string) string {
	switch {
	case level == LevelEnhanced && status == StatusVerified:
		return "verified"
	case level == LevelBasic && status == StatusVerified:
		return "basic"
	case level == LevelEnhanced:
		return "basic" // pending or rejected enhanced still keeps basic access
	default:
		return ""
	}
}
```

Called from the three existing choke points that currently read `u.KYCLevel` directly:
`handler/token.go:kycClaimFor`, the inline refresh-grant block (`token.go:~385`), and
`handler/userinfo.go:41-42`. No other caller reads `KYCLevel` for the claim.

`ctech-wallet` requires **no changes** — its `Claims.KYCLevel` / `RequireKYC` already treat
`"basic"` as "any verification started" and `"verified"` as "fully verified"
(`ctech-wallet/api/internal/middleware/scope.go:54-74`).

## 6. Data model (`user.User`)

Removed: `Address` field and struct (`kyc/address.go`, `ValidateAddress`, `NormalizeAddress`
deleted), the four `selfie_{up,down,left,right}` document types.

```
kyc_level              "" | "basic" | "enhanced"
kyc_status             "" | "pending" | "verified" | "rejected"   // renamed from kyc_doc_status
kyc_basic_verified_at  RFC3339 | ""   // set once, never cleared
kyc_submitted_at       RFC3339        // timestamp of whichever level is currently pending
kyc_expires_at         RFC3339        // stale enhanced/pending unlocks re-submission (unchanged 30d TTL)
kyc_verified_at        RFC3339 | ""   // enhanced verified timestamp
kyc_rejection_reason   string         // enhanced only
kyc_documents          [ {id, type: "id_front"|"id_back"|"selfie_with_document", key, uploaded_at} ]
phone_number           string         // E.164, collected at Basic
phone_verified_at      RFC3339 | ""
kyc_risk_score         int
kyc_risk_signals       []string       // "name:detail" pairs, latest snapshot
kyc_risk_evaluated_at  RFC3339 | ""
cpf / legal_name / birth_date         // unchanged
```

No production data to migrate — the attribute rename (`kyc_doc_status` → `kyc_status`) and
document-type shrink are free.

## 7. API contract

| Method | Path                                | Change                                                                                     |
|--------|--------------------------------------|---------------------------------------------------------------------------------------------|
| `GET`  | `/account/kyc`                       | response gains `phone_masked`, `basic_verified_at`; drops `address`                          |
| `POST` | `/account/kyc/basic`                 | **new**, replaces old `POST /account/kyc`. `{cpf, legal_name, birth_date, phone_number}` → validates CPF check digits/uniqueness + age ≥ 18, saves basic/pending, sends OTP |
| `POST` | `/account/kyc/basic/verify-phone`    | **new**. `{code}` → basic/verified                                                           |
| `POST` | `/account/kyc/basic/resend-code`     | **new**. 60s cooldown enforced via Valkey TTL                                                |
| `POST` | `/account/kyc/documents`             | `type` enum shrinks to `id_front\|id_back\|selfie_with_document`; requires basic/verified first |
| `POST` | `/account/kyc/documents/confirm`     | same enum change                                                                             |
| `POST` | `/account/kyc/enhanced`              | **new**, replaces old document-submit `POST /account/kyc`. Requires basic/verified + all 3 docs uploaded → enhanced/pending |
| `GET`  | `/internal/kyc/:user_id`             | gains `phone_number`, drops `address`                                                        |
| `cmd/kyc list/show/approve/reject`   | unchanged CLI shape; operates only on `enhanced/pending`; `show` prints risk score + signals |

All errors remain RFC 7807 via `apierror.*` + `problem.Send`. New constructors:
`KYCBasicRequired`, `KYCInvalidCode`, `KYCResendCooldown` (429, `Retry-After`),
`KYCPhoneVerificationUnavailable` (503).

## 8. Phone verification (SNS)

OTP: 6 digits, hashed (never plaintext) in Valkey `kyc_otp:{user_id}` (TTL 10min). Resend
cooldown `kyc_otp_resend:{user_id}` (TTL 60s). Attempt counter `kyc_otp_attempts:{user_id}`
(max 5; exceeding requires a fresh resend rather than a permanent lockout).

New `api/internal/sms` package (mirrors `internal/email`): `sms.Client.SendOTP(ctx, phoneE164,
code) error` via `sns.Publish` directly to the phone number (no topic). Gated by a new config
var `PHONE_VERIFICATION_ENABLED` (bool, default `false`) — mirrors how an absent
`KYC_DOCUMENTS_BUCKET` disables document verification. While `false`, `POST /kyc/basic`,
`/verify-phone`, and `/resend-code` return `503 KYCPhoneVerificationUnavailable` — a hard block,
not a silent-log fallback, since AWS SNS production SMS access is still pending. Flip to `true`
via env var once granted, no redeploy needed.

## 9. Risk scoring — structural hook only

New independent domain package `api/internal/domain/risk`:

```go
type Signal struct {
	Name   string // e.g. "vpn_tor", "duplicate_account", "suspicious_activity"
	Score  int
	Detail string // reviewer-facing note
}

type Assessment struct {
	Score       int
	Signals     []Signal
	EvaluatedAt string // RFC3339
}

type Evaluator interface {
	Evaluate(ctx context.Context, userID, ip string) (Assessment, error)
}
```

`NoopEvaluator` is the only implementation now: always returns `Assessment{Score: 0}`.
`ponytail: no real detectors (VPN/Tor, multi-account, suspicious activity) — swap in a concrete
Evaluator once criteria are defined; the interface already threads userID+ip so an IP-reputation
lookup for VPN/Tor plugs in without a signature change.`

`kyc.Service.Submit` (both the Basic and Enhanced submit paths) calls `Evaluate` and persists the
latest snapshot on the user record (`kyc_risk_score`, `kyc_risk_signals`, `kyc_risk_evaluated_at`)
— overwritten on every submission, no history kept. No threshold, no gate: `cmd/kyc show` prints
it for the human reviewer alongside CPF and documents. The IP comes from the existing
`clientIP(c)` helper (`handler/helpers.go`) already used by audit/session recording — reused, not
reimplemented.

Multi-account correlation and "suspicious activity" are documented target categories for a
future `Evaluator`, not empty code stubs — nothing pretends to detect them yet.

## 10. UI (`ui/`)

`/account/identity` rewritten around the 6-state machine: Basic form (CPF/name/birth
date/phone) → OTP entry screen → Enhanced document upload (3 files instead of 6).
`selfie-capture.tsx` (4-clip `MediaRecorder` video capture) is replaced by a single static-photo
capture. `kyc-document-upload.tsx`'s `type` union shrinks from 6 values to 3. Address fields are
removed from the form entirely.

`kyc-policy` (legal copy at `/kyc-policy`) needs a content rewrite for the new flow — likely
another ToS/Privacy version bump (`internal/legal/version.go`) since phone number is now
collected. Copy itself is out of scope for this spec; only the version-constant bump is a code
change.

## 11. cdk

`iam-stack.ts`: add `sns:Publish` to the instance role. Resource `*` is the documented AWS
pattern for direct-to-phone-number publish (no topic ARN exists to scope to) — called out here
so it doesn't read as a least-privilege violation. No new stack: SNS SMS needs no bucket/topic
resource. `README.md` First Deploy gains a note to set the account's SNS SMS spend limit once
(`aws sns set-sms-attributes`), outside CDK, when production access is granted.

## 12. Cross-project impact

- **ctech-wallet**: no code changes — its `""|"basic"|"verified"` claim contract already
  matches (§5).
- **ctech-dfe**: unaffected — does not gate on `kyc_level`.
- **cdk**: `sns:Publish` IAM permission only (§11).

## 13. Testing

- `kyc/service_test.go`: OTP generate/verify/resend/cooldown/max-attempts; `ClaimLevel` mapping
  table (all 6 level/status combinations); Enhanced submit requires basic/verified + 3 docs;
  Review (approve/reject) unchanged logic, now gated on enhanced level only.
- `risk/`: `NoopEvaluator` returns zero score; `Evaluator` interface satisfied by test doubles.
- `handler/kyc_test.go`: integration tests for every new/changed route and error mapping.
- `handler/token_test.go`, `handler/userinfo_test.go`: updated for claim derivation across all
  6 level/status combinations.
