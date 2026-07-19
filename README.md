# ctech-account

Centralized OAuth 2.0 + OpenID Connect Identity Provider for the aoctech.app platform.

Built with **Go 1.26** and **Fiber v3**. Runs on AWS Lambda via API Gateway or on EC2/ECS.

---

## Features

- **OAuth 2.0** — Authorization Code flow with PKCE
- **OpenID Connect** — Discovery document, JWKS, UserInfo endpoint
- **Persistent sessions** — Cookie-based refresh tokens with automatic rotation and token-reuse detection
- **API keys** — Long-lived scoped tokens for programmatic access
- **TOTP / MFA** — Time-based one-time passwords (Sprint 2)
- **WebAuthn / Passkeys** — Passwordless authentication (Sprint 2)
- **RFC 7807 Problem Details** — All error responses use `application/problem+json`
- **RFC Health Check** — `/healthz` responds with `application/health+json`
- **DynamoDB** — Single-table design; sessions, users, API keys, OAuth clients and codes
- **Valkey** — Optional cache for MFA tokens and session data (disabled when `VALKEY_URL` is unset)

---

## Project Layout

```
ctech-account/
├── api/              # Go module — all commands below assume `cd api` first
│   ├── cmd/api/      # Entry point — Fiber app wiring
│   └── internal/
│       ├── apierror/     # RFC 7807 Problem Details types + constructors
│       ├── cache/        # Valkey client wrapper
│       ├── config/       # Environment-driven configuration
│       ├── crypto/       # JWT signing (RS256), bcrypt, PKCE helpers
│       ├── database/     # DynamoDB client wrapper
│       ├── domain/       # Core business logic
│       │   ├── apikey/   # API key entity, repository interface, service
│       │   ├── mfa/
│       │   │   ├── passkey/ # WebAuthn credential model, repository, service
│       │   │   └── totp/    # TOTP secret management
│       │   ├── oauth/    # OAuth client entity + repository interface
│       │   │   ├── client/
│       │   │   └── code/
│       │   ├── session/  # Session entity, repository interface, service
│       │   └── user/     # User entity, repository interface, service
│       ├── handler/      # HTTP handlers (one file per route group)
│       ├── middleware/   # RequireAuth JWT middleware
│       └── validate/     # go-playground/validator singleton
├── ui/               # Next.js 16 frontend
└── cdk/              # AWS CDK infrastructure
```

## Central Jurídica

O frontend publica a documentação jurídica institucional e de produtos em
`https://accounts.aoctech.app/legal`. Termos gerais e privacidade preservam as rotas
canônicas `/terms` e `/privacy` e seus históricos; os demais documentos ficam
centralizados no Accounts:

- gerais: cookies, segurança, uso aceitável, KYC e contrato para desenvolvedores;
- empresarial: Data Processing Addendum (DPA);
- produtos: DF-e, Wallet, Wallet para Jogos, Poker, regras do Poker e Billing;
- confiança: divulgação responsável e relatório de transparência.

Os frontends dos produtos devem apontar para as rotas correspondentes no Accounts,
mantendo neste repositório a fonte pública de verdade dos textos.

---

## API

