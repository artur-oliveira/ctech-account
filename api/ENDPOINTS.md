# API Endpoints — `ctech-account`

Anchored to the code as of 2026-07-20. Traced from route registration
(`api/cmd/api/main.go`) through each handler in `api/internal/handler` →
service → repository. The only source of truth is the implementation; where the
code and the prose README diverge, the code wins (see "Divergences" at the end).

## Conventions

- **Base path**: `/v1.0`. Well-known is at `/.well-known`.
- **All errors** are RFC 7807 `application/problem+json` via `apierror.*`
  (`internal/handler/helpers.go:157` `parseBody`, `api/cmd/api/main.go:213` error handler).
- **Auth middleware chains** (defined in `internal/middleware`):
  - `RequireAuth(jwtSvc)` — verifies `Authorization: Bearer` JWT, populates locals
    (`middleware/auth.go:20`).
  - `RequireClientID(id)` — token `azp` claim must equal `id` (`middleware/auth.go:135`).
    Guards self-service account endpoints (no scope governs them).
  - `RequireInternalScope(scope)` — bearer token with empty `sid` (machine token)
    carrying the scope (`middleware/auth.go:148`).
  - `RequireRecentMFA(maxAge)` — JWT `last_mfa_at` claim within `maxAge`
    (default 5 min, `middleware/stepup.go:11`).
  - Rate limiters: `login`/`pwreset`/`token` count only failures (5 / 15 min / IP,
    `middleware/ratelimit.go:31`); `/account/*` uses `perUserLimiter` (100 / min / user).
- **Cookies** (`internal/handler/helpers.go`):
  - `ctech_session` — HttpOnly SSO session token. Set on login/register/MFA/social.
    Read by `GET /authorize`, `GET /auth/end-session`, `POST /auth/logout`.
  - `ctech_rt` — HttpOnly per-client refresh token. Set only by the `/token` code
    exchange & refresh. Read by refresh & revoke.
  - `ctech_auth` — **non**-HttpOnly hint marker (`"1"`); tells the SPA a session
    may exist so it knows whether a silent refresh is worth attempting.
- **Token lifetimes**: access token `expires_in: 900` (15 min, `token.go:43`,
  `config.AccessTokenTTL`); `ctech_rt` cookie max-age `90*24*3600` (`token.go:26`).
- **User vs M2M**: "M2M" = `client_credentials` (service-to-service, no `sid`)
  and the internal KYC read; `api_key` grant mints a token from a user-owned key
  consumed by resource servers. Everything under `/account/*` and `/auth/step-up/*`
  is a **user** endpoint gated on `azp == SELF_CLIENT_ID`.

---

## Well-known (no auth)

### `GET /.well-known/openid-configuration`
OIDC discovery. No auth, no side effects.
Response: `issuer`, `authorization_endpoint`, `token_endpoint`,
`userinfo_endpoint`, `revocation_endpoint`, `end_session_endpoint`, `jwks_uri`,
`response_types_supported`, `scopes_supported`, `id_token_signing_alg_values_supported`,
`token_endpoint_auth_methods_supported`, `claims_supported`,
`code_challenge_methods_supported`, `grant_types_supported`
(`internal/handler/wellknown.go:23`).
> Note: `grant_types_supported` lists only `authorization_code` + `refresh_token`,
> but `api_key` and `client_credentials` are implemented (see Divergences).

### `GET /.well-known/jwks.json`
Returns `{ "keys": [...] }` from `jwtSvc.PublicKeyJWKs()` (`wellknown.go:44`). No auth.

---

## `GET /v1.0/health-check` (no auth)
Registered at `main.go:260`. Returns `application/health+json`
(`cmd/api/health.go:41`). Side effects: **DynamoDB `DescribeTable` ping**,
**Valkey `Ping`**, CPU & memory reads off `/proc`.
- `status`: `pass` | `warn` | `fail`. Overall `fail` (HTTP 503) if any of
  dynamodb/valkey/cpu/mem = `fail`.
- Valkey reports `fail` when required and unavailable, `warn` otherwise
  (`health.go:113`). Body: `{ status, version, releaseId, serviceId, description, checks }`.

