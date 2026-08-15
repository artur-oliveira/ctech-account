# ctech-account

Centralized OAuth 2.0 + OpenID Connect Identity Provider for the aoctech.app platform.

Built with **Go 1.26** and **Fiber v3**. Runs on an EC2 Auto Scaling Group routed by
the CTech HAProxy edge load balancer, with CloudFront for the frontend. There is no
Lambda or API Gateway.

---

## Features

- **OAuth 2.0** — Authorization Code flow with PKCE
- **OpenID Connect** — Discovery document, JWKS, UserInfo endpoint
- **Persistent sessions** — Cookie-based refresh tokens with automatic rotation and token-reuse detection
- **API keys** — Long-lived scoped tokens for programmatic access
- **TOTP / MFA** — Time-based one-time passwords (Sprint 2)
- **WebAuthn / Passkeys** — Passwordless authentication (Sprint 2)
- **RFC 7807 Problem Details** — All error responses use `application/problem+json`
- **RFC Health Check** — `GET /v1.0/health-check` responds with `application/health+json`
- **DynamoDB** — Eight tables: `account_users`, `account_sessions`, `account_oauth_clients`, `account_api_keys`,
  `account_mfa`, `account_passkeys`, `account_audit`, `ctech_scopes`
- **Valkey** — Required in non-dev (see §Configuration): OAuth codes, MFA/passkey challenges, recovery tokens and rate
  limiting live in Valkey with no DynamoDB fallback; the API refuses to boot without it outside dev

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