| Method   | Path                                                | Auth     | Description                                                                                              |
|----------|-----------------------------------------------------|----------|----------------------------------------------------------------------------------------------------------|
| `POST`   | `/v1.0/auth/register`                               | —        | Create a new account (`accept_terms: true` required). Always `202` — the response never reveals whether the email was already registered |
| `POST`   | `/v1.0/auth/login`                                  | —        | Password login; sets session cookie. `403 email-not-verified` until the address is confirmed             |
| `POST`   | `/v1.0/auth/logout`                                 | Optional | Revoke current session cookie                                                                            |
| `POST`   | `/v1.0/auth/verify-email`                           | —        | Confirm an email address from the emailed token                                                          |
| `POST`   | `/v1.0/auth/resend-verification`                    | —        | Resend the verification link (always `200`)                                                              |
| `POST`   | `/v1.0/auth/forgot-password`                        | —        | Request a reset link (always `200`)                                                                      |
| `POST`   | `/v1.0/auth/reset-password`                         | —        | Set a new password from the emailed token (revokes all sessions)                                         |
| `GET`    | `/v1.0/auth/google`                                 | —        | Start Google sign-in / sign-up (OpenID)                                                                  |
| `GET`    | `/v1.0/auth/google/callback`                        | —        | Google OAuth callback. Existing accounts → session cookie. Brand-new accounts → redirected to the ui's `/accept-terms` interstitial (never saw the register form's checkbox) |
| `POST`   | `/v1.0/auth/accept-terms`                           | —        | One-time token (from the Google callback or the `/authorize` terms gate) + `accept_tos` / `accept_privacy` → stamps the pending documents. Issues the withheld session only for a suspended sign-up |
| `GET`    | `/v1.0/authorize`                                   | Session  | OAuth authorization endpoint (redirects to the consent screen, or to `/accept-terms` when a ToS/Privacy version bump is pending) |
| `POST`   | `/v1.0/authorize/consent`                           | Session  | Record the consent decision; returns `redirect_to` (client `error=access_denied` on deny)                |
| `POST`   | `/v1.0/token`                                       | —        | OAuth token endpoint (`authorization_code`, `refresh_token`, `api_key`, `client_credentials` grants)     |
| `GET`    | `/v1.0/userinfo`                                    | Bearer   | OIDC UserInfo                                                                                            |
| `GET`    | `/v1.0/scopes`                                      | —        | Grantable-scope catalog (code + descriptions, grouped by service) for UI pickers                         |
| `GET`    | `/v1.0/account/profile`                             | Bearer   | Get profile (includes `terms_pending: {tos, privacy}`, `has_password`, and `google_linked` — drive the in-app terms gate and the Link/Unlink Google UI) |
| `PATCH`  | `/v1.0/account/profile`                             | Bearer   | Update profile                                                                                           |
| `POST`   | `/v1.0/account/terms/accept`                        | Bearer   | Re-accept the documents whose version moved (`accept_tos` / `accept_privacy`); returns the cleared `terms_pending` |
| `PUT`    | `/v1.0/account/password`                            | Bearer   | Change password (revokes all other sessions)                                                             |
| `POST`   | `/v1.0/account/password`                            | Bearer   | Set the first password on a Google-created account                                                       |
| `DELETE` | `/v1.0/account/link/google`                         | Bearer + step-up | Unlink the bound Google identity (refused for passwordless accounts, which would lose their only login method) |
| `GET`    | `/v1.0/account/sessions`                            | Bearer   | List active sessions                                                                                     |
| `DELETE` | `/v1.0/account/sessions`                            | Bearer   | Revoke all other sessions                                                                                |
| `DELETE` | `/v1.0/account/sessions/:id`                        | Bearer   | Revoke a specific session                                                                                |
| `GET`    | `/v1.0/account/activity`                            | Bearer   | Security activity log (cursor pagination: `?cursor=&limit=`, newest first, 400-day retention)            |
| `GET`    | `/v1.0/account/api-keys`                            | Bearer   | List API keys                                                                                            |
| `POST`   | `/v1.0/account/api-keys`                            | Bearer   | Create API key                                                                                           |
| `DELETE` | `/v1.0/account/api-keys/:id`                        | Bearer   | Revoke API key                                                                                           |
| `GET`    | `/v1.0/account/oauth-clients`                       | Bearer   | List OAuth applications owned by the user                                                                |
| `POST`   | `/v1.0/account/oauth-clients`                       | Bearer   | Register an OAuth application (`client_secret` returned once for confidential clients)                   |
| `PUT`    | `/v1.0/account/oauth-clients/:id`                   | Bearer   | Update name / redirect URIs / scopes / audience                                                          |
| `DELETE` | `/v1.0/account/oauth-clients/:id`                   | Bearer   | Delete an OAuth application                                                                              |
| `POST`   | `/v1.0/account/oauth-clients/:id/regenerate-secret` | Bearer   | Rotate the client secret (returned once)                                                                 |
| `GET`    | `/v1.0/account/kyc`                                 | Bearer   | KYC status: `{state, level, method, cpf_masked, legal_name, birth_date, address, documents, rejection_reason, submitted_at, expires_at, verified_at}` (CPF masked `***.***.***-XX`) |
| `POST`   | `/v1.0/account/kyc`                                 | Bearer + step-up | Submit identity data `{cpf, legal_name, birth_date, address}` → `pending_review` (requires `id_front`, `id_back` and all four `selfie_{up,down,left,right}` already uploaded) |
| `POST`   | `/v1.0/account/kyc/documents`                       | Bearer + step-up | `{type, content_type}` → `{document_id, upload_url}` — presigned S3 PUT; `type` one of `id_front`, `id_back`, `selfie_up`, `selfie_down`, `selfie_left`, `selfie_right` |
| `POST`   | `/v1.0/account/kyc/documents/confirm`               | Bearer + step-up | `{document_id, type}` → records the upload (verified via HeadObject); documents may be uploaded before any identity data is submitted |
| `GET`    | `/v1.0/internal/kyc/:user_id`                       | Service token (`internal:wallet:confirm-deposit`) | Full unmasked identity record (ctech-wallet withdrawal-key validation) — the only internal KYC route; approve/reject is a CLI-only action, see `cmd/kyc` |
| `GET`    | `/v1.0/account/consents`                            | Bearer   | List connected apps (consent grants)                                                                     |
| `DELETE` | `/v1.0/account/consents/:clientID`                  | Bearer   | Revoke a consent grant                                                                                   |
| `POST`   | `/v1.0/auth/mfa/challenge`                          | —        | Exchange MFA token + TOTP code for session                                                               |
| `POST`   | `/v1.0/auth/mfa/passkey/begin`                      | —        | Begin passkey assertion as 2nd factor                                                                    |
| `POST`   | `/v1.0/auth/mfa/passkey/complete`                   | —        | Complete passkey assertion → session cookie                                                              |
| `POST`   | `/v1.0/auth/step-up`                                | Bearer   | Step-up challenge: `{method:"totp",code}` → stamps fresh MFA proof on the session (rate-limited)         |
| `POST`   | `/v1.0/auth/step-up/passkeys/begin`                 | Bearer   | Step-up WebAuthn assertion challenge for the current user                                                |
| `POST`   | `/v1.0/auth/step-up/passkeys/complete`              | Bearer   | Validate step-up assertion → stamps fresh MFA proof                                                      |
| `POST`   | `/v1.0/auth/passkeys/authenticate/begin`            | —        | WebAuthn discoverable login challenge                                                                    |
| `POST`   | `/v1.0/auth/passkeys/authenticate/complete`         | —        | Validate assertion → session cookie                                                                      |
| `GET`    | `/v1.0/account/mfa/totp/setup`                      | Bearer   | Generate TOTP provisioning URI                                                                           |
| `POST`   | `/v1.0/account/mfa/totp/confirm`                    | Bearer   | Activate TOTP + get backup codes                                                                         |
| `DELETE` | `/v1.0/account/mfa/totp`                            | Bearer   | Remove TOTP from account                                                                                 |
| `POST`   | `/v1.0/account/mfa/totp/backup-codes`               | Bearer   | Regenerate backup codes                                                                                  |
| `GET`    | `/v1.0/account/mfa/passkeys`                        | Bearer   | List registered passkeys                                                                                 |
| `POST`   | `/v1.0/account/mfa/passkeys/register/begin`         | Bearer   | WebAuthn registration challenge                                                                          |
| `POST`   | `/v1.0/account/mfa/passkeys/register/complete`      | Bearer   | Validate attestation → persist credential                                                                |
| `DELETE` | `/v1.0/account/mfa/passkeys/:id`                    | Bearer   | Remove a passkey                                                                                         |
| `GET`    | `/.well-known/openid-configuration`                 | —        | OIDC Discovery document                                                                                  |
| `GET`    | `/.well-known/jwks.json`                            | —        | JSON Web Key Set                                                                                         |
| `GET`    | `/healthz`                                          | —        | Health check (`application/health+json`)                                                                 |

