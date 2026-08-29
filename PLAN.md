# ctech-account — Implementation Plan

> Persist-tolerant checklist. Mark items with `[x]` as you complete them.
> Last updated: 2026-06-08

---

## Sprint 1 — Core Auth + OIDC (current)

### Go Backend

- [x] Project structure created (`cmd/`, `internal/`, `cdk/`, `.github/`)
- [x] `go.mod` initialized with all dependencies
- [x] `internal/config/config.go` — env vars + RSA key loading
- [x] `internal/crypto/password.go` — Argon2id hash/verify
- [x] `internal/crypto/jwt.go` — RS256 sign/verify, JWK export
- [x] `internal/crypto/token.go` — opaque token generation + hash
- [x] `internal/database/dynamo.go` — DynamoDB SDK v2 wrapper
- [x] `internal/cache/valkey.go` — Valkey client wrapper (no-op if URL empty)
- [x] `internal/domain/user/` — model, repository, service
- [x] `internal/domain/session/` — model, repository, service (rotation + theft detection)
- [x] `internal/domain/oauth/client/` — model, repository
- [x] `internal/domain/oauth/code/` — model, repository (Valkey, 60s TTL)
- [x] `internal/domain/apikey/` — model, repository, service
- [x] `internal/domain/mfa/totp/` — model, service (pquerna/otp)
- [x] `internal/handler/wellknown.go` — OIDC discovery + JWKS
- [x] `internal/handler/authorize.go` — OAuth 2.0 Authorization Code + PKCE
- [x] `internal/handler/token.go` — token exchange + refresh rotation + revoke
- [x] `internal/handler/userinfo.go` — OIDC userinfo endpoint
- [x] `internal/handler/auth.go` — register, login (with MFA gate), logout
- [x] `internal/handler/sessions.go` — list, revoke, revoke-all
- [x] `internal/handler/profile.go` — get, update profile, change password
- [x] `internal/handler/apikeys.go` — CRUD API keys
- [x] `internal/middleware/auth.go` — RequireAuth + OptionalAuth fiber middleware
- [x] `cmd/api/main.go` — wire all dependencies + Fiber router

### Infrastructure

- [x] `cdk/lib/types.ts`
- [x] `cdk/lib/dynamodb-stack.ts` — 5 ctech_* tables + GSIs
- [x] `cdk/lib/compute-stack.ts` — ASG + EC2 t4g.micro (clone of ApiStackV2, Go binary)
- [x] `cdk/lib/frontend-stack.ts` — S3 + CloudFront (accounts.aoctech.app)
- [x] `cdk/lib/iam-stack.ts` — instance profile + DynamoDB/SSM/S3 permissions
- [x] `cdk/lib/oidc-stack.ts` — GitHub Actions OIDC role
- [x] `cdk/bin/ctech-account.ts` — CDK app entry point
- [x] `cdk/package.json`, `cdk/tsconfig.json`, `cdk/cdk.json`

### CI/CD

- [x] `.github/workflows/ci.yml` — go test, go vet, go build (on PR)
- [x] `.github/workflows/deploy-backend.yml` — build arm64 → S3 → SSM rolling deploy
- [x] `.github/workflows/deploy-frontend.yml` — next build → S3 sync → CF invalidate

### First Deploy Steps

- [ ] Generate RSA key pair: `openssl genrsa -out private.pem 2048`
- [ ] Store in SSM:
  `aws ssm put-parameter --name "/ctech-account/prod/rsa-private-key" --type SecureString --value "$(cat private.pem)"`
- [ ] `cd cdk && npm install && cdk deploy --all`
- [ ] Register py-dfe as OAuth client via POST /v1.0/account/oauth-clients (after first admin login)
- [ ] Run `go mod tidy` to generate go.sum

---

## Sprint 2 — MFA Completo + PassKeys ✓