---

## `GET /v1.0/scopes` (no bearer auth)
Public scope catalog (`internal/handler/scopes.go:19`). Side effect: reads the
scope catalog (DynamoDB `{env}_ctech_scopes`, cached in Valkey). Internal
services are filtered out (`scopes.FilterPublic`). Response `{ services: [{ service, name, scopes: [{scope, description, description_pt}] }] }`.

---

## Auth — `POST/GET /v1.0/auth/*` (no `RequireAuth` unless noted)

### `POST /v1.0/auth/register`
Request `{ email, password (min 8), first_name, last_name?, accept_terms:bool }`
(`auth.go:73`). Business rule: `accept_terms` required; email lowercased/trimmed.
Response is **always** `202 { pending_verification:true, message }` for both new and
already-registered addresses (anti-enumeration, `auth.go:81-118`).
Side effects: DynamoDB user write; if `FROM_EMAIL` set, async verification email
(`auth.go:113`); audit `terms_accepted`.

### `POST /v1.0/auth/login`  *(rate-limited: 5 fail / 15 min / IP)*
Request `{ email, password }` (`auth.go:129`). Hard gate: unverified email →
`403 email-not-verified` (`auth.go:144`). Invalid creds → generic `InvalidCredentials`
(`auth.go:154`, no account-exists signal).
- If user has TOTP **or** a passkey enrolled → `200 { requires_mfa:true, mfa_token, mfa_methods:[...] }`
  and a single-use `mfa_token` is stored in Valkey (`mfa_token:<hash>`, TTL 5 min,
  `auth.go:175`). **No cookie yet.**
- Else issues a session: sets `ctech_session` + `ctech_auth` cookies, returns
  `200 { user_id, email, first_name, last_name, session_id }` (`auth.go:204`).
Side effects: DynamoDB session create; async geo enrichment (`helpers.go:67`);
audit `login_success` / `login_mfa_required` / `login_failed`.

### `POST /v1.0/auth/logout` (no auth)
Revokes the SSO session (from `ctech_session` cookie) and the SPA refresh token
(from `ctech_rt` cookie) server-side, then clears both cookies + `ctech_auth`
(`auth.go:375`). `204`. No token reuse.

### `GET /v1.0/auth/end-session` (no auth; SSO-cookie authenticated)
RP-initiated logout (`auth.go:404`). Revokes session, clears cookies, redirects to
`post_logout_redirect_uri` only if the client allows it, else `AppURL/login`.

### `POST /v1.0/auth/mfa/challenge`
Request `{ mfa_token, code }` (`auth.go:225`). Consumes the `mfa_token` atomically
(single-use), validates TOTP, issues session (`ctech_session` + `ctech_auth`),
returns user info (`auth.go:230`).

### `POST /v1.0/auth/mfa/passkey/begin`
Request `{ mfa_token }` (`auth.go:276`). Peeks (does **not** consume) the
`mfa_token`, returns `{ session_token, options }` WebAuthn assertion challenge.

### `POST /v1.0/auth/mfa/passkey/complete`
Query `mfa_token`, `session_token`; body = raw WebAuthn assertion JSON (`auth.go:318`).
Consumes `mfa_token`, finishes passkey auth, issues session.

### `POST /v1.0/auth/verify-email`
Request `{ token }` (`auth.go:443`). Consumes `ev:<hash>` Valkey token (single-use),
marks email verified. `200 { verified:true }`.

### `POST /v1.0/auth/resend-verification`  *(rate-limited)*
Request `{ email }`. Always `200 { sent:true }` (anti-enum); sends verification
email only if user exists & unverified (`auth.go:467`).

### `POST /v1.0/auth/forgot-password`  *(rate-limited)*
Request `{ email }`. Always `200 { sent:true }`; stores `pr:<hash>` Valkey token
(TTL 15 min) and sends reset email if user exists (`auth.go:485`).

### `POST /v1.0/auth/reset-password`  *(rate-limited)*
Request `{ token, new_password (min 8) }` (`auth.go:513`). Consumes `pr:<hash>`,
force-sets password, **revokes ALL sessions** for the user (`auth.go:544`).
`200 { reset:true }`.