| Method   | Path                                                | Auth                                   | Description                                                                                                                                                                                         |
|----------|-----------------------------------------------------|----------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `POST`   | `/v1.0/auth/register`                               | —                                      | Create a new account (`accept_terms: true` required). Always `202` — the response never reveals whether the email was already registered                                                            |
| `POST`   | `/v1.0/auth/login`                                  | —                                      | Password login; sets session cookie. `403 email-not-verified` until the address is confirmed                                                                                                        |
| `POST`   | `/v1.0/auth/logout`                                 | Optional                               | Revoke current session cookie                                                                                                                                                                       |
| `POST`   | `/v1.0/auth/verify-email`                           | —                                      | Confirm an email address from the emailed token                                                                                                                                                     |
| `POST`   | `/v1.0/auth/resend-verification`                    | —                                      | Resend the verification link (always `200`)                                                                                                                                                         |
| `POST`   | `/v1.0/auth/forgot-password`                        | —                                      | Request a reset link (always `200`)                                                                                                                                                                 |
| `POST`   | `/v1.0/auth/reset-password`                         | —                                      | Set a new password from the emailed token (revokes all sessions)                                                                                                                                    |
| `GET`    | `/v1.0/auth/google`                                 | —                                      | Start Google sign-in / sign-up (OpenID)                                                                                                                                                             |
| `GET`    | `/v1.0/auth/google/callback`                        | —                                      | Google OAuth callback. Existing accounts → session cookie. Brand-new accounts → redirected to the ui's `/accept-terms` interstitial (never saw the register form's checkbox)                        |
| `POST`   | `/v1.0/auth/accept-terms`                           | —                                      | One-time token (from the Google callback or the `/authorize` terms gate) + `accept_tos` / `accept_privacy` → stamps the pending documents. Issues the withheld session only for a suspended sign-up |
| `GET`    | `/v1.0/authorize`                                   | Session                                | OAuth authorization endpoint (redirects to the consent screen, or to `/accept-terms` when a ToS/Privacy version bump is pending)                                                                    |
| `POST`   | `/v1.0/authorize/consent`                           | Session                                | Record the consent decision; returns `redirect_to` (client `error=access_denied` on deny)                                                                                                           |
| `POST`   | `/v1.0/token`                                       | —                                      | OAuth token endpoint (`authorization_code`, `refresh_token`, `api_key`, `client_credentials` grants)                                                                                                |
| `GET`    | `/v1.0/userinfo`                                    | Bearer                                 | OIDC UserInfo                                                                                                                                                                                       |
| `GET`    | `/v1.0/scopes`                                      | —                                      | Grantable-scope catalog (code + descriptions, grouped by service) for UI pickers                                                                                                                    |
| `GET`    | `/v1.0/account/profile`                             | Bearer                                 | Get profile (includes `terms_pending: {tos, privacy}`, `has_password`, and `google_linked` — drive the in-app terms gate and the Link/Unlink Google UI)                                             |
| `PATCH`  | `/v1.0/account/profile`                             | Bearer                                 | Update profile                                                                                                                                                                                      |
| `POST`   | `/v1.0/account/terms/accept`                        | Bearer                                 | Re-accept the documents whose version moved (`accept_tos` / `accept_privacy`); returns the cleared `terms_pending`                                                                                  |
| `PUT`    | `/v1.0/account/password`                            | Bearer                                 | Change password (revokes all other sessions)                                                                                                                                                        |
| `POST`   | `/v1.0/account/password`                            | Bearer                                 | Set the first password on a Google-created account                                                                                                                                                  |
| `DELETE` | `/v1.0/account/link/google`                         | Bearer + step-up                       | Unlink the bound Google identity (refused for passwordless accounts, which would lose their only login method)                                                                                      |
| `GET`    | `/v1.0/account/sessions`                            | Bearer                                 | List active sessions                                                                                                                                                                                |
| `DELETE` | `/v1.0/account/sessions`                            | Bearer                                 | Revoke all other sessions                                                                                                                                                                           |
| `DELETE` | `/v1.0/account/sessions/:id`                        | Bearer                                 | Revoke a specific session                                                                                                                                                                           |
| `GET`    | `/v1.0/account/activity`                            | Bearer                                 | Security activity log (cursor pagination: `?cursor=&limit=`, newest first, 400-day retention)                                                                                                       |
| `GET`    | `/v1.0/account/api-keys`                            | Bearer                                 | List API keys                                                                                                                                                                                       |
| `POST`   | `/v1.0/account/api-keys`                            | Bearer                                 | Create API key                                                                                                                                                                                      |
| `DELETE` | `/v1.0/account/api-keys/:id`                        | Bearer                                 | Revoke API key                                                                                                                                                                                      |
| `GET`    | `/v1.0/account/oauth-clients`                       | Bearer                                 | List OAuth applications owned by the user                                                                                                                                                           |
| `POST`   | `/v1.0/account/oauth-clients`                       | Bearer                                 | Register an OAuth application (`client_secret` returned once for confidential clients)                                                                                                              |
| `PUT`    | `/v1.0/account/oauth-clients/:id`                   | Bearer                                 | Update name / redirect URIs / scopes / audience                                                                                                                                                     |
| `DELETE` | `/v1.0/account/oauth-clients/:id`                   | Bearer                                 | Delete an OAuth application                                                                                                                                                                         |
| `POST`   | `/v1.0/account/oauth-clients/:id/regenerate-secret` | Bearer                                 | Rotate the client secret (returned once)                                                                                                                                                            |
| `GET`    | `/v1.0/account/kyc`                                 | Bearer                                 | KYC status: `{state, level, cpf_masked, legal_name, birth_date, phone_masked, basic_verified_at, documents, rejection_reason, submitted_at, expires_at, verified_at}`                               |
| `POST`   | `/v1.0/account/kyc/basic`                           | Bearer + step-up                       | Submit Basic identity data `{cpf, legal_name, birth_date, phone_number}` → validates CPF/age/phone, sends an SMS OTP, `basic/pending`                                                               |
| `POST`   | `/v1.0/account/kyc/basic/verify-phone`              | Bearer + step-up                       | `{code}` → `basic/verified` on a correct 6-digit code                                                                                                                                               |
| `POST`   | `/v1.0/account/kyc/basic/resend-code`               | Bearer + step-up                       | Resends the OTP; 60s cooldown (`429` + `Retry-After`-equivalent `retry_after_seconds`)                                                                                                              |
| `POST`   | `/v1.0/account/kyc/documents`                       | Bearer + step-up                       | `{type, content_type}` → `{document_id, upload_url}` — presigned S3 PUT; `type` one of `id_front`, `id_back`, `selfie_with_document`; requires `basic/verified` first                               |
| `POST`   | `/v1.0/account/kyc/documents/confirm`               | Bearer + step-up                       | `{document_id, type}` → records the upload (verified via HeadObject)                                                                                                                                |
| `POST`   | `/v1.0/account/kyc/enhanced`                        | Bearer + step-up                       | Finalizes an Enhanced submission once all 3 documents are uploaded → `enhanced/pending`                                                                                                             |
| `GET`    | `/v1.0/internal/kyc/:user_id`                       | Service token (`internal:account:kyc`) | Full unmasked identity record incl. `phone_number` (ctech-wallet withdrawal-key validation)                                                                                                         |
| `GET`    | `/v1.0/internal/resource-servers/:id/manifest`      | Bound publisher token                  | Current manifest and revision ETag                                                                                                                                                                  |
| `PUT`    | `/v1.0/internal/resource-servers/:id/manifest`      | Bound publisher token + `If-Match`     | Idempotently reconcile the service-owned scope manifest                                                                                                                                             |
| `GET`    | `/v1.0/account/consents`                            | Bearer                                 | List connected apps (consent grants)                                                                                                                                                                |
| `DELETE` | `/v1.0/account/consents/:clientID`                  | Bearer                                 | Revoke a consent grant                                                                                                                                                                              |
| `POST`   | `/v1.0/auth/mfa/challenge`                          | —                                      | Exchange MFA token + TOTP code for session (TOTP is the only MFA method — passkey is never a second factor)                                                                                         |
| `POST`   | `/v1.0/auth/step-up`                                | Bearer                                 | Step-up challenge: `{method:"totp",code}` → stamps fresh MFA proof on the session (rate-limited)                                                                                                    |
| `POST`   | `/v1.0/auth/step-up/passkeys/begin`                 | Bearer                                 | Step-up WebAuthn assertion challenge for the current user                                                                                                                                           |
| `POST`   | `/v1.0/auth/step-up/passkeys/complete`              | Bearer                                 | Validate step-up assertion → stamps fresh MFA proof                                                                                                                                                 |
| `POST`   | `/v1.0/auth/passkeys/authenticate/begin`            | —                                      | Discoverable WebAuthn challenge used by the explicit passkey button and privacy-preserving Conditional UI — passkey is a primary, password-replacing factor                                         |
| `POST`   | `/v1.0/auth/passkeys/authenticate/complete`         | —                                      | Validate assertion → session cookie                                                                                                                                                                 |
| `GET`    | `/v1.0/account/mfa/totp/setup`                      | Bearer                                 | Generate TOTP provisioning URI                                                                                                                                                                      |
| `POST`   | `/v1.0/account/mfa/totp/confirm`                    | Bearer                                 | Activate TOTP + get backup codes                                                                                                                                                                    |
| `DELETE` | `/v1.0/account/mfa/totp`                            | Bearer                                 | Remove TOTP from account                                                                                                                                                                            |
| `POST`   | `/v1.0/account/mfa/totp/backup-codes`               | Bearer                                 | Regenerate backup codes                                                                                                                                                                             |
| `GET`    | `/v1.0/account/mfa/passkeys`                        | Bearer                                 | List registered passkeys                                                                                                                                                                            |
| `POST`   | `/v1.0/account/mfa/passkeys/register/begin`         | Bearer                                 | WebAuthn registration challenge                                                                                                                                                                     |
| `POST`   | `/v1.0/account/mfa/passkeys/register/complete`      | Bearer                                 | Validate attestation → persist credential                                                                                                                                                           |
| `DELETE` | `/v1.0/account/mfa/passkeys/:id`                    | Bearer                                 | Remove a passkey                                                                                                                                                                                    |
| `GET`    | `/.well-known/openid-configuration`                 | —                                      | OIDC Discovery document                                                                                                                                                                             |
| `GET`    | `/.well-known/jwks.json`                            | —                                      | JSON Web Key Set                                                                                                                                                                                    |
| `GET`    | `/v1.0/health-check`                                | —                                      | Health check (`application/health+json`)                                                                                                                                                            |

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
  flow. `kyc` adds the `kyc_level` claim (`""` | `"basic"` | `"verified"`) to access tokens, id_tokens
  and userinfo — CPF, birth date and legal name never enter tokens.