- [x] `internal/domain/mfa/passkey/` — WebAuthn credential model + repository + service
- [x] `internal/handler/mfa.go` — TOTP setup/confirm/remove endpoints
- [x] `GET /v1.0/account/mfa/totp/setup` — generate QR code URI
- [x] `POST /v1.0/account/mfa/totp/confirm` — verify + activate + generate backup codes
- [x] `DELETE /v1.0/account/mfa/totp`
- [x] `POST /v1.0/account/mfa/totp/backup-codes` — regenerate (invalidates old)
- [x] `POST /v1.0/account/mfa/passkeys/register/begin` — WebAuthn challenge (go-webauthn v0.17.4)
- [x] `POST /v1.0/account/mfa/passkeys/register/complete`
- [x] `GET /v1.0/account/mfa/passkeys`
- [x] `DELETE /v1.0/account/mfa/passkeys/:id`
- [x] MFA gate in `POST /v1.0/auth/login` — issues `mfa_token` (Valkey, 5 min TTL) if TOTP enabled
- [x] `POST /v1.0/auth/mfa/challenge` — validates mfa_token + TOTP code → creates session + sets cookie
- [x] `POST /v1.0/auth/passkeys/authenticate/begin` — discoverable login challenge
- [x] `POST /v1.0/auth/passkeys/authenticate/complete` — validates assertion → creates session
- [x] Passkey-first login in `GET /v1.0/authorize` flow — explicit button plus privacy-preserving Conditional UI/autofill; no email-enrollment probe; after successful auth `ctech_session` is set and `/authorize` redirects normally
- [x] Passkey + TOTP preserves AMR as `["webauthn","otp"]` (password + TOTP remains `["pwd","otp"]`)

---

## Sprint 3 — Frontend (accounts.aoctech.app)

- [x] Init Next.js app: `npx create-next-app@latest ui --typescript --tailwind --app`
- [x] Install ShadCN: `npx shadcn@latest init` (v4 with @base-ui/react, Tailwind v4)
- [x] `/login` — email/password form + continue param support
- [x] `/login/mfa` — TOTP code input
- [x] `/register` — create account form
- [x] `/register/verify` — email verification confirmation page
- [x] `/account` — dashboard (session count, current session, account age)
- [x] `/account/profile` — edit name + change password
- [x] `/account/security` — MFA methods list + TOTP remove
- [x] `/account/security/totp` — QR code setup + backup codes
- [x] `/account/security/passkeys` — list + register (WebAuthn) + remove
- [x] `/account/sessions` — device/IP/last-active + revoke buttons
- [x] `/account/api-keys` — list, create (with scopes + expiry), revoke
- [x] `/account/oauth-clients` — placeholder (admin-only provisioning)
- [x] OAuth redirect flow: `/login` reads `?continue=` param, redirects back after auth
- [x] `src/proxy.ts` — protects `/account/*`, redirects to `/login?continue=` if no token
- [x] BFF auth Route Handlers: login (server-side PKCE OAuth dance), register, logout, MFA, refresh
- [x] Server Actions for all account mutations (profile, sessions, API keys, TOTP, passkeys)
- [x] Persistent session: client-side silent refresh via `/v1.0/token` (AuthInitializer in query-provider.tsx)
- [x] `/forgot-password` — email form, calls POST /v1.0/auth/forgot-password, always 200 (no enumeration)
- [x] `/reset-password` — reads `?token=`, validates password match, calls POST /v1.0/auth/reset-password
- [x] `/verify-email` — reads `?token=`, calls POST /v1.0/auth/verify-email on mount, shows success/error
- [x] `/login` — added "Forgot password?" link + Google sign-in button (redirects to /v1.0/auth/google)
- [x] i18n strings (en + pt-BR): forgotPassword, resetPassword, verifyEmail namespaces
- [x] `accounts` OAuth client is created/reconciled automatically at API startup

### Resource Server scope ownership

- [x] Downstream Resource Servers publish versioned manifests through bound confidential clients
- [x] Account is registered as `RESOURCE_SERVER/account` from its embedded system-owned manifest
- [x] Account routes enforce exact `account:*` scopes in addition to the trusted SPA `azp`
- [x] Account exposes RFC 9728 metadata at `/.well-known/oauth-protected-resource`