---

## Social — `GET/POST /v1.0/auth/*` (no `RequireAuth`)

### `GET /v1.0/auth/google`
Redirects to Google OAuth. Requires `GOOGLE_CLIENT_ID` + Valkey (`social.go:45`).
Stores `gs:<hash>` Valkey state (TTL 10 min) and `continue` URL.

### `GET /v1.0/auth/google/callback`
Exchanges Google code, fetches profile (Google email must be verified). Links Google
to an existing SSO session if present; otherwise find-or-create user. New Google
accounts are sent to the accept-terms interstitial. Issues `ctech_session` +
`ctech_auth` and redirects to `continue` (`social.go:77`). Open-redirect guarded by
`isAllowedContinueURL` (`social.go:247`).

### `POST /v1.0/auth/accept-terms`
Request `{ token, accept_tos, accept_privacy }` (`terms.go:118`). Token-authenticated
(`terms_token:<hash>` in Valkey, single-use). Stamps the documents the user
actually owes (recomputed server-side from current ToS/Privacy versions, never trusts
the client flags — `terms.go:76`). Issues a session for brand-new signups; existing
accounts keep theirs. Returns `{ redirect }` (`social.go:198`).

---

## Authorization — `GET/POST /v1.0/authorize*`

### `GET /v1.0/authorize`
Authenticates with the **`ctech_session` SSO cookie, not a bearer token**
(`authorize.go:118`). Query: `client_id`, `redirect_uri`, `response_type=code`,
`scope`, `state`, `code_challenge`, `code_challenge_method=S256`, `nonce`, `max_age`.
Business rules (all redirect to `redirect_uri` with OAuth error params once the URI
is validated):
- client + `redirect_uri` must be registered (`authorize.go:86-96`);
- `response_type` must be `code`; PKCE `S256` required for public clients
  (`authorize.go:99-114`);
- scopes filtered by `client.FilterScopes` — at least one valid (`authorize.go:106`);
- **`max_age`**: if `now - session.AuthTime > max_age`, force re-login
  (`authorize.go:138`) — this is how a downstream app triggers fresh MFA via `max_age=0`;
- **terms gate**: a ToS/Privacy version bump redirects to the accept-terms
  interstitial (`authorize.go:152`);
- **consent gate**: third-party clients require an existing grant; first-party clients
  skip it (`authorize.go:170`).
On success: mints a single-use auth code in Valkey (`codeRepo.Store`) and
`302` redirects to `redirect_uri?code=...&state=...` (`authorize.go:180-206`).

### `POST /v1.0/authorize/consent`
Request `{ req (base64 of the original /authorize URL), approved:bool }`
(`authorize.go:253`). SSO-cookie authenticated. Re-validates the referenced
`/authorize` URL server-side (no open redirect). On approve, records the consent
grant and returns `{ redirect_to: <api>/v1.0/authorize?... }` so the browser
re-runs authorize and issues the code. On deny, returns `{ redirect_to: <redirect_uri>?error=access_denied }`.

---

## Token — `POST /v1.0/token`, `POST /v1.0/revoke`  *(no `RequireAuth`; `/token` rate-limited)*

### `POST /v1.0/token`
Dispatch on `grant_type` (`token.go:92`):

**`authorization_code`** (user) — `{ code, client_id, redirect_uri, code_verifier? }`.
Validates client; confidential clients need `client_secret`; consumes the code
atomically from Valkey; verifies PKCE; loads user + session; signs access token with
`azp = client_id` (`token.go:265`). If `openid` in scopes, also mints an
`id_token`. Calls `sessionSvc.IssueClientToken` → sets the `ctech_rt` HttpOnly
cookie (+ `ctech_auth` hint). **Public clients get the refresh token ONLY in the
cookie; confidential clients also receive it in the JSON body** (`token.go:295-305`).
Response `{ access_token, token_type, expires_in:900, id_token?, scope }`.

