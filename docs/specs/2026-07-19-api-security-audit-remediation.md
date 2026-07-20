# API Security & Concurrency Audit — Remediation Design Spec

Date: 2026-07-19
Status: Draft (audit complete, findings enumerated, remediation proposed)
Scope: `ctech-account/api/` (Go OAuth2/OIDC IdP). Audit lens: the service runs as a
**distributed, autoscaling** deployment — many instances, each with its own in-process
state; shared state lives only in DynamoDB and an **optional** Valkey. Every finding is
tagged with its distributed risk.
Environments reviewed: `api/internal/**`, `api/cmd/**`.

This is an audit artifact. Findings are enumerated in §2; remediation is grouped into
prioritized phases in §3. No code was changed.

---

## 1. Themes (read these first)

### 1.1 "Optional Valkey" is a false premise — the root cause of most P0/P1 issues

`api/CLAUDE.md` and `cache/valkey.go` describe Valkey as *optional* (a no-op when
`VALKEY_URL` is unset). In reality the following are **only** in Valkey, with no
DynamoDB fallback:

- OAuth authorization codes (`oauth/code`, 60s TTL)
- MFA tokens + passkey WebAuthn challenges (5min TTL)
- email-verify / password-reset / google-state / accept-terms single-use tokens
- the scope catalog cache (5min TTL)
- the JWK rotation lock (`rotate_jwk_lock`)

When Valkey is absent or down:

- `/authorize` mints a code that can never be redeemed → interactive login is dead
  (token grant always returns `invalid_grant`). (`token.go:227` + `valkey.go:57` no-op)
- `/forgot-password`, `/resend-verification`, `/reset-password`, `/verify-email` break
  or return 500 (`auth.go:485-522`)
- MFA challenge, passkey login/register, social login, terms-accept return 503/500
  (`auth.go:176,236`, `social.go:49,88,204`)
- **rate limiting silently disables** → unlimited password guessing
  (`ratelimit.go:58`)
- yet `/healthz` still reports `pass` (`health.go:113-125`)

**Decision required:** treat Valkey as a **mandatory** production dependency. In
production, refuse to boot (or fail `/healthz`) when `VALKEY_URL` is unset or the ping
fails. Optionally back the few low-volume, must-survive-disconnect tokens
(ev/pr/gs/terms) with DynamoDB so account recovery and the legal-accept gate still work
during a Valkey blip — but the OAuth code + MFA/passkey challenges can remain in Valkey
once Valkey is mandatory. See §3 Phase 0.

### 1.2 Non-atomic read-modify-write is the dominant concurrency defect

Four independent sites do a read, decide, then an unconditional write with no
conditional expression: refresh-token rotation (`session`), TOTP verify/backup-code
removal (`totp`), WebAuthn challenge consume (`passkey`), and duplicate-email creation
(`user`). Each is a real race under concurrent instances. The fix pattern is uniform:
**conditional DynamoDB writes** (`UpdateItem`/`PutItem` with `ConditionExpression`,
gated on `database.IsConditionFailed`) or atomic Valkey ops (`GETDEL`, Lua `EVAL`).

### 1.3 Fail-open everywhere

Rate limiting, Valkey errors, and health all **fail open** (allow / report pass) instead
of failing closed. For an auth service this is the wrong default: a missing cache or a
transient error should *degrade to denial*, not to "everything allowed".

---

## 2. Findings (enumerated)

Severity: **CRIT** > **HIGH** > **MED** > **LOW**. `D` = distributed risk (yes/no).
ID groups: SEC (security), CON (concurrency), CAC (cache/distributed), BUG (behavior),
DRY (duplication/structure/constants).

### 2.1 CRITICAL

**SEC-001 · X-Forwarded-For IP spoofing — RESOLVED IN INFRASTRUCTURE (verified)**
`utils/ip.go:9-15`, `cmd/api/main.go:189-193`, `cdk/lib/compute-stack.ts:115,176,180,191`
~CRIT (as written) · **Status: N/A — handled at the nginx layer, not in app code**
The app code indeed never sets `EnableIPValidation: true` and `utils.IP` returns the
left-most XFF element. But the attack is **already neutralized upstream**:
- `cdk/lib/compute-stack.ts:191` calls `addRealipRefreshCommands`, which writes
  `/etc/nginx/conf.d/realip.conf` with `set_real_ip_from <VPC CIDR>`, `set_real_ip_from`
  for every CloudFront origin-facing prefix, `real_ip_header X-Forwarded-For`, and
  `real_ip_recursive on`. nginx therefore rewrites `$remote_addr` to the **real,
  unforgeable** viewer IP (walking XFF right-to-left and discarding only trusted hops).
- `compute-stack.ts:180` sets `proxy_set_header X-Forwarded-For $remote_addr;` —
  **overwrite, not append** (comment at :177-179 is explicit about why). The attacker's
  XFF is discarded; the Go app only ever receives the realip-resolved IP in XFF and
  `X-Real-IP`. `utils.IP` reading the left-most entry now returns the genuine client IP.
- `compute-stack.ts:169-170` also enforces `limit_req`/`limit_conn` per real IP at the
  edge, so the app-level Valkey limiter is defense-in-depth, not the only layer.