---

## Sprint 4 — py-dfe-api Migration

See `PYDFE_MIGRATION.md` for the full plan.

- [x] Phase 0: py-dfe-api dual-auth — verify_rs256 fallback in security.py + get_current_token; HS256 path unchanged
- [x] Phase 1: ctech-account POST /internal/v1.0/users/migrate (X-Internal-Token auth, idempotent); py-dfe-api get_user_by_ctech_id + ctech-user-id-index GSI in CDK; migration script at py-dfe-api/scripts/migrate_users_to_ctech.py
- [x] Phase 2: py-dfe-client OAuth redirect switch
- [x] Phase 3: py-dfe-api cutover (RS256 only) — HS256 path removed, RS256-only verify
- [x] Phase 4: Lazy user creation via ctech /v1.0/userinfo (PK = USER_{ctech_user_id}); no migration needed

---

## Sprint 5 — Support Tickets

See `docs/specs/2026-08-22-support-tickets-design.md` and `docs/plans/2026-08-22-support-tickets.md`
for the full spec and implementation plan.

### Go Backend

- [x] `cdk/lib/dynamodb-stack.ts` — `account_support_tickets` table + `status-index`/`user-index`/`anon-token-index`/`ticket-number-index` GSIs
- [x] `cdk/lib/api-stack.ts` + `internal/config/config.go` — `TURNSTILE_SECRET_KEY` SSM wiring
- [x] `internal/domain/support/model.go` — `Ticket`/`Message`, category/priority/status/author catalogs
- [x] `internal/validate/freetext.go` — reusable trim/length/junk-pattern validator (`subject_other`, `body`, `nps_message`)
- [x] `internal/domain/user` — `SupportRole` field + `SetSupportRole`
- [x] `internal/domain/support/repository.go` — DynamoDB repository (atomic ticket-number counter, cursor-paginated GSI queries)
- [x] `internal/domain/support/service.go` — create/reply/status/NPS business logic, admin-unscoped fetch, SES Message-ID bookkeeping
- [x] `internal/email/ses.go` — threaded confirmation/reply/NPS emails via SES raw MIME (`In-Reply-To`/`References`)
- [x] `internal/turnstile/` — Cloudflare Turnstile Siteverify client
- [x] `internal/middleware/support.go` — `RequireSupportRole` (DB lookup, not a JWT claim)
- [x] `cmd/supportrole/` — operator CLI to grant/revoke `support_role`
- [x] `internal/handler/support.go`, `support_admin.go` — public/account/admin routes
- [x] `internal/handler/profile.go` — expose `support_role` on `GET /account/profile`
- [x] `cmd/api/main.go` — wire repository/service/handlers, mount `/v1.0/support`, `/v1.0/account/support`, `/v1.0/admin`
- [x] Production error-observability audit — structured backend logging with request correlation,
      explicit logs for support-email/background failures, and sanitized frontend diagnostics
- [x] Binary protobuf WebSocket at `/v1.0/support/tickets/:id/ws` — first-frame JWT/anonymous-token auth,
      owner/agent authorization and Valkey fan-out across API instances
- [x] Terminal closure semantics — closed threads reject replies, status changes, escalation and internal notes
- [x] Agent collaboration — private notes and explicit specialist/engineering escalation
- [x] `account_support_metrics` — transactional daily/monthly/yearly/all-time created/product and resolution aggregates

### Frontend (accounts.aoctech.app)

- [x] `/support` — public ticket form (category + subcategory selects, Turnstile, conditional email field for anonymous submitters)
- [x] `/support/ticket` — thread view + reply + NPS prompt on closure
- [x] `/account/support` — "meus tickets" list
- [x] `/admin/support`, `/admin/support/ticket` — agent queue + thread, gated by `support_role` via `admin/layout.tsx`
- [x] `lib/support-catalog.ts` — category/subcategory catalog merged into one `subject_other` string
- [x] `lib/mock.ts` — seeded support-ticket scenarios (open/answered/closed/closed-with-NPS/anonymous) for `NEXT_PUBLIC_MOCK_API` dev mode
- [x] `locales/en.json`, `locales/pt-BR.json` — `support` i18n namespace
- [x] Agent support workspace — live connection state, irreversible-close confirmation, internal notes,
      escalation controls and compact operational metrics