**`refresh_token`** (user) — refresh token from `ctech_rt` cookie or `refresh_token`
form field; `client_id` required (`token.go:308`). `sessionSvc.RotateClientToken`
detects reuse (`TokenReuse`) / expiry / client mismatch. Rotates the `ctech_rt`
cookie. Scopes are clamped to the originally-granted set re-filtered by the
client's current allowed scopes (`token.go:369-375`); `kyc_level` claim refreshed if
`kyc` scope present. Same cookie/public-vs-confidential rule as above.

**`api_key`** (user-owned key, consumed by resource servers) — `{ api_key }`
(`token.go:110`). Authenticates the raw key, signs an access token with
`azp = "api-key"` and `aud` = this IdP + every service named by the key's scopes.
No refresh token, no cookie.

**`client_credentials`** (M2M) — `{ client_id, client_secret, scope? }`
(`token.go:145`). Restricted to **confidential first-party** clients
(`!IsPublic() && FirstParty`). Signs access token with `sub = client_id`, **empty
`sid`** (so `RequireInternalScope` accepts it), `azp = client_id`, no refresh,
no MFA/KYC claims. Used by ctech-wallet for `internal:*` scopes.

### `POST /v1.0/revoke`
Request `{ token }` or the `ctech_rt` cookie (`token.go:411`). If the token is a
per-client refresh token → `RevokeClientToken`; else if it validates as an SSO
session token → revoke the whole session. Clears `ctech_rt`. `200 { revoked:true }`.

### `GET /v1.0/userinfo` *(user, `RequireAuth`)*
Returns `{ sub, email, name, preferred_username, given_name, family_name,
email_verified }`; adds `kyc_level` when the token carries the `kyc` scope and the
user has a level (`userinfo.go:21`).

---

## Step-up — `POST /v1.0/auth/step-up*`  *(user; `RequireAuth` + `RequireClientID(SELF_CLIENT_ID)` + rate-limited 5/15min/user)*

These re-prove possession of an MFA factor and stamp a fresh `last_mfa_at` on the
session. The client must then silent-refresh to get a token that passes
`RequireRecentMFA`.

### `POST /v1.0/auth/step-up`
Request `{ method:"totp", code (6 digits) }` (`stepup.go:56`). Requires an
enrolled MFA method (`MFAEnrollmentRequired` otherwise). Validates TOTP,
`RecordMFA(AMR_TOTP)`. `204`.

### `POST /v1.0/auth/step-up/passkeys/begin`
Returns `{ session_token, options }` WebAuthn assertion challenge (`stepup.go:85`).

### `POST /v1.0/auth/step-up/passkeys/complete`
Query `session_token`; body = raw WebAuthn assertion. Finishes passkey auth,
`RecordMFA(AMR_WebAuthn)`. `204`.

---

## Account — `GET/POST/PUT/DELETE /v1.0/account/*`  *(user; `RequireAuth` + `RequireClientID(SELF_CLIENT_ID)` + 100 req/min/user)*

`stepUp` in a route below means the handler is additionally wrapped in
`RequireRecentMFA(StepUpMaxAge=5min)` (`main.go:298`).

### Profile
- `GET /v1.0/account/profile` → `{ user_id, email, first_name, last_name,
  display_name, avatar_url, email_verified, has_password, google_linked,
  terms_pending:{tos,privacy}, created_at }` (`profile.go:37`).
- `PUT /v1.0/account/profile` → `{ first_name, last_name?, display_name? }`.
- `PUT /v1.0/account/password` *(stepUp)* → `{ current_password, new_password }`;
  on success **revokes all other sessions** (`profile.go:107`).
- `POST /v1.0/account/password` → `{ new_password }`; sets an initial password for
  Google-only accounts (refuses if one already exists); revokes other sessions.
- `DELETE /v1.0/account/link/google` *(stepUp)* → unlinks Google (refused if the
  account has no password).

### Sessions
- `GET /v1.0/account/sessions` → `{ sessions:[{ session_id, device_name, ip_address, created_at, last_used_at, is_current, geo_* }] }`.
- `DELETE /v1.0/account/sessions/:id` → revoke one (refuses the current session;
  use logout instead). `204`.
- `DELETE /v1.0/account/sessions` → revoke all **except** current. `204`.