---

## Error Format

All errors follow [RFC 7807](https://www.rfc-editor.org/rfc/rfc7807):

```json
{
  "type": "https://accounts.aoctech.app/problems/invalid-credentials",
  "title": "Invalid Credentials",
  "status": 401,
  "detail": "The email or password is incorrect.",
  "instance": "/v1.0/auth/login"
}
```

Token endpoint errors additionally include `error` and `error_description` (RFC 6749).

---

## Sessions, Cookies & Refresh Tokens

Three cookies exist, all scoped to the parent domain:

| Cookie          | HttpOnly | Purpose                                                                                                        |
|-----------------|----------|----------------------------------------------------------------------------------------------------------------|
| `ctech_session` | yes      | SSO session token — authenticates `GET /v1.0/authorize`. Set at login, **never rotated by token grants**       |
| `ctech_rt`      | yes      | Per-client refresh token for the accounts SPA — rotated on every `refresh_token` grant                         |
| `ctech_auth`    | no       | Marker (`1`) telling frontend JS a session may exist, so it knows whether a silent refresh is worth attempting |

Refresh tokens are stored per `(session, client)` — item `REFRESH_{session_id}#{client_id}`
in the sessions table. One client's code exchange or rotation never invalidates the SSO
session or another client's refresh chain. Revoking a session cascades to all its
refresh tokens. Reuse of a stale refresh token returns `401 token-reuse`.

## Scopes

Two scope families:

- **OIDC identity scopes**: `openid`, `profile`, `email`, `kyc` — for humans, via the OAuth
  flow. `kyc` adds the `kyc_level` claim (`""` | `verified`) to access tokens, id_tokens
  and userinfo — CPF, birth date and legal name never enter tokens.
- **Service scopes**: `service:resource:action` (e.g. `dfe:nfe:issue`, `account:*:read`) —
  permissions on a downstream resource server. `*` is allowed as a full resource or action
  segment; the action segment may be omitted to grant all actions on a resource.

The `internal` service (e.g. `internal:wallet:confirm-deposit`) is machine-to-machine
only: hidden from `GET /v1.0/scopes` and the consent UI, rejected by self-service
client/API-key creation, and assigned to first-party confidential clients exclusively
via operator seed.

The grantable set is a fixed catalog served by `GET /v1.0/scopes` (code + pt/en
descriptions, grouped by service) — UIs render pickers from it and creation endpoints
reject anything outside it, so scopes are chosen, never free-typed. dfe scopes mirror
ctech-dfe's RBAC resources (`dfe:nfes:read` → `get.nfes` + `list.nfes`;
`dfe:nfes:write` → `create/delete.nfes` + events). The IdP records granted scopes in
the token / API key; the named service enforces the semantics. API keys accept only
service scopes (default `account:profile:read`); identity scopes are rejected on keys.