**Residual caveat (keep, do not implement as a fix):** the app's safety *depends on* nginx
being in front and overwriting XFF. A future deploy that bypasses nginx (direct ALB, or a
new edge that appends instead of overwrites) reintroduces the spoof. Cheap belt-and-braces
option (optional, not blocking): set `EnableIPValidation: true` + `TrustedProxies` for the
nginx peer so the app fails closed if XFF is ever spoofed. No code change required to
close SEC-001 as originally raised.

### 2.2 HIGH

**SEC-002 · Rate limiting is a no-op when Valkey is disabled**
`middleware/ratelimit.go:58-60`
HIGH · D=yes
`RateLimit` returns `c.Next()` immediately when `cfg.Cache == nil || !cfg.Cache.Enabled()`.
Per CLAUDE.md the cache is a no-op when `VALKEY_URL` unset, so any instance started
without it (dev, misconfig, Valkey init failure) has **zero** failed-login protection.
Fix: for the failed-login limiter, refuse to start without the cache, or fail closed
(reject auth) when the limiter can't enforce. (Ties to §1.1.)

**SEC-003 · Rate limiter fails open on any Valkey error**
`middleware/ratelimit.go:67-83`
HIGH · D=yes
Pre-check `if n, err := Count(...); err == nil && n >= Max` skips the reject branch
whenever `Count` errors; every `Incr` is called as `_, _ = ...`, swallowing errors. A
Valkey blip → limiter stops rejecting *and* stops counting → brute-force defense vanishes
with no signal. Fix: treat cache errors on the failed-login limiter as **deny**; surface
the error instead of discarding it.

**CON-004 · Refresh-token rotation is non-atomic → single-use defeated by concurrent rotation**
`domain/session/service.go:136-175`, `domain/session/repository.go:133-140`
HIGH · D=yes
`RotateClientToken` does `GetRefreshTokenByHash` → check → `UpdateRefreshTokenHash`
(unconditional `UpdateItem`, no condition on the prior hash). Two `/token` refreshes
presenting the **same** token (legitimate + stolen, or two tabs / two instances) both
pass the read, both succeed, each minting a valid new token. Reuse detection only fires
when the old hash is *gone*, so the exact-simultaneous race lets the token be redeemed
twice. Fix: make `UpdateRefreshTokenHash` conditional —
`SET refresh_token_hash = :new WHERE refresh_token_hash = :old`; on `ConditionalCheckFailed`
(CAUSE `database.IsConditionFailed`) return `ErrTokenReuse`.

**CAC-005 · Valkey is a hard SPOF; health hides it → LB routes to broken instances**
`cache/valkey.go:34-50,57,78,118` (no-op), `cmd/api/health.go:113-125`
HIGH · D=yes
When Valkey is disabled/down: OAuth codes, MFA tokens, passkey challenges, ev/pr/gs/terms
tokens, scope cache, rotation lock all break — but `checkValkey` returns `"warn"` and the
overall-status loop only fails on `dynamodb`/`cpu`/`mem` `"fail"`. So a functionally
broken instance still reports `pass`/200 and stays in rotation. Fix: when `!Enabled()` or
`Ping` errors, return `"fail"` for the valkey check and let it drive overall `fail` (503).
(Ties to §1.1.)

**SEC-006 · TOTP secret stored plaintext at rest**
`domain/mfa/totp/model.go:8`, `domain/mfa/totp/service.go:54`
HIGH · D=no
`TOTPSecret.Secret` is a bare base32 `string`, persisted via unconditional `PutItem`.
No envelope/KMS encryption exists anywhere in `internal/` (grep encrypt/cipher/kms →
nothing). Anyone with `account_mfa` table read access can derive TOTP codes and, with the
password, fully bypass MFA. Fix: envelope-encrypt the secret (KMS data key or a
`crypto` helper) before `MarshalMap`; decrypt in `Get`; store only ciphertext.

**SEC-007 · ToS/Privacy acceptance gate bypassable via `created` flag**
`handler/social.go:157`, `domain/user/...` (`FindOrCreateByGoogle`)
HIGH · D=no
`googleCallback` gates the accept-terms interstitial on the `created` return value instead
of the actual pending-terms state. Wait out the 10-min terms token (or hit the
legacy-bind branch where `created=false`) and the code skips the gate, minting a full SSO
session even though `TOSVersion`/`PrivacyVersion` are still `""` and
`legal.PendingFor("","")` is pending. Fix: gate on terms-pending state —
`if legal.PendingFor(u.TOSVersion, u.PrivacyVersion).Any() { redirectToAcceptTerms }` —
for both the new-account and legacy-bind branches.

**CON-008 · Duplicate-email accounts via non-atomic check-then-create**
`domain/user/repository.go:68`, `domain/user/service.go:41-47,203-245`
HIGH · D=yes
`Register` and `FindOrCreateByGoogle` do `GetByEmail` then `Create` with no
`ConditionExpression`. Because PK is a fresh UUID per create, two concurrent registrations
(or two Google sign-ups with the same email+sub) both see "not found", both `PutItem`,
yielding two `USER_*` items sharing one email in the `email-index` GSI. Subsequent
`GetByEmail` returns an arbitrary one; the second account is silently shadowed.
Fix: enforce email uniqueness with a conditional write — a dedicated email-marker item
(`PutItem` with `attribute_not_exists(pk)`) or a GSI condition — returning `ErrEmailConflict`
on failure; abort the `Create`.