### API keys  *(create stepUp-gated)*
- `GET /v1.0/account/api-keys` → `{ api_keys:[{ key_id, key_prefix, name, scopes, last_used_at, expires_at, created_at }] }`.
- `POST /v1.0/account/api-keys` *(stepUp)* → `{ name, scopes? (max 20),
  expires_in_days? (0–365) }`. Default scope `account:profile:read`
  (`apikeys.go:56`). Scopes must be grantable and **not** OIDC scopes (rejected).
  Returns the `raw_key` **once** (`apikeys.go:64`).
- `DELETE /v1.0/account/api-keys/:id` → revoke. `204`.

### OAuth clients  *(all stepUp-gated)*
- `GET /v1.0/account/oauth-clients` → `{ oauth_clients:[{ client_id, name, client_type, redirect_uris, allowed_scopes, audience, created_at, updated_at }] }`
  (secret hash never exposed, `oauth_clients.go:32`).
- `POST /v1.0/account/oauth-clients` → `{ name, client_type:public|confidential,
  redirect_uris (1–10, uri), allowed_scopes (1–20), audience? }`. Confidential
  clients get `client_secret` returned once.
- `PUT /v1.0/account/oauth-clients/:id` → update (same shape minus `client_type`).
- `DELETE /v1.0/account/oauth-clients/:id` → remove (only if owned by the user).
- `POST /v1.0/account/oauth-clients/:id/regenerate-secret` → `{ client_secret }` (once).

### Consents (connected apps)
- `GET /v1.0/account/consents` → `{ consents:[{ client_id, client_name, scopes, created_at, updated_at }] }`.
- `DELETE /v1.0/account/consents/:clientID` → revoke grant. `204`.

### Activity (audit log)
- `GET /v1.0/account/activity` → query `limit` (1–100, default 25), `cursor`.
  Returns `{ events:[{ event_type, ip, user_agent, metadata (allowlisted keys only),
  created_at }], next_cursor }` (`activity.go:36`). Metadata is filtered to
  `client_id, client_name, key_id, session_id, method, device_name`.

### KYC (identity verification)  *(all writes stepUp-gated)*
- `GET /v1.0/account/kyc` → current submission status (serialized `kyc.Submission`).
- `POST /v1.0/account/kyc` *(stepUp)* → `{ cpf (11 digits), legal_name, birth_date
  (YYYY-MM-DD), address:{ zip_code (8), street, number, complement?, district,
  city, state (2-letter) } }`. Submits for review (`kyc.go:66`).
- `POST /v1.0/account/kyc/documents` *(stepUp)* → `{ type: id_front|id_back|
  selfie_up|selfie_down|selfie_left|selfie_right, content_type }`. Returns a
  short-lived S3 presigned upload URL `{ document_id, upload_url, expires_in, max_bytes, content_type }`
  (API never sees the file; requires `KYCDocumentsBucket`).
- `POST /v1.0/account/kyc/documents/confirm` *(stepUp)* → `{ document_id (uuid), type }`.
  Marks the upload landed; submission stays `awaiting_files` until all required
  documents are present.

### Terms (in-app re-acceptance)
- `POST /v1.0/account/terms/accept` → `{ accept_tos, accept_privacy }`. Stamps the
  documents the user owes (recomputed server-side) and returns `{ terms_pending }`
  (`terms.go:123`). Needed because a token-refreshing session never revisits
  `/authorize`.

### MFA — `GET/POST/DELETE /v1.0/account/mfa/*`
- `GET /v1.0/account/mfa/totp` → `{ enabled:bool }`.
- `GET /v1.0/account/mfa/totp/setup` → `{ provisioning_uri }` (enrollment, open).
- `POST /v1.0/account/mfa/totp/confirm` → `{ code (6) }`; enables TOTP, returns
  `{ backup_codes }` (open — it *is* the enrollment path).
- `DELETE /v1.0/account/mfa/totp` *(stepUp)* → disable. `204`.
- `POST /v1.0/account/mfa/totp/backup-codes` *(stepUp)* → regenerate, returns `{ backup_codes }`.