### Adding a new scope

The runtime source of truth is the platform-wide **`{env}_ctech_scopes`** DynamoDB
table (single partition `pk=SERVICE`, one item per service with its scope list),
read through a 5-minute Valkey cache (`scope_catalog` key). No accounts deploy is
needed to add a scope:

1. Add the `ScopeEntry` (code + English + pt-BR descriptions) to the seed in
   `api/internal/scopes/catalog.go` — the seed stays in the repo so scope codes and
   descriptions remain code-reviewed — or a new `ServiceScopes` block with the
   service's `Audience` (its `SERVICE_AUDIENCE`).
2. Run the seeder against the environment (from `api/`):
   `AWS_REGION=... TABLE_PREFIX=production_ VALKEY_URL=... go run ./cmd/seedscopes`
   (writes all services and invalidates the cache). For a one-off addition without
   a checkout, `aws dynamodb put-item` on the service item works too — then
   invalidate manually: `DEL scope_catalog` in Valkey, or wait up to 5 minutes.
3. Keep the scope ↔ RBAC mapping documented in the consuming service
   (`ctech-dfe/INTEGRATION.md` for dfe) in sync.
4. `TestCatalog` enforces grammar + both descriptions on the seed;
   run `go test ./internal/scopes/` from `api/`.

Services marked `Internal: true` in the seed are hidden from `GET /v1.0/scopes` and
rejected by self-service client/API-key creation — their scopes are assigned only via
operator seed. `internal` is a shared machine-to-machine namespace, not a single
service: each downstream consumer gets its own catalog entry keyed
`internal:<service>` (e.g. `internal:wallet`) with its own `Audience`, so
`AudiencesFor` resolves the right `aud` claim per target instead of lumping every
internal scope under one bucket. After deploying a release that adds catalog entries
(e.g. `kyc`, `internal:wallet:confirm-deposit`), re-run `go run ./cmd/seedscopes` per
environment. ctech-wallet's M2M client is seeded confidential + `first_party: true` +
`allowed_scopes: ["internal:wallet:confirm-deposit"]` (direct DynamoDB put, same as
`accounts-ui`).

Pickers, consent screens, and creation validation pick the change up automatically —
validation fails closed if the catalog cannot be read.

### API keys (machine-to-machine)