**BUG-009 · Authorization-code grant silently breaks when Valkey is disabled**
`handler/token.go:227`, `handler/authorize.go:198`, `cache/valkey.go:57-59`
HIGH · D=no
The code store is a no-op when Valkey disabled: `Store()` returns nil, `GetAndDelete()`
returns `ErrNotFound`. `/authorize` "succeeds" (issues an unpersisted code) while `/token`
always returns `invalid_grant`. The whole interactive login flow depends on Valkey yet
fails open with no signal. (Subset of §1.1; listed separately because the failure mode is
silent and specifically breaks the code grant.) Fix: fail authoritatively in `/authorize`
when the code store cannot guarantee persistence, or back codes with DynamoDB.

### 2.3 MEDIUM

**CAC-010 · INCR+EXPIRE non-atomic → counter can persist with no TTL (lockout)**
`cache/valkey.go:241-258`, `middleware/ratelimit.go:72,80`
HIGH→MED · D=yes
`Incr` does `INCR key` (no TTL) then a separate `EXPIRE key NX`; the error is swallowed by
`_, _ =`. If the process crashes between the two ops, or `EXPIRE` blips, the key has **no
expiry**. On the next `Incr`, `EXPIRE NX` *does* self-heal (NX sets when no TTL exists) —
**but only if another request arrives**. A client who hits `Max` failures and then stops
stays blocked **permanently** until manual `DEL`. The in-memory path sets TTL atomically
and is correct, so unit tests pass while prod breaks. Fix: atomic increment+expire via a
single Lua `EVAL` (`incr`; if `ttl==-1` then `expire`), or `SET key 0 NX EX ttl` before
`INCR`. Do not swallow the `EXPIRE` error.

**SEC-011 · id_token accepted as an access token (no `token_use`/`typ`)**
`crypto/jwt.go:120-128,147-172`, `middleware/auth.go:71`
MED · D=no
`Verify` checks alg/aud/iss but never a `typ`/`token_use` claim. For the first-party
client, `client_id == SelfClientID == cfg.Audience`, so an id_token (`aud=[client_id]=[selfAudience]`,
`iss=cfg.BaseURL`) passes `Verify` **and** `RequireClientID`. It carries no `sid`/`scope`/
`last_mfa_at`, but account endpoints gated only by `RequireAuth+RequireClientID` treat it
as a bearer for that user. Classic OIDC id_token-replay. Fix: add `token_use:"access"` when
signing access tokens; reject tokens lacking it in `Verify`.

**CAC-012 · JWK reload interval (1h) exceeds token lifetime → intermittent 401s after rotation**
`keystore/rotator.go:15,36-60`
MED · D=yes
`CheckInterval = 1h` is both reload cadence and the documented propagation ceiling. On
rotation, instance A writes new KID to SSM and starts signing; instance B keeps stale
`active=K1` until its next tick (up to 1h). A K2-signed access token (valid ≤15min)
presented to B before reload hits `keyForKID(K2)==nil` → rejected. Valid tokens are
intermittently rejected cluster-wide for up to an hour after each 90-day rotation.
Fix: poll SSM on a short interval (1–5 min, well below min token lifetime); optionally
broadcast an immediate reload on successful rotation.

**CON-013 · Rate limiter Count-then-Incr TOCTOU over-admits at the limit boundary**
`middleware/ratelimit.go:67-83`
MED · D=yes
Reads `Count(key)` (GET) then `Incr` (INCR) — two round trips. Under concurrent requests
(across instances, since all share Valkey) several observe `n==Max-1`, all pass `>=Max`,
then all increment → limit exceeded by up to the number of in-flight requests. Fix:
`Incr` first, decide on its return value (`n, _ := Incr(key, window); if n > Max { reject }`).
Fold into the same Lua script as §CAC-010.

**SEC-014 · WebAuthn challenge not bound to user/session**
`domain/mfa/passkey/service.go:68,145,231,299-307`
MED · D=yes
The challenge is cached under `webauthn_session:<hash>` with no association to the
requesting `userID`; `consumeSession` retrieves purely by token. `registerComplete`/
`authenticateComplete`/`passkeyComplete` pass the *caller's* `GetUserID()` into `Finish*`.
A leaked token (proxy log, Referer, history) can be replayed by any other authenticated
principal. (go-webauthn binds the credential to the caller's account, so impact is
fixation/relay, not cross-account credential theft — but it removes scoping/revocation.)
Fix: store `userID` in the cached `SessionData` (or namespace the key
`webauthn_session:<uid>:<hash>`) and assert it equals the caller before `Finish*`.

