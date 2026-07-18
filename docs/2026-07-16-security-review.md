Code review(review · 5 findings)                                                                                                                                                                                                  
● internal/domain/session/service.go:136 [auth-bypass] — Refresh-token reuse detection never revokes the session or sibling tokens — it only returns an error.                                                                 
● internal/domain/user/repository.go:74 [race-condition] — Email uniqueness is enforced only by a check-then-create race, not atomically at the data layer.                                                                    
● internal/handler/oauth_clients.go:79 [authz-design] — Self-service OAuth client registration accepts an arbitrary free-form `aud` (Audience) list with no ownership/registry check.                                          
● internal/geo/geo.go:24 [input-validation] — Client IP is concatenated unvalidated into the outbound geo-lookup URL and persisted as session IPAddress.                                                                       
● internal/email/ses.go:76 [xss] — User-supplied FirstName is interpolated unescaped into transactional HTML emails.

● Findings, most severe first:

1. CRITICAL internal/domain/session/service.go:136 — refresh-token reuse detection (RotateClientToken) returns ErrTokenReuse on a stale-hash replay but never revokes anything. token.go:341-346 just logs + returns a "session   
   revoked" error message that's a lie — no Revoke/RevokeAll call exists on that path. Worse, UpdateRefreshTokenHash overwrites the old hash on rotation, so by the time reuse is detected there's no stored link from the stale hash
   back to the session to revoke. This is a real architectural gap, not a missed one-liner — contradicts CLAUDE.md's stated design directly.
2. MEDIUM internal/domain/user/repository.go:74 — email uniqueness is check-then-create, no ConditionExpression/transaction. Concurrent registrations with the same email can both succeed, creating two accounts sharing one     
   email — breaks the invariant login/reset/Google-linking assume.
3. LOW/MEDIUM internal/handler/oauth_clients.go:79 — self-service OAuth clients set arbitrary aud values, no registry check, embedded straight into access tokens. Exploitability depends on downstream services validating scope
   strictly, but it's an audience-confusion foot-gun.
4. LOW internal/geo/geo.go:24 — client IP (attacker-controlled via XFF when behind trusted proxy) goes unvalidated into the geo-lookup URL and into the stored session IPAddress shown in "active sessions"/audit logs.
5. LOW internal/email/ses.go:76 — FirstName interpolated unescaped into HTML emails via fmt.Sprintf. Self-XSS only today, becomes real the moment any email is sent to someone other than the account owner.

Verified clean: JWT (RS256-only, kid-scoped, alg-confusion not possible), PKCE/code single-use/redirect_uri exact-match, MFA gate (can't get a session without passing it), Argon2id passwords with timing-safe dummy path, rate  
limiting (RemoteIP() unless TRUSTED_PROXIES explicit), IDOR (all resources keyed by owning user's PK), token randomness (crypto/rand throughout), JWKS/keystore (private keys never serialized, SSM SecureString).

Priority fix is #1 — it defeats the whole point of reuse detection. Want me to patch it (needs a reverse-index or per-family session id surviving rotation so the reuse handler can resolve and revoke)?         ****