---

## Pending Decisions

| Decision                         | Options                                              | Status                                         |
|----------------------------------|------------------------------------------------------|------------------------------------------------|
| Domain routing for accounts UI   | Single CloudFront (multi-origin) vs separate domains | Decided: single CF (see CDK)                   |
| Refresh token storage on client  | httpOnly cookie vs localStorage                      | httpOnly cookie on accounts.aoctech.app |
| Email verification provider      | AWS SES                                              | Implemented: SESv2, verify + password reset    |
| Google OAuth                     | google-oauth2 via /v1.0/auth/google                  | Implemented: state in Valkey (gs:, 10min TTL)  |
| py-dfe OAuth client registration | Manual seed script vs admin UI                       | Manual SSM/direct for now                      |
| Backup codes encryption          | Argon2id hash only                                   | Decided: hash only (unrecoverable)             |

---

## SSM Parameters Required

| Path                                        | Type         | Description                                               |
|---------------------------------------------|--------------|-----------------------------------------------------------|
| `/ctech-account/{env}/rsa-private-key`      | SecureString | RSA 2048 PEM private key                                  |
| `/ctech/{env}/valkey/url`                   | String       | Valkey connection URL (existing, from ctech-cdk)          |
| `/ctech-account/{env}/from-email`           | String       | SES verified sender address (FROM_EMAIL)                  |
| `/ctech-account/{env}/turnstile-secret-key` | SecureString | Cloudflare Turnstile Siteverify secret (TURNSTILE_SECRET_KEY); empty disables verification (dev only) |
| `/ctech-account/{env}/app-url`              | String       | Public base URL, e.g. https://accounts.aoctech.app |
| `/ctech-account/{env}/webauthn-rpid`        | String       | Optional shared WebAuthn RP ID; defaults to the app-url hostname |
| `/ctech-account/{env}/google-client-id`     | String       | Google OAuth 2.0 client ID                                |
| `/ctech-account/{env}/google-client-secret` | SecureString | Google OAuth 2.0 client secret                            |
| `/ctech-account/{env}/maxmind-account-id`   | SecureString | MaxMind account ID for GeoLite2 City auto-updates          |
| `/ctech-account/{env}/maxmind-license-key`  | SecureString | MaxMind license key for GeoLite2 City auto-updates          |

---

## Architecture Notes

- Token flow: access_token (RS256 JWT, 15min) + refresh_token (opaque, 90d, in httpOnly cookie)
- Refresh token rotation: single-use; reuse = theft → revoke full session
- PKCE mandatory for all public OAuth clients
- KID rotation: automated — versioned keys in SSM (`jwk/active` + `jwk/previous`), hourly reload, 90-day rotation
  under Valkey lock, both KIDs served in JWKS (see README §Signing key rotation; manual: `cmd/rotatekeys`)
- CORS: `accounts.aoctech.app` whitelisted + any registered OAuth client origin
- Rate limiting: 5 failed logins / 15min per IP (Valkey counter), 100 req/min per authenticated user.
  Now also covers `/authorize`, `/authorize/consent`, `/auth/register`, `/revoke`, `/auth/google*`,
  `/auth/passkeys/authenticate/complete`. Every `429` carries `Retry-After` + `retry_after_seconds`
  from the counter's TTL (see README §Rate limiting)
- GeoIP: local MaxMind GeoLite2 City DB (`internal/geo`), per-instance auto-update every 24h once
  7+ days stale (`internal/geoupdater`, no distributed lock — no shared store for this file across
  the ASG). New-device login (device name + country not seen before) gets `new_device: "true"` audit
  metadata + email notification (see README §GeoIP + new-device login notification).