Raw API keys are never sent to resource servers. Clients exchange them here:

```
POST /v1.0/token
grant_type=api_key&api_key=<raw key>
→ { access_token (RS256, 15 min), scope, expires_in }
```

The JWT carries `sub` = key owner, `scope` = the key's scopes, and `aud` = this IdP
plus the audiences of every service named by those scopes (a `dfe:*` scope adds dfe's
`SERVICE_AUDIENCE`). Resource servers validate it via JWKS like any other token —
no key lookup, no shared storage. Revoking the key blocks new exchanges immediately;
outstanding tokens expire within 15 minutes.

### Service-to-service tokens (`client_credentials`)

First-party **confidential** clients (e.g. ctech-wallet) obtain machine tokens directly:

```
POST /v1.0/token
grant_type=client_credentials&client_id=...&client_secret=...&scope=internal:kyc
→ { access_token (RS256, 15 min), scope, expires_in }   # no refresh token
```

Public or third-party clients get `403 unauthorized_client`. The requested scope is
clamped to the client's `allowed_scopes`. The token carries `sub = client_id` and an
**empty `sid`** — internal routes (`/v1.0/internal/*`) accept only tokens with an empty
`sid` plus the required `internal:*` scope, so user tokens and API-key tokens (which
always carry a `sid`) can never reach them.

### Terms of Service / Privacy Policy acceptance

`POST /v1.0/auth/register` requires `accept_terms: true` (validation `required` on
a bool — `false`/missing → `422`). On success the account is stamped with
`tos_version`/`tos_accepted_at`/`privacy_version`/`privacy_accepted_at`
(`api/internal/legal` holds the current version constants) and an `auth.terms_accepted`
audit event is recorded.

Google sign-up never shows the register form, so a brand-new account
(`FindOrCreateByGoogle` returning `created=true`) is redirected to the ui's
`/accept-terms?token=...` interstitial instead of getting a session immediately.
The token (Valkey, 10 min TTL, single-use) carries the suspended
device/IP/user-agent/continue-URL; `POST /v1.0/auth/accept-terms` consumes it,
stamps acceptance, and only then issues the session.

**Version bumps re-gate every account.** Acceptance is an exact version match
(`legal.PendingFor`), so bumping `CurrentToSVersion` or `CurrentPrivacyVersion`
makes every stored acceptance stale — including accounts predating versioning,
which carry no version at all. The two documents version independently: a user
who owes only the Privacy Policy is asked for that one alone.

Two gates enforce it, one per credential:

- **`GET /v1.0/authorize`** — checked right after the SSO session is validated and
  before any authorization code is minted, so every product behind this IdP
  (ctech-wallet, ctech-dfe) inherits the block. The user holds a session cookie
  but not necessarily an access token (on a fresh login the code exchange hasn't
  happened yet), so the interstitial is authenticated by the same single-use
  terms token described above — `Reaccept: true`, which stamps acceptance
  **without** issuing a second session — and then bounces back to the original
  `/authorize` URL.
- **`POST /v1.0/account/terms/accept`** — bearer-authenticated. A session that
  keeps refreshing its access token never passes through `/authorize` again, so
  the SPA blocks `/account/*` on `terms_pending` from `GET /account/profile` and
  clears it here.

Both paths recompute the pending set server-side before writing: the client's
`accept_tos` / `accept_privacy` flags are a confirmation, and a pending document
left unconfirmed is a `422`. Only the documents actually owed are stamped, so
re-accepting one never forges an acceptance of the other.

Published at `accounts.aoctech.app/terms` and `/privacy` (master ToS/Privacy for
the whole CTech platform — legal entity A O CARVALHO TECH, CNPJ
62.787.449/0001-07). Product-specific addenda (financial terms for ctech-wallet,
data-processing terms for ctech-dfe) live in each product's own repo/frontend.

### KYC (identity verification)

**Manual-only, document-based.** KYC via Pix deposit was removed: the Pix webhook
payload never carries the payer's CPF (only free-text `infoPagador`), so an automated
CPF match is unsupportable. Every submission is reviewed by a human via `cmd/kyc` —
there is no admin UI and no `internal:kyc` service scope anymore. Levels collapse to
`none` (`""`) → `verified`, stored on the user — there is no intermediate `basic`.

**Upload-then-submit.** Unlike a typical form, documents are uploaded *before* the
identity data:

1. `POST /v1.0/account/kyc/documents` (presign) → browser PUTs straight to S3 →
   `POST /v1.0/account/kyc/documents/confirm` (per document). Repeat for all six
   required types: `id_front`, `id_back`, `selfie_up`, `selfie_down`, `selfie_left`,
   `selfie_right`. Accepted content types: JPEG/PNG/HEIC/PDF and `video/webm`/`video/mp4`
   (≤ 5 MiB each). The four selfie poses are short head-turn clips, not one static
   photo — a printed photo or looped video can't turn on command, so this is the
   liveness signal; the *reviewer* still judges real-vs-photo, no server-side ML.
2. `POST /v1.0/account/kyc` (step-up required) — validates CPF check digits, rejects
   repeated-digit sequences, requires age ≥ 18 and a full address (CEP + UF against
   the 27 states), claims the CPF via a `CPF_{cpf}` uniqueness item in the same
   DynamoDB transaction (1 CPF = 1 account; conflict → `409 cpf-already-registered`),
   and is **rejected** (`409 kyc-not-submitted`) unless every required document is
   already uploaded. On success: `kyc_doc_status = pending_review`, `kyc_level` stays
   `""`.

`GET /v1.0/account/kyc` returns a derived `state` the UI branches on: `not_started` |
`awaiting_files` (some/all docs uploaded, not yet submitted) | `under_review` |
`rejected` | `verified`. While a submission is under review, both re-uploading
documents and resubmitting identity data are refused with `409 kyc-submission-locked`;
a rejection or the 30-day expiry unlocks it (an expired pending submission reads back
as `not_started` — its documents are still on file, so resubmitting just re-queues
them). A **rejection clears the uploaded documents**, so resubmission requires a fresh
upload cycle.

**Manual review — `cmd/kyc`** (no HTTP route; a reviewer runs this locally from `api/`
with a DynamoDB-scoped AWS session):

```bash
cd api
AWS_REGION=... TABLE_PREFIX=production_ KYC_DOCUMENTS_BUCKET=... go run ./cmd/kyc list
... go run ./cmd/kyc show <user_id>                          # raw CPF + presigned document URLs
... go run ./cmd/kyc approve <user_id> [-note "looks good"]
... go run ./cmd/kyc reject <user_id> -reason "blurry photo"
```

`list` and `approve`/`reject` work without `KYC_DOCUMENTS_BUCKET`; only `show` needs it.
`list` runs a DynamoDB **Scan** filtered on `kyc_doc_status = pending_review` —
acceptable for an offline operator tool, not a request path; a GSI on `kyc_doc_status`
is the scale upgrade if this ever needs to run hot.

`GET /v1.0/internal/kyc/:user_id` (scope `internal:wallet:confirm-deposit`) is the one
surviving internal route — it hands ctech-wallet the raw CPF for withdrawal-key
validation. Downstream services otherwise read the `kyc_level` claim from the JWT
(scope `kyc`, values `""` | `verified`) — no callback to this service on the hot path;
the level lands in tokens on the next refresh after approval. Audit events:
`kyc.submitted`, `kyc.document_uploaded`, `kyc.verified`, `kyc.rejected`.

### Step-up authentication (recent MFA)

Access tokens issued from a session carry `auth_time`, `amr` (RFC 8176: `pwd`,
`otp`, `webauthn`, `google`) and `last_mfa_at` claims. Sensitive routes
(change password, TOTP/passkey removal, backup-code regeneration, API key
creation, OAuth client mutations) run `RequireRecentMFA(5 min)`: when
`last_mfa_at` is missing or older than 5 minutes they answer
`403 step-up-required` (with `max_age_seconds`). The client completes
`POST /v1.0/auth/step-up` (TOTP or passkey), silent-refreshes to obtain a
token with the fresh claim, and retries. Users with no MFA enrolled get
`403 mfa-enrollment-required` from the challenge. `grant_type=api_key`
tokens carry none of these claims and can never pass step-up.

Every security-relevant event (logins, MFA challenges, password/MFA/key/client
mutations, session revocations, token-reuse detections) is recorded in the
`{env}_account_audit` table (TTL 400 days) and exposed to the account owner at
`GET /v1.0/account/activity`.

### Signing key rotation

Production signing keys live versioned in SSM:

| Parameter                                | Content                                   |
|------------------------------------------|-------------------------------------------|
| `/ctech-account/{env}/jwk/active`        | JSON `{kid, pem, created_at}` (SecureString) — signs new tokens |
| `/ctech-account/{env}/jwk/previous`      | Same shape — verify-only, served in JWKS  |

Every instance reloads the keys hourly. When the active key is older than
90 days, one instance (elected via Valkey `SET rotate_jwk_lock NX EX 3600`)
generates a new RSA-2048 key, demotes the old active to `previous` and keeps
both in JWKS — the previous key stays valid a full rotation period, far beyond
the 15-min access / 1-h id token lifetimes and any downstream JWKS cache.
Valkey absent → auto-rotation off; manual rotation always available:

```bash
cd api
go run ./cmd/rotatekeys -env prod -init   # one-time migration: wraps the legacy rsa-private-key (KID preserved)
go run ./cmd/rotatekeys -env prod         # forced manual rotation
```

Dev mode (`RSA_PRIVATE_KEY` env set) uses that single key and never rotates.

---

## Configuration

All configuration is read from environment variables at startup.

| Variable            | Required | Description                                                                                               |
|---------------------|----------|-----------------------------------------------------------------------------------------------------------|
| `ENVIRONMENT`       | Yes      | `production`, `staging`, or `development`                                                                 |
| `APP_VERSION`       | No       | Release identifier reported as `releaseId` on the health check (default `0.0.1`). Format `YYMMDDHHMM:<7-char commit>`, written by CI into `release.env` inside the deployment artifact and sourced by `start.sh` |
| `BASE_URL`          | Yes      | Go API public URL, e.g. `https://accountsapi.aoctech.app`                                                 |
| `APP_URL`           | No       | Frontend URL for login redirects (defaults to `BASE_URL`)                                                 |
| `PORT`              | No       | HTTP port (default `8080`)                                                                                |
| `DYNAMO_TABLE`      | Yes      | DynamoDB table name                                                                                       |
| `RSA_PRIVATE_KEY`   | Dev only | PEM-encoded RSA private key (RS256). When set, single-key dev mode — no rotation. When absent, keys load from SSM `/ctech-account/{env}/jwk/*` |
| `PUBLIC_KEY_KID`    | No       | Key ID for the env-provided key (derived from the public key when unset). Ignored in SSM mode            |
| `VALKEY_URL`        | No       | Redis-compatible URL; cache disabled when absent or invalid                                               |
| `FROM_EMAIL`        | No       | SES-verified sender address. When unset, email verification & password-reset emails are silently disabled |
| `KYC_DOCUMENTS_BUCKET` | No    | Private S3 bucket for KYC identity documents and selfie clips. When unset, KYC submission is unavailable entirely — document review is the only path |
| `AUDIENCE`          | No       | Expected `aud` claim on access tokens verified by this service (defaults to `BASE_URL`)                   |
| `ACCESS_TOKEN_TTL`  | No       | Access token lifetime in seconds (default `900`)                                                          |
| `REFRESH_TOKEN_TTL` | No       | Refresh token lifetime in seconds (default `2592000`)                                                     |
| `TRUSTED_PROXIES`   | No       | Comma-separated IPs/CIDRs whose `X-Forwarded-For` is trusted (e.g. `10.0.0.0/8`)                          |
| `SELF_CLIENT_ID`    | No       | OAuth `client_id` of this service's own frontend (default `accounts`, matching the `accounts-ui` seed). `/v1.0/account/*` and `/v1.0/step-up/*` reject any token whose `azp` doesn't match this — they have no scope of their own, so a downstream client's token (dfe's, or any consented third party) must never reach them |

---

## Running Locally

All Go commands below assume the working directory is `api/` (`cd api`).

```bash
cd api

# Start DynamoDB Local
docker run -p 8000:8000 amazon/dynamodb-local

# Export required vars
export ENVIRONMENT=development
export BASE_URL=http://localhost:8080
export DYNAMO_TABLE=ctech-account-dev
export RSA_PRIVATE_KEY="$(cat key.pem)"
export PUBLIC_KEY_ID=dev-key

# Run
go run ./cmd/api
```

---

## Testing

```bash
cd api

# Unit tests — all domain services
go test ./internal/domain/...

# Integration tests — all HTTP handlers (no AWS required)
go test ./internal/handler/...

# All tests
go test ./...
```