**CON-015 · WebAuthn `consumeSession` Get+Delete non-atomic → double consume**
`domain/mfa/passkey/service.go:299-307`
MED · D=yes
`cache.Get` then `cache.Delete` are separate calls. Two concurrent `registerComplete`/
`authenticateComplete` using the same `session_token` both `Get` before either `Delete` →
both pass, yielding a duplicate passkey write (repo `PutItem` has no condition) or a second
assertion off one challenge. Fix: single atomic consume (`cache.GetDel`).

**CON-016 · TOTP `Verify` has no condition → concurrent confirm clobbers backup codes**
`domain/mfa/totp/service.go:93-98`
MED · D=yes
`Verify` reads `secret.Verified`, then `UpdateItem` sets `verified=true`+`backup_codes`
with no `ConditionExpression`. Two concurrent `/totp/confirm` (same 30s code window) both
pass the in-memory `Verified==false` check; the second write overwrites the first's freshly
generated `backup_codes` → the first caller gets useless codes. `RegenerateBackupCodes`
(line 177) has the same shape. Fix: conditional update keyed on an expected value (or a
monotonic version attribute / transactional check) so only the first confirm commits.

**CON-017 · Backup-code single-use is TOCTOU (double-spend)**
`domain/mfa/totp/service.go:134-149`
MED · D=yes
`validateBackupCode` reads `secret.BackupCodes`, finds the match via `VerifyPassword`, then
`UpdateItem` removing index `i` with no condition that the code is still present. Two
concurrent logins presenting the same backup code (within the 5-min Valkey rate-limit
window, possibly across instances) both read it, both verify, both issue removal → both
succeed and both mint sessions. Fix: conditional removal keyed on the current
`backup_codes` list still containing the matched hash (optimistic concurrency / version).

**SEC-018 · KYC `ConfirmDocument` type not bound to the presigned `documentID`**
`domain/kyc/service.go:163-198`
MED · D=no
`PresignDocument` pins the S3 key but never persists the `(documentID → type)` mapping.
`ConfirmDocument` takes `Type`+`DocumentID` from the client, rebuilds the key, checks only
`Size>0`, and records whatever `Type` the client supplied. A user can presign `id_front`,
upload a selfie clip, then confirm as `type=selfie_up`, defeating the document/liveness
check. Fix: persist the presigned intent (`documentID`→`type`,`content_type`) server-side;
reject `ConfirmDocument` when `req.Type`/`content_type` mismatch the stored intent.

**CAC-019 · Scope catalog cache never invalidated on write (5-min stale window)**
`internal/scopes/service.go:33-49`, `internal/scopes/repository.go:51-62`
MED · D=yes
`Catalog()` serves from global key `scope_catalog` (5min TTL). `PutService()` writes
DynamoDB but never deletes the cache. After seeding a new scope/service (incl. a new
`Audience`): `ValidateGrantable` rejects it, `GET /v1.0/scopes` hides it, `AudiencesFor`
omits the audience so tokens for that service are rejected — for up to 5 min, every
instance. Fix: `PutService()` issues `cache.Delete(ctx, CatalogCacheKey)` after the write
(no-op when disabled, so safe). Optionally version the key.

**BUG-020 · `ctech_rt` refresh cookie shared across all clients on the cookie domain**
`handler/token.go:25,288,394`
MED · D=no
`refreshTokenCookieName` is a single `"ctech_rt"` for every client. If two public SPA
clients share the cookie domain, the latest code exchange overwrites the cookie; the other
client's silent refresh presents the wrong token with its own `client_id` →
`ErrClientMismatch` (`session/service.go:145`) — fails safe but breaks multi-client silent
refresh. Fix: scope the cookie per client (`ctech_rt_<clientID>`), or store a
client_id→token index.

**SEC-021 · Register email-enumeration timing oracle**
`handler/auth.go:81-105`, `domain/user/service.go:38-73`
MED · D=no
New-registration runs `crypto.HashPassword` (Argon2) before `Create`; the conflict path
returns `ErrEmailConflict` (existing != nil) with no hash and no `Create`. So an
already-registered email is materially faster → a timing side-channel that defeats the
202-always anti-enumeration design. Fix: always burn Argon2 (hash the real or a dummy
password) before/independent of the existence check so both branches cost the same.

**BUG-022 · `/forgot-password` returns 200 `{"sent":true}` when recovery is impossible**
`handler/auth.go:485-511`
MED · D=no
When Valkey disabled, `forgot-password` still returns 200 `{"sent":true}` while sending no
email and storing no token (`resetPassword` then 500s). The user is told a reset email
was sent that never arrives. (Response is uniform, so not an enumeration leak — a
reliability defect.) Fix: surface recovery unavailability, or back recovery tokens with
DynamoDB (ties to §1.1).

**SEC-023 · Disabled-account response inconsistent across login factors (oracle)**
`handler/passkey.go:227` vs `handler/auth.go:154`, `handler/stepup.go:77`
MED · D=no
Password login returns `InvalidCredentials` on a disabled account (deliberate
anti-enumeration); step-up mirrors it; passkey login returns `AccountDisabled`, leaking
that the account exists and is disabled. Same logical state, two responses. Fix: pick one
policy — passkey should mirror password (`InvalidCredentials`).