- **Service scopes**: `service:resource:action` (e.g. `dfe:nfe:issue`, `account:*:read`) —
  permissions on a downstream resource server. `*` is allowed as a full resource or action
  segment; the action segment may be omitted to grant all actions on a resource.

The `internal` service (e.g. `internal:account:kyc`) is machine-to-machine
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

Downstream services own versioned manifests and publish them with a dedicated,
namespace-bound confidential client. No Account code change or catalog seed is
needed. The registry validates grammar/ownership, reconciles with `If-Match`,
keeps immutable revisions, invalidates the Valkey catalog, and prevents direct
removal of an active scope. See
[`docs/resource-server-scope-registry.md`](docs/resource-server-scope-registry.md)
for bootstrap, migration, rollback, manifest schema, and CI wiring.

`cmd/seedscopes` remains the compatibility/bootstrap source for OIDC and
Account-owned scopes. V2 `RESOURCE_SERVER` rows override legacy downstream
`SERVICE` rows, so the migration can happen without a flag day.

Services marked `Internal: true` in the seed are hidden from `GET /v1.0/scopes` and
rejected by self-service client/API-key creation — their scopes are assigned only via
operator seed. `internal` is a shared machine-to-machine namespace, not a single
service: each downstream consumer gets its own catalog entry keyed
`internal:<service>` (e.g. `internal:wallet`) with its own `Audience`, so
`AudiencesFor` resolves the right `aud` claim per target instead of lumping every
internal scope under one bucket. After deploying a release that adds catalog entries
(e.g. `kyc`, `internal:account:kyc`), re-run `go run ./cmd/seedscopes` per
environment. ctech-wallet's M2M client is seeded confidential + `first_party: true` +
`allowed_scopes: ["internal:account:kyc"]` (direct DynamoDB put, same as
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

### Provisioning confidential M2M clients

Operators create trusted clients for the `client_credentials` grant with the
`cmd/createclient` CLI (run from `api/`):

```bash
AWS_REGION=us-east-1 TABLE_PREFIX=production_ go run ./cmd/createclient \
  -client-id wallet-worker \
  -name "Wallet worker" \
  -scopes internal:account:kyc,internal:wallet:credit \
  -ssm-path-client /ctech/wallet/client-id \
  -ssm-path-secret /ctech/wallet/client-secret
```

The client ID, name and scopes are required. The two SSM paths are optional and
independent. When present, the client ID is stored as `String` and the secret as
`SecureString`; existing parameters are not overwritten. The command validates
parameter paths before creating the client. It also validates client ID, scope grammar and registration
in the runtime catalog, rejects OIDC identity scopes and duplicate client IDs, then
stores a confidential first-party client with `owner_user_id = "system"`. The client
secret is printed only when `-ssm-path-secret` is absent or an SSM write fails, so
the operator can recover a client that was already created. Token audiences are
derived from the registered scopes' catalog entries rather than configured on the
client.

### Service-to-service tokens (`client_credentials`)

First-party **confidential** clients (e.g. ctech-wallet) obtain machine tokens directly:

```
POST /v1.0/token
grant_type=client_credentials&client_id=...&client_secret=...&scope=internal:account:kyc
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

**Two-level Basic/Enhanced verification.** Splits KYC into two distinct levels to simplify user onboarding:

- **Basic KYC**: Users submit their CPF, full legal name, date of birth, and phone number (
  `POST /v1.0/account/kyc/basic`). The system validates the input, claims the CPF transactionally, and dispatches a
  6-digit verification code via AWS SNS SMS. Entering this code (`POST /v1.0/account/kyc/basic/verify-phone`) verifies
  the phone and grants Basic access (`kyc_level = "basic"`). Valkey cache stores the OTP hashed with a 10-minute TTL, a
  60-second resend cooldown (`429 kyc-resend-cooldown` with `retry_after_seconds`), and enforces a 5-attempt guess
  limit.
- **Enhanced KYC**: Requires Basic verification to be completed first. Users upload three required documents: ID front,
  ID back, and a selfie holding the document (`POST /v1.0/account/kyc/documents` and `confirm`). A static photo of the
  selfie holding the document replaces the legacy four-clip video flow. Once all documents are uploaded, submitting the
  verification (`POST /v1.0/account/kyc/enhanced`) queues the profile for manual review (`kyc_level = "basic"`, state
  `under_review`).

`GET /v1.0/account/kyc` returns the user-facing status details with masked CPF and phone, and a derived `state`
representing the lifecycle: `not_started`, `awaiting_phone_verification`, `basic_verified`, `under_review`, `rejected`,
and `verified`. If a pending Enhanced submission is not reviewed within 30 days (`SubmissionTTL`), it expires and
reverts to `basic_verified` access. While a submission is under review, documents and resubmissions are locked (
`409 kyc-submission-locked`). Rejection clears the uploaded documents and requires a fresh upload cycle.

An informational-only risk evaluation hook scores each submission at `SubmitBasic` and `SubmitEnhanced` based on client
IP and user details (via a pluggable risk evaluator, defaulting to a zero-score no-op implementation) and persists the
latest evaluation.

**Manual review — `cmd/kyc`** (CLI tool, no HTTP endpoints):
Reviewers list and review Enhanced submissions from a CLI environment:

```bash
cd api
AWS_REGION=... TABLE_PREFIX=production_ KYC_DOCUMENTS_BUCKET=... go run ./cmd/kyc list
... go run ./cmd/kyc show <user_id>                          # prints raw CPF, phone, risk, and S3 document URLs
... go run ./cmd/kyc approve <user_id> [-note "looks good"]  # sets kyc_level=enhanced, status=verified
... go run ./cmd/kyc reject <user_id> -reason "blurry photo"  # clears documents, sets status=rejected
```

`GET /v1.0/internal/kyc/:user_id` is the internal machine-to-machine route that returns the raw unmasked CPF, legal
name, birth date, and phone number for withdrawal validations. Downstream JWT consumers map `kyc_level` claim values to
`""` | `"basic"` | `"verified"` via `ClaimLevel` (no change in `ctech-wallet` or `ctech-dfe`).
Audit events: `kyc.submitted`, `kyc.phone_verified`, `kyc.document_uploaded`, `kyc.verified`, `kyc.rejected`.

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

Login AMR preserves the complete authentication chain: password + TOTP issues
`["pwd","otp"]`, passkey alone issues `["webauthn"]`, and passkey + TOTP issues
`["webauthn","otp"]`. Passkey is never represented as password and is never a
second-factor gate after a password login.

Every security-relevant event (logins, MFA challenges, password/MFA/key/client
mutations, session revocations, token-reuse detections) is recorded in the
`{env}_account_audit` table (TTL 400 days) and exposed to the account owner at
`GET /v1.0/account/activity`.

### Signing key rotation

Production signing keys live versioned in SSM:

| Parameter                           | Content                                                         |
|-------------------------------------|-----------------------------------------------------------------|
| `/ctech-account/{env}/jwk/active`   | JSON `{kid, pem, created_at}` (SecureString) — signs new tokens |
| `/ctech-account/{env}/jwk/previous` | Same shape — verify-only, served in JWKS                        |

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

| Variable                     | Required | Description                                                                                                                                                                                                                                                                                                                   |
|------------------------------|----------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `ENVIRONMENT`                | Yes      | `dev`, `stage`, or `prod`                                                                                                                                                                                                                                                                                                     |
| `APP_VERSION`                | No       | Release identifier reported as `releaseId` on the health check (default `0.0.1`). Format `YYMMDDHHMM:<7-char commit>`, written by CI into `release.env` inside the deployment artifact and sourced by `start.sh`                                                                                                              |
| `BASE_URL`                   | Yes      | Go API's **own** public URL, e.g. `https://accountsapi.aoctech.app`. Used for the OAuth issuer/`Audience` default — never the browser-facing origin                                                                                                                                                                           |
| `APP_URL`                    | No       | Frontend **SPA** URL, e.g. `https://accounts.aoctech.app` (defaults to `BASE_URL`, which is only correct when API and SPA share an origin, e.g. local dev). This is the origin WebAuthn ceremonies actually run on — `RPID`/`RPOrigins` derive from this, not from `BASE_URL`                                                 |
| `WEBAUTHN_RPID`              | No       | WebAuthn Relying Party ID (registrable domain, e.g. `aoctech.app`). Defaults to `APP_URL`'s hostname. Set explicitly if multiple SPA subdomains must share credentials; the EC2 bootstrap reads the optional `/ctech-account/{env}/webauthn-rpid` SSM parameter                                                               |
| `PORT`                       | No       | HTTP port (default `8001`)                                                                                                                                                                                                                                                                                                    |
| `DYNAMO_TABLE`               | Yes      | DynamoDB table name                                                                                                                                                                                                                                                                                                           |
| `RSA_PRIVATE_KEY`            | Dev only | PEM-encoded RSA private key (RS256). When set, single-key dev mode — no rotation. When absent, keys load from SSM `/ctech-account/{env}/jwk/*`                                                                                                                                                                                |
| `PUBLIC_KEY_KID`             | No       | Key ID for the env-provided key (derived from the public key when unset). Ignored in SSM mode                                                                                                                                                                                                                                 |
| `VALKEY_URL`                 | Non-dev  | Redis-compatible URL; **required outside dev** — the API refuses to boot without it (OAuth codes, MFA tokens and rate limiting have no DynamoDB fallback)                                                                                                                                                                     |
| `FROM_EMAIL`                 | No       | SES-verified sender address. When unset, email verification & password-reset emails are silently disabled                                                                                                                                                                                                                     |
| `KYC_DOCUMENTS_BUCKET`       | No       | Private S3 bucket for KYC identity documents and selfie clips. When unset, Enhanced document verification is unavailable                                                                                                                                                                                                      |
| `PHONE_VERIFICATION_ENABLED` | No       | `false` unless set to `true`. Gates AWS SNS phone verification — while false, every `/kyc/basic*` route returns `503`. Flip once production SNS SMS access is granted; no redeploy needed beyond the env var                                                                                                                  |
| `AUDIENCE`                   | No       | Expected `aud` claim on access tokens verified by this service (defaults to `BASE_URL`)                                                                                                                                                                                                                                       |
| `ACCESS_TOKEN_TTL`           | No       | Access token lifetime in seconds (default `900`)                                                                                                                                                                                                                                                                              |
| `REFRESH_TOKEN_TTL`          | —        | Not an env var — refresh-token lifetime is a fixed code constant (`SessionTTL`, 90 days); nothing to configure                                                                                                                                                                                                                |
| `TRUSTED_PROXIES`            | No       | Comma-separated IPs/CIDRs whose `X-Forwarded-For` is trusted (e.g. `10.0.0.0/8`)                                                                                                                                                                                                                                              |
| `SELF_CLIENT_ID`             | No       | OAuth `client_id` of this service's own frontend (default `accounts`, matching the `accounts-ui` seed). `/v1.0/account/*` and `/v1.0/step-up/*` reject any token whose `azp` doesn't match this — they have no scope of their own, so a downstream client's token (dfe's, or any consented third party) must never reach them |

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
# 4096-bit RSA key, no passphrase (dev mode: set RSA_PRIVATE_KEY to this key's contents)
openssl genrsa -out key.pem 4096
openssl rsa -in key.pem -pubout -out key.pub