### Passkeys — `GET/POST/DELETE /v1.0/account/mfa/passkeys/*`
- `GET /v1.0/account/mfa/passkeys` → `{ passkeys:[{ id, name, transports, aaguid, created_at, last_used_at }] }`.
- `POST /v1.0/account/mfa/passkeys/register/begin` → `{ name }`; returns
  `{ session_token, name, options }` WebAuthn registration challenge.
- `POST /v1.0/account/mfa/passkeys/register/complete` → query `session_token`, `name`;
  body = raw credential JSON. Creates credential, returns it. `201`.
- `DELETE /v1.0/account/mfa/passkeys/:id` *(stepUp)* → delete. `204`.

---

## Passkey auth — `POST /v1.0/auth/passkeys/*` (no `RequireAuth`)
- `POST /v1.0/auth/passkeys/authenticate/begin` → `{ session_token, options }`.
- `POST /v1.0/auth/passkeys/authenticate/complete` → query `session_token`; body =
  raw WebAuthn assertion. If the user also has TOTP enabled, returns an `mfa_token`
  (routes them to the MFA step); otherwise issues a session (`ctech_session` +
  `ctech_auth`) and returns user info (`passkey.go:200`).

---

## Internal — `GET /v1.0/internal/kyc/:user_id`  *(M2M; `RequireAuth` + `RequireInternalScope(internal:account:kyc)`)*
Service-to-service read used by **ctech-wallet** (`kyc.go:38`). Requires a
machine token (empty `sid`) carrying `internal:account:kyc`. Returns the **full,
unmasked** identity record `{ level, method, doc_status, cpf, legal_name,
birth_date, address }` (`kyc.go:158`). No human-facing scope governs this.

---

## Divergences from the prose docs (flagged, not "fixed")

1. **`accounts` vs `accounts-ui` client-id mismatch (real).** The account-management
   gate is `RequireClientID(cfg.SelfClientID)`, and `SELF_CLIENT_ID` defaults to
   `"accounts"` (`internal/config/config.go:156`). The frontend's OAuth client id is
   `CLIENT_ID = process.env.NEXT_PUBLIC_OAUTH_CLIENT_ID ?? 'accounts'`
   (`ui/src/lib/env.ts:6`) — also defaulting to `accounts`. But the CDK first-deploy
   instructions tell you to **seed an `accounts-ui` OAuth client**
   (`cdk/README.md:201`, `cdk/CLAUDE.md:184`), and the root `README.md:455`
   *claims* the default `"accounts"` "matches the `accounts-ui` seed" — which it
   does **not** (`accounts` ≠ `accounts-ui`). For a deploy that seeds `accounts-ui`
   and points the SPA at it (`NEXT_PUBLIC_OAUTH_CLIENT_ID=accounts-ui`), the token's
   `azp` becomes `accounts-ui` and **every `/v1.0/account/*` and `/v1.0/auth/step-up/*`
   call returns `403 Forbidden`** unless `SELF_CLIENT_ID` is *also* set to
   `accounts-ui`. The repo sets neither env var in CDK (`cdk/lib/compute-stack.ts`
   injects no `SELF_CLIENT_ID` / `NEXT_PUBLIC_OAUTH_CLIENT_ID`). The prior agent's
   hypothesis was mis-named (`OAUTH_CLIENT_ID=accounts-ui`) but the underlying
   three-way divergence (seeded `accounts-ui` vs defaults `accounts`) is genuine and
   should be reconciled in deployment (seed `accounts`, or set both env vars to
   `accounts-ui`).

2. **Discovery doc omits two grant types.** `GET /.well-known/openid-configuration`
   advertises `grant_types_supported: [authorization_code, refresh_token]`, but
   `POST /v1.0/token` also implements `api_key` (`token.go:99`) and
   `client_credentials` (`token.go:101`). `scopes_supported` is likewise limited to
   `openid/profile/email` while the catalog exposes many more (see `GET /v1.0/scopes`).

3. **`authorize` issues a code with `MFAVerified:false` hardcoded**
   (`authorize.go:193`) regardless of the session's actual MFA state — the field
   exists but is not propagated to the token. Hypothesized: MFA freshness is instead
   enforced downstream via `max_age`/`RequireRecentMFA`, so the field is currently
   inert. Flagged as a hypothesis, not a change.