**SEC-024 · Presigned S3 PUT has no object-size cap**
`internal/storage/s3.go:40-50`
MED · D=no
`PresignPutObject` sets only `Bucket/Key/ContentType`; no `ContentLength` upper bound. A
presigned-URL holder can PUT an arbitrarily large object; size is only checked afterwards
via `HeadObject`. Fix: add a `ContentLength` constraint to the presigned PUT (S3 rejects on
exceed); enforce a hard max via bucket policy/handler.

**CAC-025 · JWK rotation lock not environment-namespaced**
`keystore/rotator.go:17`
LOW→MED · D=yes
`LockKey = "rotate_jwk_lock"` is global while SSM paths are env-namespaced
(`/ctech-account/%s/jwk/...`). A single Valkey shared across envs lets one env's rotation
block another's. Fix: `fmt.Sprintf("rotate_jwk_lock:%s", env)`.

**CAC-026 · JWK rotation lock acquired but never explicitly released**
`keystore/rotator.go:43,19`
LOW · D=yes
`TryLock` = `SetNX` with `LockTTL=1h`, no `Unlock`. Safe today only because rotations are
90d apart and `CheckInterval==LockTTL==1h`. Fragile if cadence changes or a rotation is
forced (key compromise). Fix: release explicitly on success (TTL as crash-net), or use a
fencing token / shorter TTL with renewal.

**BUG-027 · `SignAccessToken` ignores `ACCESS_TOKEN_TTL`; signed `exp` ≠ advertised `expires_in`**
`crypto/jwt.go:67-92`, `handler/token.go:43,137,191,298,402`
MED · D=no
`SignAccessToken` hardcodes `now.Add(15 * time.Minute)`; `id_token` hardcodes 1h. Meanwhile
`handler/token.go` defines `accessTokenTTLSeconds=900` and hardcodes `"expires_in": 900`
at lines 298/402. So (a) the `ACCESS_TOKEN_TTL` config is dead/unused, and (b) when it is
set ≠ 900 the signed `exp` won't match the advertised `expires_in`. Fix: thread the config
TTL into `SignAccessToken`/`SignIDToken` and use `accessTokenTTLSeconds` (single source)
in both the signature and the response.

**DRY-028 · Valkey key prefixes are inline magic strings**
`handler/auth.go:192,243,294,336,434,457,501,528`, `handler/social.go:62,94`
MED · D=yes
`mfa_token:` (4 sites), `ev:` (2), `pr:` (2), `gs:` (2) hardcoded, contradicting the
code's own `oauth/code/repository.go:15` `keyPrefix` constant. Fix: named prefix constants
in `helpers.go`.

**DRY-029 · Token-mint-and-store repeated for ev/pr/gs (not for mfa/terms)**
`handler/auth.go:180-201,426-441,485-511` (+ `social.go:58-62`)
MED · D=yes
The `GenerateMFAToken()`+`cache.Set(prefix+hash,payload,ttl)` / `GetDel` pattern is
reimplemented inline for email-verify, password-reset, google-state; mfa/terms already
have shared helpers. Fix: extract `mintCacheToken`/`consumeCacheToken` in `helpers.go`.

**DRY-030 · Audience built two different ways across the four grants**
`handler/token.go:127,178` vs `265,388`
MED · D=no
`apiKeyExchange`/`clientCredentials` build `append([]string{cfg.Audience}, serviceAudiences...)`
inline; `authorizationCode`/`refreshToken` use the `accessTokenAudience` helper. The two
paths can compute different audiences for the same scopes. Fix: route all four grants
through one `accessTokenAudience` helper.

**DRY-031 · `oauth/code.Repository` is a concrete struct injected by handlers (violates CLAUDE.md)**
`domain/oauth/code/repository.go:17`, `handler/authorize.go:27`, `handler/token.go:50`
MED · D=no
CLAUDE.md mandates "every domain package exports a `Repository` interface" and "services
take repository interfaces — never concrete types." This is the sole exception (all other
domain packages export an interface). Fix: export `Repository` interface +
`NewRepository(*cache.Client) Repository`; type handler fields as `authcode.Repository`.

**DRY-032 · Session-issue + login-completion blocks duplicated across handlers**
`handler/auth.go:204-222,257-273,355-372`, `handler/passkey.go:244-250`,
`handler/social.go:332-345`, `handler/terms.go:231-238`, `handler/mfa.go` equivalents
MED · D=yes
The `sessionSvc.Create`+`enrichSessionAsync`+`setSessionCookies`+`recordAudit(LoginSuccess)`+
response map is copy-pasted ~6 sites; the user-session JSON response map is duplicated 4
sites. Fix: `createUserSession(c, cfg, sessionSvc, auditSvc, u, amr, deviceName, auditMethod)`

+ `userSessionResponse(u, sess)` helpers in `helpers.go`.

**DRY-033 · Inconsistent error-handling architecture; passkey copy-pastes domain→problem mapping**
`handler/passkey.go:212-218,232`, `handler/stepup.go:123-129`, `handler/kyc.go:185-221`,
`handler/oauth_clients.go:46-62`
MED · D=yes
KYC and OAuth-clients have centralized `sendKYCError`/`sendClientError` mappers; passkey/
TOTP auth-flow errors are copy-pasted (identical `ErrSessionExpired→InvalidToken`,
`ErrInvalidResponse→InvalidRequest`, default `Unauthorized` switch in 3 files). Fix: a
shared `passkeyError(c, err)` helper closing the gap.

