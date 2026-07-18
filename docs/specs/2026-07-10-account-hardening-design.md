# Account Hardening — Design Spec

Date: 2026-07-10
Status: Approved (design), pending implementation plan
Scope: ctech-account (Go API + ui + cdk). Prerequisite work for the future ctech-wallet service.

Three features, independent but complementary:

- **A. Step-up authentication** — `auth_time`/`amr` claims + recent-MFA middleware.
- **B. Security audit log** — append-only auth event trail, user-visible + internal.
- **C. JWKS auto-rotation** — versioned keys in SSM, dual-KID JWKS, automatic ~90-day rotation.

Out of scope (decided): back-channel logout (consumers are stateless JWT verifiers today), KYC (separate spec), wallet
itself (separate spec).

---

## A. Step-up Authentication

### Goal

Sensitive operations (future: wallet withdrawal; present: password change, MFA removal, API key creation, OAuth client
creation) can demand that the user proved MFA **recently**, not merely that they hold a valid 15-min access token.

### Policy (decided)

- Users **without** any MFA method enrolled are **blocked** from step-up-protected operations and directed to enroll
  TOTP or a passkey. Re-typing the password is NOT accepted as step-up.
- Freshness window: default `max_age = 5 minutes` (constant, per-route overridable).

### Data model

`session.Session` gains:

| Field       | dynamodbav                               | Meaning                                                                           |
|-------------|------------------------------------------|-----------------------------------------------------------------------------------|
| `AuthTime`  | `auth_time` (int64 epoch)                | When the user actively authenticated (login). Set at `session.Service.Create`.    |
| `AMR`       | `amr` (string set, e.g. `["pwd","otp"]`) | Methods used at login: `pwd`, `otp` (TOTP), `swk`/`webauthn` (passkey), `google`. |
| `LastMFAAt` | `last_mfa_at` (int64 epoch)              | Last successful MFA proof (login MFA gate OR step-up challenge). 0 if never.      |

### Token claims

`SignAccessToken` gains `auth_time` (int64), `amr` ([]string) and `last_mfa_at` (int64, 0 if never) claims, sourced from
the session at token issuance (code exchange, refresh grant, api_key grant — api_key tokens carry no `amr`/`auth_time`,
they can never pass step-up). Values reflect the **session's** current state, so a step-up challenge followed by silent
token refresh yields a token that passes the check.

### Middleware

`middleware.RequireRecentMFA(maxAge time.Duration)` — runs after `RequireAuth`:

1. Reads `amr` + `auth_time`/`last_mfa_at` claims from verified token (claims exposed via a new `LocalAuthTime`/
   `LocalAMR` locals set in `extractAndVerify`).
2. If token has no MFA-capable `amr` and user has no MFA enrolled → `403` `apierror.MFAEnrollmentRequired` (new Problem
   type, `type: .../mfa-enrollment-required`), UI redirects to security settings.
3. If `last_mfa_at` older than `maxAge` → `403` `apierror.StepUpRequired` (new Problem type) with `max_age` in
   extensions. UI opens step-up modal.

Note: since claims come from the JWT, a fresh step-up requires the UI to do a silent token refresh after the challenge
succeeds (refresh grant re-reads session → new claims). This keeps the middleware stateless (no DynamoDB read per
request).

### Step-up challenge endpoint

`POST /v1.0/auth/step-up` (RequireAuth, session cookie not required):

- Body: `{ "method": "totp", "code": "123456" }` or passkey assertion (reuse existing WebAuthn begin/complete pattern:
  `POST /v1.0/auth/step-up/passkeys/begin` + `/complete`).
- On success: sets `session.LastMFAAt = now`, appends method to session `AMR`, writes audit event.
- Rate-limited same as login (Valkey counter, 5 fails / 15 min per user).

### Protected routes (initial set)

`RequireRecentMFA(5*time.Minute)` on: change password, TOTP remove, passkey remove, backup-code regenerate, API key
create, OAuth client create/update. Wallet service will enforce the same via claims (it reads `last_mfa_at`/`amr` from
the JWT — no call back to accounts needed).

### UI

- Step-up modal component (TOTP via existing `otp-input.tsx`, or passkey button) triggered by `step-up-required` Problem
  responses; on success → silent refresh → retry original request.
- No-MFA users: redirect to `/account/security` with an enrollment prompt banner.

---

## B. Security Audit Log

### Goal

Append-only trail of security-relevant events. Two consumers: the account owner (activity page — detect compromise) and
internal/forensic (full trail, queried via AWS tooling).

### Storage

New DynamoDB table `{env}_account_audit` (CDK dynamodb-stack):

| Attr                                         | Value                                                                                |
|----------------------------------------------|--------------------------------------------------------------------------------------|
| `pk`                                         | `USER_{userID}` (or `ANON_{ip}` for failed logins on unknown emails)                 |
| `sk`                                         | `EVT_{RFC3339Nano}_{shortRandom}` (chronological, collision-safe)                    |
| `event_type`                                 | constant string, see catalog                                                         |
| `ip`, `user_agent`, `geo_city`, `geo_region` | request context (reuse session geo enrichment)                                       |
| `metadata`                                   | map — event-specific (client_id, key_id, session_id, method…) — never secrets/tokens |
| `created_at`                                 | RFC3339                                                                              |
| `expires_at`                                 | Unix epoch, DynamoDB TTL = **400 days**                                              |

Query = `Query` on pk, `ScanIndexForward=false`, cursor pagination via `ExclusiveStartKey`. No GSI needed initially.

### Event catalog (constants in `internal/domain/audit`)