Integration tests use in-memory repository implementations — no real DynamoDB or Valkey needed.

---

## First Deploy Checklist

Run these once before the first production deployment. Order matters.

### 1 — Generate RSA key pair (RS256 for JWT signing)

```bash
# 4096-bit RSA key, no passphrase (Lambda/ECS reads it from env)
openssl genrsa -out key.pem 4096
openssl rsa -in key.pem -pubout -out key.pub

# Verify
openssl rsa -in key.pem -check -noout
```

### 2 — Store secrets in AWS SSM Parameter Store

```bash
REGION=eu-west-1
ENV=production

# RSA private key
aws ssm put-parameter \
  --name "/$ENV/ctech-account/RSA_PRIVATE_KEY" \
  --value "$(cat key.pem)" \
  --type SecureString --region $REGION

# Assign a key ID (any stable string, e.g. year + env)
aws ssm put-parameter \
  --name "/$ENV/ctech-account/PUBLIC_KEY_KID" \
  --value "2026-$ENV" \
  --type String --region $REGION

# Delete local private key after storing
rm key.pem
```

### 3 — Deploy CDK infrastructure

```bash
cd cdk
npm install
npx cdk bootstrap aws://ACCOUNT_ID/$REGION
npx cdk deploy --all
```

This creates: DynamoDB table, Lambda function, API Gateway, IAM roles, SSM read permissions.

### 4 — Seed the `accounts-ui` OAuth client in DynamoDB

The frontend SPA uses its own client ID for the authorization code flow. Write this item once
(schema: `pk = CLIENT_{client_id}` in the `{env}_account_oauth_clients` table):

```bash
TABLE=production_account_oauth_clients  # adjust to your environment prefix

aws dynamodb put-item --table-name $TABLE --region $REGION --item '{
  "pk":             {"S": "CLIENT_accounts"},
  "name":           {"S": "CTech Account"},
  "client_type":    {"S": "public"},
  "redirect_uris":  {"L": [{"S": "https://accounts.aoctech.app/login/callback"}]},
  "allowed_scopes": {"L": [{"S": "openid"}, {"S": "profile"}, {"S": "email"}]},
  "first_party":    {"BOOL": true},
  "owner_user_id":  {"S": "system"},
  "created_at":     {"S": "2026-01-01T00:00:00Z"}
}'
```

> `first_party: true` skips the consent screen — set it ONLY on platform-operated
> clients (accounts UI, dfe). It is deliberately not settable through the
> self-service API; user-registered applications always go through consent.

### 4b — Seed the scope catalog

`GET /v1.0/scopes` and all scope validation read the `{env}_ctech_scopes` table —
empty table means no scope can be granted. Seed it once per environment:

```bash
cd api
AWS_REGION=$REGION TABLE_PREFIX=production_ VALKEY_URL=$VALKEY_URL go run ./cmd/seedscopes
```

### 5 — Configure Next.js environment (Vercel / ECS / EC2)

```bash
API_URL=https://api-id.execute-api.eu-west-1.amazonaws.com/prod  # your API GW URL
NEXT_PUBLIC_API_URL=$API_URL
OAUTH_CLIENT_ID=accounts-ui
BASE_URL=https://accounts.aoctech.app
```

Set these in Vercel dashboard → Settings → Environment Variables, or in your ECS task definition.

### 6 — Deploy Next.js frontend

```bash
cd ui
npm run build  # verify clean build before deploy
# then: vercel deploy --prod  OR  docker build + push + ECS service update
```

### 7 — Smoke test

```bash
# Backend health
curl -s https://<api-gw-url>/healthz | jq .

# OIDC discovery
curl -s https://<api-gw-url>/.well-known/openid-configuration | jq .issuer

# JWKS (confirm your kid matches PUBLIC_KEY_KID)
curl -s https://<api-gw-url>/.well-known/jwks.json | jq '.keys[0].kid'

# Frontend
curl -sI https://accounts.aoctech.app/login  # expect 200
```

### 8 — Post-deploy

- Rotate the RSA key annually: generate new pair, update SSM, redeploy, update `PUBLIC_KEY_KID`.
- Enable DynamoDB Point-in-Time Recovery on the table.
- Set CloudWatch alarm on Lambda error rate > 1%.

---

## License

Elastic License 2.0 — see [LICENSE.md](LICENSE.md).