**DRY-034 · Constant literals not named (response_type, PKCE, grant_type reuse, Bearer)**
`handler/authorize.go:99,102`, `handler/wellknown.go:33,39,40`, `handler/social.go:68,286`,
`handler/token.go:29-39,302-304,405-407`
MED · D=yes
`"code"` (response_type), `"S256"` (PKCE), `"authorization_code"` (hardcoded in
social.go:286 + wellknown.go:40 despite `token.go` constants), `"Bearer"` (×4 in token.go)
are inline. Fix: `responseTypeCode`, `pkceMethodS256`, reuse `grantAuthorizationCode`/
`grantRefreshToken`, `bearerTokenType`.

**DRY-035 · `authorize.go:125` inlines cookie-clear, bypassing `clearAuthCookie` (and uses wrong domain var)**
`handler/authorize.go:125`
MED · D=no
Inline `c.Cookie(&fiber.Cookie{...Domain: h.cookieDomain})` instead of
`clearAuthCookie(c, h.cfg, sessionCookieName)`; passes `h.cookieDomain` directly instead of
`effectiveCookieDomain`, diverging from the helper on localhost/127.x. Fix: use the helper.

**SEC-036 · `mfa_token` not bound to request IP/UA**
`handler/auth.go:243,258,35`
LOW · D=no
`issueMFAToken` stores IP/UA for audit only; `mfaChallenge`/`mfaPasskeyComplete` never
compare incoming IP/UA to stored values. An intercepted `mfa_token` is origin-agnostic.
Bounded (still needs the TOTP code / passkey assertion). Fix: flag/reject use from a
different IP/UA, or shorten the 5-min TTL.

**CON-037 · Google `gs:` state consumed via Get+Delete, not atomic GetDel**
`handler/social.go:94-97`
LOW · D=yes
`googleCallback` reads `gs:` state with `cache.Get` then `cache.Delete` instead of
`GetDel`, inconsistent with every other single-use token. Fix: `cache.GetDel`.

**SEC-038 · Rejected KYC documents never deleted from S3 (PII persists)**
`domain/kyc/repository.go:201-220`
LOW · D=no
`MarkRejected` only `REMOVE kyc_documents`; the S3 objects at `kyc/{userID}/{documentID}`
(selfie biometrics + IDs) remain indefinitely. Fix: delete S3 objects (best-effort, async)
on reject, or enforce an S3 lifecycle/expiry on `kyc/` prefixes.

**SEC-039 · HTML injection via `firstName` in transactional emails**
`internal/email/ses.go:77-90`
LOW · D=no
`firstName` is interpolated raw into HTML email bodies. A crafted `firstName` renders
verbatim in a ctech email (self-directed, limited, but aids phishing). Fix: `html.EscapeString`
or `html/template`.

**BUG-040 · `/healthz` `cpuPercent` computes boot-time average, not current utilization**
`cmd/api/health.go:127-134`
LOW · D=no
Reads `/proc/stat` once; without a second sample it's the since-boot average, misinforming
autoscaling. Fix: two samples ~100–250ms apart, divide deltas.

**BUG-041 · `roundOne(-1)` mangles the "unavailable" sentinel → `-0.9`**
`cmd/api/health.go:219-221`
LOW · D=no
Cosmetic but misleading. Fix: special-case `<0` before rounding.

**SEC-042 · `geo.Lookup` ignores context, no IP validation, ships IPs to a third party**
`internal/geo/geo.go:20,24-25`
LOW · D=no
Concatenates `ip` into a public API URL with no validation and no `context` (only a 3s
`Timeout`). Fix: validate `ip`; pass a `context`; consider a self-hosted geo provider.

**SEC-043 · `apierror.NewFromFiber` surfaces `fiber.Error.Message` to the client**
`internal/apierror/problem.go:134-146`
LOW · D=no
For body-parse/route errors, `fe.Error()` can include a snippet of the offending request
input. Fix: map known fiber codes to safe static messages.

**SEC-044 · RSA-2048 signing keys (KID only 64 bits)**
`internal/keystore/key.go:16,41`
LOW · D=no
RSA-2048 is below the current 3072-bit recommendation for long-lived OIDC signing keys;
KID truncates SHA-256 to 16 hex (unnecessary given full hash is computed). Fix: generate
RSA-3072 (or Ed25519); use full/128-bit KID. Low urgency, cheap hardening.

**DRY-045 · `rotatekeys` uses raw AWS SDK loader, not `awsconfig.Load`**
`cmd/rotatekeys/main.go:30`
LOW · D=no
May authenticate against a different region/role than the running service, writing keys to
the wrong SSM path/env. Fix: use `awsconfig.Load(ctx, region)`.