`login.success`, `login.failed`, `login.mfa_required`, `mfa.challenge.success`, `mfa.challenge.failed`,
`stepup.success`, `stepup.failed`, `password.changed`, `password.reset_requested`, `password.reset_completed`,
`email.verified`, `totp.enabled`, `totp.disabled`, `passkey.added`, `passkey.removed`, `backup_codes.regenerated`,
`apikey.created`, `apikey.revoked`, `oauth_client.created`, `oauth_client.updated`, `oauth_client.deleted`,
`consent.granted`, `consent.revoked`, `session.revoked`, `session.revoked_all`, `token.reuse_detected`, `social.linked`.

### Architecture

- `internal/domain/audit`: `Event` model, `Repository` interface (Put + QueryByUser), `Service.Record(ctx, evt)`.
- **Failure mode:** `Record` never fails the caller's request — errors are logged (structured) and swallowed. Audit
  write is synchronous (single PutItem, ~5ms) — no goroutine fan-out, keeps ordering and testability.
- Handlers call `audit.Service.Record` after the domain operation succeeds (or on auth failure paths). Injected into
  handlers like other services.

### API + UI

- `GET /v1.0/account/activity?cursor=&limit=` (RequireAuth) — returns user-facing subset (all events under the user's
  pk; `metadata` filtered through an allowlist).
- UI: `/account/activity` page — event list with icon, description (i18n en/pt-BR), IP, location, device, time. Link
  from dashboard.

---

## C. JWKS Auto-Rotation

### Goal

Keys rotate ~every 90 days with zero deploys and zero downstream breakage (ctech-dfe verifies via JWKS with its own
cache).

### Key storage (SSM)

Replace the single `/ctech-account/{env}/rsa-private-key` with a versioned scheme:

- `/ctech-account/{env}/jwk/active` — JSON `{ "kid": "...", "pem": "...", "created_at": "..." }` (SecureString)
- `/ctech-account/{env}/jwk/previous` — same shape, or absent

Migration: first boot with new code, if `jwk/active` missing, wrap the legacy parameter into `jwk/active` (one-time
`cmd/migratekeys` or lazy in rotation code — decided: **explicit `cmd/rotatekeys --init`** run once; safer than
boot-time writes).

### JWTService changes

- Holds `active` (sign + verify) and `previous` (verify only) key structs. `sign()` always uses active KID.
- `Verify` resolves key by token `kid` header against the loaded set; unknown kid → invalid token.
- `PublicKeyJWKs()` returns both JWKs; `wellknown.go` serves both in JWKS.
- Keys become reloadable: `JWTService` internals guarded by `sync.RWMutex`, plus `Reload(active, previous)` method.

### Rotation loop (in-process, Valkey lock — decided)

Goroutine started in `cmd/api/main.go`:

1. Every **1 hour**: reload keys from SSM (picks up rotations done by other instances — this doubles as the ASG-wide
   propagation mechanism).
2. If active key age > **90 days** (from `created_at`): attempt `SET rotate_jwk_lock NX EX 3600` in Valkey.
    - Lock won → generate new RSA-2048 pair, write `previous ← old active`, `active ← new` to SSM (two `PutParameter`
      calls, previous first), reload own keys, audit-log `jwk.rotated` (internal pk `SYSTEM`), release nothing (lock
      expires).
    - Lock lost → skip; next hourly reload picks up the new keys.
3. If Valkey disabled → rotation skipped entirely (dev mode); manual `cmd/rotatekeys` always available as
   fallback/forced rotation.

### Safety properties

- Old key stays in JWKS as `previous` for a full rotation period (90 d) — far exceeds the 15-min access-token and 1-h
  id-token lifetimes and any downstream JWKS cache TTL.
- Instances lag at most 1 h behind a rotation; during that window laggards still sign with the old key — which remains
  in JWKS — so verification never breaks.
- IAM: instance role needs `ssm:PutParameter` on `/ctech-account/{env}/jwk/*` (CDK iam-stack change).

### Config changes

- `RSA_PRIVATE_KEY` env removed from user-data script; replaced by SSM reads at boot in Go (`config` loads via SSM
  client using existing AWS credentials). Keeps dev override: if `RSA_PRIVATE_KEY` env is set, use it as active (no
  rotation).

---

## Cross-project impact

- **cdk**: new audit table (dynamodb-stack), IAM `ssm:PutParameter`+`GetParameter` on `jwk/*`, user-data no longer
  injects `RSA_PRIVATE_KEY`.
- **ui**: step-up modal, `/account/activity` page, enrollment redirect, i18n strings.
- **ctech-dfe**: no change required (JWKS-driven). Gains optional `amr`/`auth_time`/`last_mfa_at` claims it can use
  later.
- **future wallet**: consumes `last_mfa_at`/`amr` claims for withdrawal gating; audit pattern reusable.

## Testing

- Unit: session AuthTime/AMR/LastMFAAt set+update; audit Service.Record swallow-on-error; JWTService multi-kid
  sign/verify/reload; rotation decision logic (age check, lock behavior with fake clock + in-memory cache).
- Integration (handler): step-up challenge happy/wrong-code/no-MFA-enrolled/rate-limit; RequireRecentMFA 403 paths incl.
  Problem types; activity endpoint pagination; JWKS serves 2 keys after rotation; token signed with old key verifies
  during grace.
- Regression: existing token/refresh flows unaffected when session lacks new fields (zero values).

## Rollout order

1. B (audit) — standalone, no auth-flow risk.
2. A (step-up) — depends on nothing from C; audit events plug in.
3. C (JWKS) — critical area (CLAUDE.md), last, with manual `cmd/rotatekeys` shipped before enabling the auto loop.