# Verify
openssl rsa -in key.pem -check -noout
```

### 2 — Provision the signing key in AWS SSM Parameter Store

Production signing keys live versioned in SSM — the API loads `/ctech-account/{env}/jwk/active`
and `/ctech-account/{env}/jwk/previous`. There is **no** `RSA_PRIVATE_KEY` SSM parameter in
production; generate and store the first key with the rotation tool:

```bash
cd api
REGION=eu-west-1
ENV=production

go run ./cmd/rotatekeys -env $ENV -init   # writes /ctech-account/$ENV/jwk/active (+ jwk/previous)
```

`jwk/active` is a `SecureString` JSON `{kid, pem, created_at}`. Runtime config
(`base-url`, `allowed-origins`, `app-url`, `google-client-id`, `google-client-secret`,
`cookie-domain`, `from-email`, `internal-token`) is also read from SSM under
`/ctech-account/{env}/<param>` at container start. In dev only, a single key is supplied via the
`RSA_PRIVATE_KEY` env var and never rotates.

### 3 — Deploy CDK infrastructure

```bash
cd cdk
npm install
npx cdk bootstrap aws://ACCOUNT_ID/$REGION
npx cdk deploy --all
```

This creates: the eight DynamoDB tables, an EC2 Auto Scaling Group registered with
the CTech HAProxy edge load balancer, CloudFront, IAM roles, and SSM read permissions.
There is no Lambda or API Gateway.

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

### 5 — Configure the SPA environment (EC2 Auto Scaling Group / CloudFront)

```bash
# In production CloudFront forwards /v1.0/* and /.well-known/* to HAProxy, so the SPA calls the API same-origin:
NEXT_PUBLIC_API_URL=https://accounts.aoctech.app
OAUTH_CLIENT_ID=accounts-ui
```

Set these as build environment variables for the static-export SPA (or your container/deploy pipeline) — there is no
Vercel or ECS runtime.

Separately, on the **API's** own deployment config, set `APP_URL=https://accounts.aoctech.app` (the SPA's real origin)
alongside `BASE_URL=https://accountsapi.aoctech.app` (the API's own origin) — see the Configuration table above.
`APP_URL` is what WebAuthn's RPID derives from; a `BASE_URL`/`APP_URL` mixup here breaks every passkey ceremony in
production with a `SecurityError`.
Only create `/ctech-account/{env}/webauthn-rpid` when credentials must be shared by
multiple SPA subdomains; otherwise leaving it absent is the safest configuration.

### 6 — Deploy the static-export frontend

```bash
cd ui
npm run build  # verify clean build before deploy (static export)
# then: docker build + push, and update the EC2 Auto Scaling Group / container deploy pipeline
```

### 7 — Smoke test

```bash
# Backend health
curl -s https://<haproxy-or-cloudfront-url>/v1.0/health-check | jq .

# OIDC discovery
curl -s https://<haproxy-or-cloudfront-url>/.well-known/openid-configuration | jq .issuer

# JWKS (confirm your kid matches the `kid` in /ctech-account/{env}/jwk/active)
curl -s https://<haproxy-or-cloudfront-url>/.well-known/jwks.json | jq '.keys[0].kid'

# Frontend
curl -sI https://accounts.aoctech.app/login  # expect 200
```

### 8 — Post-deploy

- Rotate the signing key annually: `go run ./cmd/rotatekeys -env <env>` writes a new `jwk/active` and demotes the old to
  `jwk/previous` — no redeploy needed.
- Once AWS grants production SNS SMS access, run `aws sns set-sms-attributes` once per account/region to set the monthly
  spend limit, then set `PHONE_VERIFICATION_ENABLED=true` in SSM — this is outside CDK's scope (no such resource exists
  to provision).
- Enable DynamoDB Point-in-Time Recovery on all eight tables.
- Set a CloudWatch alarm on the EC2 and HAProxy error rate > 1%.

---

## License

Elastic License 2.0 — see [LICENSE.md](LICENSE.md).