**DRY-046 · Operator CLI table-prefix/DB/S3 init boilerplate duplicated**
`cmd/seedscopes/main.go:26-49`, `cmd/kyc/main.go:39-63`, `cmd/createclient/main.go:60-77`
LOW · D=no
Identical `TABLE_PREFIX||ENVIRONMENT`, `TrimSuffix`, `database.New`, `storage.NewS3`.
Fix: a small shared `internal/cli` helper (`LoadTablePrefix`, `MustDB`, `MustS3`).

**DRY-047 · Misc small duplications**
`handler/auth.go:390-393,411-413` (logout/endSession cookie-clear dup →
`clearSessionAndRefreshCookies`), `handler/authorize.go:295-301` (deny branch inlines
`redirectError`), `handler/token.go:302-304,405-407` (public-vs-confidential refresh
inclusion dup → `includeRefreshToken`), `handler/auth.go:161`/`stepup.go:146`/`passkey.go:231`
(`totp.IsSetup` check dup → `userHasTOTP`), `handler/auth.go:319`/`stepup.go:113`/`passkey.go:201`
(WebAuthn param-guard dup → `requireWebAuthnParams`), `handler/auth.go:301`/`stepup.go:91`/`passkey.go:185`
(`ErrCacheRequired→ServiceUnavailable` dup). All LOW · D=yes. Fix: the named helpers.

**VERIFIED SAFE (audited and found correct — no action):**

- OAuth `redirect_uri` validated exact-match at `/authorize` and `/token`; social `continue`
  URL guarded by `isAllowedContinueURL` (no open redirect).
- PKCE required for public clients, enforced at `/token`, S256-only, no confidential→public
  downgrade.
- OAuth code single-use via atomic `GETDEL`.
- Scope over-grant prevented (token returns code-authorized scopes; refresh clamps via
  `FilterScopes`).
- Refresh rotation vs replace used correctly (`IssueClientToken` on code exchange,
  `RotateClientToken` on refresh); SSO cookie not rotated on refresh.
- KYC CPF uniqueness **is** atomic (`TransactWriteItems` conditional `Put` → `ErrCPFConflict`).
- MFA gate cannot be bypassed when TOTP/passkey enabled (only the terms gate is, see SEC-007).
- ev/pr/gs/terms/mfa_token consume is single-use via `GetDel` when Valkey enabled.
- account-client lock (`azp == SelfClientID`) correctly enforced for `/account/*` and `/step-up/*`.
- `crypto.JWTService.Reload` is guarded by `sync.RWMutex` (the rotator goroutine is safe);
  `password.go` Argon2 + dummy-hash side-channel mitigation is sound; `token.go` entropy OK.
- **SEC-001 (XFF spoofing) — resolved in infrastructure.** `cdk/lib/compute-stack.ts`
  rewrites `$remote_addr` via the realip module (VPC CIDR + CloudFront origin prefixes,
  `real_ip_recursive on`) and **overwrites** `X-Forwarded-For $remote_addr;` before proxying
  to the app, so the app only ever receives the unforgeable real client IP. App code needs
  no change; see the SEC-001 entry for the residual (nginx-must-be-in-front) caveat.

---

## 3. Remediation phases

### Phase 0 — Make the deployment honest (blocks everything else)

1. ~~**SEC-001** XFF IP spoofing~~ — **RESOLVED IN INFRA** (`cdk/lib/compute-stack.ts`:
   realip rewrite + `proxy_set_header X-Forwarded-For $remote_addr;` overwrite). No app
   code change closes it. Optional belt-and-braces (non-blocking): set `EnableIPValidation:
   true` + `TrustedProxies` for the nginx peer so the app fails closed if XFF is ever
   spoofed. Not in scope as a fix.
2. **§1.1 / CAC-005 / BUG-009** Treat Valkey as mandatory in production: fail boot or fail
   `/healthz` (`fail`/503) when `VALKEY_URL` unset or ping fails; fix `health.go` valkey
   check; remove the "optional" framing from CLAUDE.md/README. (HIGH)
3. **SEC-002 / SEC-003 / CAC-010 / CON-013** Rate limiter fail-closed + atomic counter:
   Lua `EVAL` for incr+expire, `Incr`-first admission, deny on cache error for the
   failed-login limiter. (HIGH)

### Phase 1 — Close the concurrency/security races

4. **CON-004** Conditional refresh-token rotation (`UpdateRefreshTokenHash ... WHERE
   refresh_token_hash = :old`; `ErrTokenReuse` on `IsConditionFailed`). (HIGH)
5. **CON-008** Conditional email uniqueness (email-marker item / GSI condition →
   `ErrEmailConflict`). (HIGH)
6. **SEC-006** Envelope-encrypt TOTP secret at rest. (HIGH)
7. **SEC-007** Gate terms-accept on pending-terms state, not `created`. (HIGH)
8. **SEC-011** Add `token_use:"access"`; reject absent in `Verify`. (MED)
9. **SEC-014 / CON-015** Bind WebAuthn challenge to userID; atomic `GetDel` consume.
   (MED)
10. **CON-016 / CON-017** Conditional TOTP verify + backup-code removal (version/expected
    value). (MED)
