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
- [x] `cdk/lib/frontend-stack.ts` — S3 + CloudFront (accounts.arturocarvalho.com)
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
- [ ] PassKey authentication in `GET /v1.0/authorize` flow (OAuth PKCE — deferred to Sprint 3)

---

## Sprint 3 — Frontend (accounts.arturocarvalho.com)

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
- [ ] Persistent session: client-side silent refresh via `/api/auth/refresh` (not yet wired)
- [ ] `accounts-ui` OAuth client must be registered in DynamoDB before first login

---

## Sprint 4 — py-dfe-api Migration

See `PYDFE_MIGRATION.md` for the full plan.

- [ ] Phase 0: py-dfe-api dual-auth (RS256 ctech + HS256 local)
- [ ] Phase 1: User data migration script
- [ ] Phase 2: py-dfe-client OAuth redirect switch
- [ ] Phase 3: py-dfe-api cutover (RS256 only)
- [ ] Phase 4: Table cleanup

---

## Pending Decisions

| Decision                         | Options                                              | Status                                         |
|----------------------------------|------------------------------------------------------|------------------------------------------------|
| Domain routing for accounts UI   | Single CloudFront (multi-origin) vs separate domains | Decided: single CF (see CDK)                   |
| Refresh token storage on client  | httpOnly cookie vs localStorage                      | httpOnly cookie on accounts.arturocarvalho.com |
| Email verification provider      | AWS SES                                              | Not implemented (Sprint 2+)                    |
| py-dfe OAuth client registration | Manual seed script vs admin UI                       | Manual SSM/direct for now                      |
| Backup codes encryption          | Argon2id hash only                                   | Decided: hash only (unrecoverable)             |

---

## SSM Parameters Required

| Path                                   | Type         | Description                                      |
|----------------------------------------|--------------|--------------------------------------------------|
| `/ctech-account/{env}/rsa-private-key` | SecureString | RSA 2048 PEM private key                         |
| `/ctech/{env}/valkey/url`              | String       | Valkey connection URL (existing, from ctech-cdk) |

---

## Architecture Notes

- Token flow: access_token (RS256 JWT, 15min) + refresh_token (opaque, 90d, in httpOnly cookie)
- Refresh token rotation: single-use; reuse = theft → revoke full session
- PKCE mandatory for all public OAuth clients
- KID rotation: generate new key pair → deploy with both KIDs in JWKS → after 24h, remove old KID from JWKS → after
  another 24h, stop issuing with old KID
- CORS: `accounts.arturocarvalho.com` whitelisted + any registered OAuth client origin
- Rate limiting: 5 failed logins / 15min per IP (Valkey counter), 100 req/min per authenticated user