11. **SEC-018** Persist + validate presigned KYC document intent. (MED)
12. **CAC-019** Invalidate scope catalog cache on `PutService`. (MED)
13. **BUG-020** Per-client refresh cookie name. (MED)
14. **SEC-021** Constant-time register (always burn Argon2). (MED)
15. **SEC-023** Unify disabled-account response (`InvalidCredentials`). (MED)
16. **SEC-024** Presigned PUT `ContentLength` cap. (MED)
17. **BUG-027** Thread `ACCESS_TOKEN_TTL` into signing; single TTL source. (MED)

### Phase 2 — Distributed correctness & hardening

18. **CAC-012** Short SSM poll interval (1–5 min) for JWK reload. (MED)
19. **CAC-025 / CAC-026** Env-namespaced rotation lock; explicit release. (LOW–MED)
20. **BUG-022** Truthful `/forgot-password` (or DynamoDB-backed recovery). (MED, ties to §1.1)
21. **SEC-036** Bind `mfa_token` to IP/UA or shorten TTL. (LOW)
22. **CON-037** `GetDel` for google `gs:` state. (LOW)
23. **SEC-038** Delete rejected KYC S3 objects / lifecycle. (LOW)
24. **SEC-039 / SEC-042 / SEC-043** Escape `firstName`; geo `context`+validation; safe
    `NewFromFiber` messages. (LOW)
25. **BUG-040 / BUG-041** Correct `cpuPercent` (two samples); fix `roundOne` sentinel. (LOW)
26. **SEC-044** RSA-3072 / fuller KID. (LOW)

### Phase 3 — DRY / structure / constants (can land incrementally, no behavior change)

27. **DRY-028…DRY-047** Named cache-key/constant prefixes; `mintCacheToken`/`consumeCacheToken`;
    single `accessTokenAudience`; `createUserSession`/`userSessionResponse`; `passkeyError`
    mapper; `clearSessionAndRefreshCookies`/`includeRefreshToken`/`userHasTOTP`/
    `requireWebAuthnParams`; export `oauth/code.Repository` interface; `clearAuthCookie`
    at authorize:125; `internal/cli` helper for operator CLIs; `awsconfig.Load` in
    `rotatekeys`. (LOW–MED)

---

## 4. Distributed-systems guardrails (apply to every future change)

- **No per-instance mutable auth state.** Anything that affects correctness across
  requests (codes, tokens, locks, caches) lives in DynamoDB or shared Valkey — never in a
  package-level `var` or in-memory map on a single instance (the in-memory cache exists
  for tests only).
- **Every read-decide-write on shared data is conditional.** Prefer DynamoDB
  `ConditionExpression` (`IsConditionFailed`) or atomic Valkey ops (`GETDEL`, Lua `EVAL`).
- **Fail closed, not open.** Cache/limiter/health errors deny or report degraded; they
  never silently allow.
- **Valkey is mandatory in production;** back only the tokens that must survive a Valkey
  blip with DynamoDB (recovery + legal-gate).
- **Keys/caches are env-namespaced** so a shared Valkey across stages never cross-talks.
- **Reloadable config (keys, scopes) propagates well below the min token lifetime.**

---

## 5. Testing

- **Unit (race):** `go test -race ./internal/...` covering concurrent `RotateClientToken`
  (same token twice → second `ErrTokenReuse`), concurrent `Register` (→ `ErrEmailConflict`),
  TOTP concurrent `Verify` (→ only first commits backup codes), WebAuthn double `GetDel`,
  KYC concurrent CPF (already covered).
- **Integration:** XFF spoofing rejected (`EnableIPValidation`); rate limiter denies at
  boundary under concurrency; `/authorize` fails when code store unavailable; id_token
  rejected as bearer; terms gate enforced regardless of `created`; multi-client refresh
  cookies don't clobber; scope catalog reflects a just-seeded scope.
- **Health:** valkey-down instance returns 503.
- **Regression:** each fixed finding gets a focused test; add the missing tests noted in
  the handler/domain `*_test.go` files.

## 6. Cross-project impact

- **cdk**: IAM for KMS (TOTP envelope) if chosen; health-check alarm on 503; Valkey treated
  as required dependency (no longer optional in deploy docs).
- **ui**: terms-gate still enforced server-side (no client change needed, but the
  `created`-based assumption in the Google interstitial flow should be dropped — gate on
  `terms_pending` from `/account/profile`).
- **ctech-dfe / ctech-wallet**: JWK reload interval shortens → they pick up new KIDs faster
  (good); `token_use` claim added → downstream verifiers must accept it (additive; existing
  RS256 verification unaffected). Scope catalog invalidation fixes delayed
  `internal:wallet:*` grants after seeding.
- **ctech-cdk / ctech-go-common**: if a shared atomic-counter / conditional-write helper is
  introduced, consider upstreaming it.

## 7. Rollout order

1. Phase 0 (deployment honesty) — unblocks safe operation; highest risk-reduction per line.
2. Phase 1 (races) — security-critical, each independently shippable behind its test.
3. Phase 2 (distributed hardening) — polish + resilience.
4. Phase 3 (DRY) — pure refactor, ship as separated small PRs.

No client-facing contract changes except the additive `token_use` claim (§SEC-011) and
per-client refresh cookie rename (§BUG-020, cookie-only, transparent to the SPA).
