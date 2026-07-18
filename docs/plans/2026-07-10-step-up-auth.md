# Step-up Authentication Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Sensitive operations demand recent MFA proof, enforced statelessly via new `auth_time`/`amr`/`last_mfa_at` JWT claims and a `RequireRecentMFA` middleware, with a `POST /v1.0/auth/step-up` challenge endpoint.

**Architecture:** Session stores `AuthTime`/`AMR`/`LastMFAAt`; access tokens carry them as claims; middleware checks claims only (no DB read per request). After a step-up challenge the UI silent-refreshes to obtain a token with fresh claims. Users with no MFA enrolled get `403 mfa-enrollment-required` from the challenge endpoint. Spec: `docs/specs/2026-07-10-account-hardening-design.md` §A. Depends on the audit plan (`2026-07-10-audit-log.md`) being implemented first (records `stepup.*` events).

**Tech Stack:** Go 1.26, Fiber v3, DynamoDB, Valkey, Next.js 16.

## Global Constraints

- All HTTP errors via `*apierror.Problem` + `.Send(c)`; new Problem types follow `newProblem(slug, title, status, detail, instance)` in `internal/apierror`.
- AMR values are constants: `pwd`, `otp`, `webauthn`, `google` (package `session`).
- Step-up freshness window: `middleware.StepUpMaxAge = 5 * time.Minute` (named constant).
- Step-up challenge rate limit: reuse `middleware.RateLimit` — prefix `"stepup"`, `FailedLoginMax`/`FailedLoginWindow`, keyed by user ID, `CountOnlyFailures: true`.
- `api_key` grant tokens carry no `amr`/`auth_time` → can never pass step-up.
- All request structs validated via `validate.Struct(req)`.
- JWT signing is a **critical area** (CLAUDE.md) — token claim changes must keep existing claims byte-compatible (`sub`, `sid`, `scope`, `iss`, `aud`, `azp`, `iat`, `exp` unchanged); ctech-dfe only reads `sub` today, so additive claims are backward compatible.

---

### Task 1: Session model + service — AuthTime, AMR, LastMFAAt

**Files:**
- Modify: `internal/domain/session/model.go` (Session struct)
- Modify: `internal/domain/session/service.go` (`Create` signature; new `RecordMFA`)
- Modify: `internal/domain/session/repository.go` (interface + dynamo impl: `UpdateMFA`)
- Modify: `internal/domain/session/service_test.go`
- Modify: all `Create` call sites: `internal/handler/auth.go`, `internal/handler/passkey.go`, `internal/handler/social.go` (find with `rg -n "sessionSvc.Create|\.Create\(c.Context" internal/handler/`)
- Modify: in-memory session repo in `internal/handler/testhelpers_test.go` (add `UpdateMFA`)

**Interfaces:**
- Produces:
  - `Session` fields: `AuthTime int64` (`auth_time`), `AMR []string` (`amr,omitempty,stringset`... use plain `dynamodbav:"amr,omitempty"` string slice), `LastMFAAt int64` (`last_mfa_at,omitempty`)
  - AMR constants: `session.AMRPassword = "pwd"`, `session.AMRTOTP = "otp"`, `session.AMRWebAuthn = "webauthn"`, `session.AMRGoogle = "google"`
  - `Create(ctx, userID, deviceName, ip, userAgent string, amr []string) (*Session, string, error)` — sets `AuthTime = now`; if `amr` contains an MFA method (`otp`/`webauthn`), also sets `LastMFAAt = now`
  - `RecordMFA(ctx, userID, sessionID, method string) error` — sets `LastMFAAt = now`, appends `method` to AMR if absent
  - Repository: `UpdateMFA(ctx, userID, sessionID string, amr []string, lastMFAAt int64) error`
  - `session.IsMFAMethod(m string) bool` (true for `otp`, `webauthn`)

- [ ] **Step 1: Write failing unit tests** (extend `service_test.go`, reusing its existing in-memory repo — add `UpdateMFA` to it)

```go
func TestCreateSetsAuthTimeAndAMR(t *testing.T) {
	svc := NewService(newMemRepo()) // adapt to the file's existing fixture
	sess, _, err := svc.Create(context.Background(), "u1", "dev", "1.1.1.1", "ua", []string{AMRPassword})
	if err != nil {
		t.Fatal(err)
	}
	if sess.AuthTime == 0 {
		t.Error("AuthTime not set")
	}
	if len(sess.AMR) != 1 || sess.AMR[0] != AMRPassword {
		t.Errorf("AMR = %v", sess.AMR)
	}
	if sess.LastMFAAt != 0 {
		t.Error("pwd-only login must not set LastMFAAt")
	}
}

func TestCreateWithMFAMethodSetsLastMFAAt(t *testing.T) {
	svc := NewService(newMemRepo())
	sess, _, _ := svc.Create(context.Background(), "u1", "dev", "1.1.1.1", "ua", []string{AMRPassword, AMRTOTP})
	if sess.LastMFAAt == 0 {
		t.Error("MFA login must set LastMFAAt")
	}
}

func TestRecordMFAUpdatesSessionOnce(t *testing.T) {
	repo := newMemRepo()
	svc := NewService(repo)
	sess, _, _ := svc.Create(context.Background(), "u1", "dev", "1.1.1.1", "ua", []string{AMRPassword})

	if err := svc.RecordMFA(context.Background(), "u1", sess.ID(), AMRTOTP); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.GetByID(context.Background(), "u1", sess.ID())
	if got.LastMFAAt == 0 {
		t.Error("LastMFAAt not set")
	}
	if len(got.AMR) != 2 {
		t.Errorf("AMR = %v, want [pwd otp]", got.AMR)
	}
	// idempotent append
	_ = svc.RecordMFA(context.Background(), "u1", sess.ID(), AMRTOTP)
	got, _ = repo.GetByID(context.Background(), "u1", sess.ID())
	if len(got.AMR) != 2 {
		t.Errorf("AMR grew on repeat method: %v", got.AMR)
	}
}
```

- [ ] **Step 2: Run — verify FAIL** — `go test ./internal/domain/session/ -v` → compile errors (new signature/constants).

- [ ] **Step 3: Implement**

model.go additions:

```go
// AMR (Authentication Methods References) values, RFC 8176 where applicable.
const (
	AMRPassword = "pwd"
	AMRTOTP     = "otp"
	AMRWebAuthn = "webauthn"
	AMRGoogle   = "google"
)

func IsMFAMethod(m string) bool { return m == AMRTOTP || m == AMRWebAuthn }
```

Session struct additions:

```go
	AuthTime  int64    `dynamodbav:"auth_time,omitempty"`
	AMR       []string `dynamodbav:"amr,omitempty"`
	LastMFAAt int64    `dynamodbav:"last_mfa_at,omitempty"`
```

service.go — `Create` gains `amr []string` param; inside, after `now := time.Now().UTC()`:

```go
	var lastMFA int64
	for _, m := range amr {
		if IsMFAMethod(m) {
			lastMFA = now.Unix()
			break
		}
	}
	// set on the Session literal:
	AuthTime:  now.Unix(),
	AMR:       amr,
	LastMFAAt: lastMFA,
```

New method:

```go
// RecordMFA marks a successful MFA proof (login gate or step-up challenge) on
// the session so freshly issued tokens carry an up-to-date last_mfa_at claim.
func (s *Service) RecordMFA(ctx context.Context, userID, sessionID, method string) error {
	sess, err := s.repo.GetByID(ctx, userID, sessionID)
	if err != nil {
		return fmt.Errorf("fetching session: %w", err)
	}
	amr := sess.AMR
	found := false
	for _, m := range amr {
		if m == method {
			found = true
			break
		}
	}
	if !found {
		amr = append(amr, method)
	}
	return s.repo.UpdateMFA(ctx, userID, sessionID, amr, time.Now().UTC().Unix())
}
```

repository.go — add to interface and implement with `UpdateItem` (`SET amr = :amr, last_mfa_at = :t`, key pk/sk, condition `attribute_exists(pk)`), mirroring `UpdateGeoData`'s style.

Call sites (`amr` argument per flow):
- `auth.go` password login (no MFA gate): `[]string{session.AMRPassword}`
- `auth.go` MFA challenge success (TOTP after pwd): `[]string{session.AMRPassword, session.AMRTOTP}`
- `passkey.go` discoverable login complete: `[]string{session.AMRWebAuthn}`
- `social.go` Google callback: `[]string{session.AMRGoogle}`

- [ ] **Step 4: Run — verify PASS** — `go test ./internal/domain/session/ ./internal/handler/ -v` (fix in-memory repo compile breaks).

- [ ] **Step 5: Commit**

```bash
git add internal/domain/session/ internal/handler/
git commit -m "feat: track auth_time, amr and last_mfa_at on sessions"
```

---

### Task 2: JWT claims — auth_time, amr, last_mfa_at

**Files:**
- Modify: `internal/crypto/jwt.go:39-52` (`SignAccessToken`)
- Modify: `internal/handler/token.go` (all `SignAccessToken` call sites: code exchange, refresh grant, api_key grant)
- Test: `internal/crypto/jwt_test.go` (create if absent), `internal/handler/wellknown_test.go` untouched

**Interfaces:**
- Consumes: `*session.Session` fields from Task 1 (call sites read `sess.AuthTime`, `sess.AMR`, `sess.LastMFAAt`).
- Produces: `SignAccessToken(userID, sessionID, clientID string, scopes []string, issuer string, audience []string, authTime, lastMFAAt int64, amr []string) (string, error)`. Claims `auth_time`/`last_mfa_at` omitted when 0; `amr` omitted when empty.

- [ ] **Step 1: Failing test** (`internal/crypto/jwt_test.go`)

```go
func TestAccessTokenCarriesStepUpClaims(t *testing.T) {
	svc := newTestJWTService(t) // build from a generated RSA key + minimal config, same as handler testhelpers do
	tok, err := svc.SignAccessToken("u1", "s1", "web", []string{"openid"}, "http://iss", []string{"http://iss"}, 1000, 2000, []string{"pwd", "otp"})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := svc.Verify(tok)
	if err != nil {
		t.Fatal(err)
	}
	if claims["auth_time"].(float64) != 1000 || claims["last_mfa_at"].(float64) != 2000 {
		t.Errorf("claims: %v", claims)
	}
	amr := claims["amr"].([]any)
	if len(amr) != 2 || amr[1].(string) != "otp" {
		t.Errorf("amr: %v", amr)
	}
}

func TestZeroStepUpClaimsAreOmitted(t *testing.T) {
	svc := newTestJWTService(t)
	tok, _ := svc.SignAccessToken("u1", "s1", "web", nil, "http://iss", []string{"http://iss"}, 0, 0, nil)
	claims, _ := svc.Verify(tok)
	for _, k := range []string{"auth_time", "last_mfa_at", "amr"} {
		if _, ok := claims[k]; ok {
			t.Errorf("claim %s should be omitted when zero", k)
		}
	}
}
```

- [ ] **Step 2: Run — verify FAIL** — `go test ./internal/crypto/ -v`.

- [ ] **Step 3: Implement** — in `SignAccessToken`, after building the base claims map:

```go
	if authTime > 0 {
		claims["auth_time"] = authTime
	}
	if lastMFAAt > 0 {
		claims["last_mfa_at"] = lastMFAAt
	}
	if len(amr) > 0 {
		claims["amr"] = amr
	}
```

Call sites in `token.go`: code exchange and refresh grant pass `sess.AuthTime, sess.LastMFAAt, sess.AMR` (both paths already hold the `*session.Session`); api_key grant passes `0, 0, nil`.

- [ ] **Step 4: Run — verify PASS** — `go test ./internal/crypto/ ./internal/handler/ -v`.

- [ ] **Step 5: Commit**

```bash
git add internal/crypto/ internal/handler/
git commit -m "feat: embed auth_time, amr and last_mfa_at claims in access tokens"
```

---

### Task 3: Problem types + RequireRecentMFA middleware

**Files:**
- Modify: `internal/apierror/problems.go` (or wherever `newProblem` lives — same file as `TokenReuse`)
- Modify: `internal/middleware/auth.go` (expose new claims as locals)
- Create: `internal/middleware/stepup.go`
- Create: `internal/middleware/stepup_test.go`

**Interfaces:**
- Consumes: claims from Task 2.
- Produces:
  - `apierror.StepUpRequired(maxAge time.Duration, instance string) *Problem` — 403, slug `step-up-required`, `Extensions["max_age_seconds"]` (add an `Extensions map[string]any` mechanism only if Problem already supports extra fields; otherwise add field `MaxAgeSeconds int64` serialized as `max_age_seconds` — inspect `Problem` struct first and follow its serialization style)
  - `apierror.MFAEnrollmentRequired(instance string) *Problem` — 403, slug `mfa-enrollment-required`
  - `middleware.StepUpMaxAge = 5 * time.Minute`
  - `middleware.LocalLastMFAAt = "last_mfa_at"` local (int64), set in `extractAndVerify`
  - `middleware.RequireRecentMFA(maxAge time.Duration) fiber.Handler` — must run after `RequireAuth`

- [ ] **Step 1: Failing middleware test** (`stepup_test.go`, follow `ratelimit_test.go` style — build a tiny fiber app, mint tokens with the real `crypto.JWTService`)

```go
func TestRequireRecentMFA(t *testing.T) {
	jwtSvc := newTestJWT(t) // same fixture style as auth middleware tests / crypto tests
	app := fiber.New()
	app.Get("/p", RequireAuth(jwtSvc), RequireRecentMFA(5*time.Minute), func(c fiber.Ctx) error {
		return c.SendString("ok")
	})

	now := time.Now().Unix()
	cases := []struct {
		name      string
		lastMFAAt int64
		amr       []string
		want      int
	}{
		{"fresh mfa", now - 60, []string{"pwd", "otp"}, 200},
		{"stale mfa", now - 3600, []string{"pwd", "otp"}, 403},
		{"never mfa", 0, []string{"pwd"}, 403},
		{"api key token (no claims)", 0, nil, 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tok, _ := jwtSvc.SignAccessToken("u1", "s1", "web", nil, issuer, []string{issuer}, now-7200, tc.lastMFAAt, tc.amr)
			req := httptest.NewRequest("GET", "/p", nil)
			req.Header.Set("Authorization", "Bearer "+tok)
			resp, _ := app.Test(req)
			if resp.StatusCode != tc.want {
				t.Errorf("got %d want %d", resp.StatusCode, tc.want)
			}
		})
	}
}
```

Also assert the 403 body has `"type"` ending in `step-up-required` and `max_age_seconds: 300`.

- [ ] **Step 2: Run — verify FAIL** — `go test ./internal/middleware/ -v`.

- [ ] **Step 3: Implement**

`auth.go` — in `extractAndVerify`, also extract `last_mfa_at` (`float64` → int64, 0 when missing) and set `c.Locals(LocalLastMFAAt, v)` in both `RequireAuth` and `OptionalAuth` (extend the returned values or return the claims map — smallest diff wins; keep exported getters):

```go
// GetLastMFAAt returns the last_mfa_at claim (0 when absent).
func GetLastMFAAt(c fiber.Ctx) int64 {
	v, _ := c.Locals(LocalLastMFAAt).(int64)
	return v
}
```

`stepup.go`:

```go
package middleware

import (
	"time"

	"gopkg.aoctech.app/account/internal/apierror"
	"github.com/gofiber/fiber/v3"
)

// StepUpMaxAge is the default freshness window for step-up-protected routes.
const StepUpMaxAge = 5 * time.Minute

// RequireRecentMFA rejects requests whose token lacks an MFA proof newer than
// maxAge. Stateless: it reads only JWT claims, so after a successful step-up
// challenge the client must silent-refresh to obtain updated claims.
// Must be registered after RequireAuth.
func RequireRecentMFA(maxAge time.Duration) fiber.Handler {
	return func(c fiber.Ctx) error {
		lastMFA := GetLastMFAAt(c)
		if lastMFA == 0 || time.Since(time.Unix(lastMFA, 0)) > maxAge {
			return apierror.StepUpRequired(maxAge, c.Path()).Send(c)
		}
		return c.Next()
	}
}
```

`apierror` additions (match the file's existing constructor style exactly):

```go
// StepUpRequired signals the token's MFA proof is missing or older than maxAge.
func StepUpRequired(maxAge time.Duration, instance string) *Problem {
	p := newProblem("step-up-required", "Step-up Authentication Required", http.StatusForbidden,
		"This operation requires recent multi-factor authentication.", instance)
	p.MaxAgeSeconds = int64(maxAge.Seconds()) // adapt to Problem's extension mechanism
	return p
}

// MFAEnrollmentRequired signals the user must enroll an MFA method first.
func MFAEnrollmentRequired(instance string) *Problem {
	return newProblem("mfa-enrollment-required", "MFA Enrollment Required", http.StatusForbidden,
		"Enroll an authenticator app or passkey to perform this operation.", instance)
}
```

- [ ] **Step 4: Run — verify PASS** — `go test ./internal/middleware/ ./internal/apierror/ -v`.

- [ ] **Step 5: Commit**

```bash
git add internal/apierror/ internal/middleware/
git commit -m "feat: add RequireRecentMFA middleware and step-up Problem types"
```

---

### Task 4: Step-up challenge endpoint (TOTP + passkey)

**Files:**
- Create: `internal/handler/stepup.go`
- Create: `internal/handler/stepup_test.go`
- Modify: `cmd/api/main.go` (register)
- Modify: `internal/handler/testhelpers_test.go` (register in test app)

**Interfaces:**
- Consumes: `session.Service.RecordMFA` (Task 1), TOTP service (`Validate(ctx, userID, code)` — same one the login MFA gate uses, see `auth.go`), passkey service begin/complete (same pattern as `passkey.go` authenticate), `audit.Service`, `middleware.RateLimit`.
- Produces:
  - `POST /v1.0/auth/step-up` (RequireAuth) body `{"method":"totp","code":"123456"}` → 204; wrong code → 401 `invalid-credentials`; no TOTP and no passkeys enrolled → 403 `mfa-enrollment-required`
  - `POST /v1.0/auth/step-up/passkeys/begin` + `POST /v1.0/auth/step-up/passkeys/complete` (RequireAuth) — WebAuthn assertion for the **current** user (non-discoverable is fine: pass the user to the begin call), complete → 204
  - On success both paths call `sessionSvc.RecordMFA(ctx, userID, sessionID, method)` + audit `EventStepUpSuccess`; failures audit `EventStepUpFailed`

- [ ] **Step 1: Failing integration tests**

```go
func TestStepUpTOTPSuccess(t *testing.T) {
	ta := newTestApp(t)
	userID, token := ta.createUserWithTOTP(t) // helper: enroll TOTP via the real service, return a valid current code generator
	code := ta.totpCode(t, userID)

	resp := ta.postJSON(t, "/v1.0/auth/step-up", token, map[string]string{"method": "totp", "code": code})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("got %d", resp.StatusCode)
	}
	// session updated → next refresh carries fresh last_mfa_at
	sess, _ := ta.sessionRepo.GetByID(t.Context(), userID, sessionIDFromToken(t, ta, token))
	if sess.LastMFAAt == 0 {
		t.Error("RecordMFA not applied")
	}
}

func TestStepUpWrongCodeIs401(t *testing.T)            { /* code "000000" → 401, problem type invalid-credentials */ }
func TestStepUpWithoutEnrollmentIs403Enrollment(t *testing.T) { /* user with no TOTP/passkey → 403 mfa-enrollment-required */ }
func TestStepUpRequiresAuth(t *testing.T)              { /* no bearer → 401 */ }
```

Write the four bodies fully in the file — assert on Problem `type` slug, not only status.

- [ ] **Step 2: Run — verify FAIL** — `go test ./internal/handler/ -run TestStepUp -v`.

- [ ] **Step 3: Implement stepup.go**

```go
package handler

import (
	"gopkg.aoctech.app/account/internal/apierror"
	"gopkg.aoctech.app/account/internal/cache"
	"gopkg.aoctech.app/account/internal/domain/audit"
	"gopkg.aoctech.app/account/internal/domain/session"
	"gopkg.aoctech.app/account/internal/middleware"
	"gopkg.aoctech.app/account/internal/validate"
	"github.com/gofiber/fiber/v3"
)

const stepUpMethodTOTP = "totp"

type StepUpHandler struct {
	sessionSvc *session.Service
	totpSvc    TOTPService // reuse the interface auth.go already defines for the login gate
	passkeySvc PasskeyAuthService // extract/reuse from passkey.go if an interface exists; else use the concrete *passkey.Service like passkey.go does
	auditSvc   *audit.Service
	cache      *cache.Client
}

func NewStepUpHandler(sessionSvc *session.Service, totpSvc TOTPService, passkeySvc *passkeyDomainService, auditSvc *audit.Service, cacheCli *cache.Client) *StepUpHandler { /* mirror the other constructors */ }

func (h *StepUpHandler) Register(v1 fiber.Router, requireAuth fiber.Handler) {
	rl := middleware.RateLimit(middleware.RateLimitConfig{
		Cache: h.cache, Prefix: "stepup",
		Max: middleware.FailedLoginMax, Window: middleware.FailedLoginWindow,
		KeyFunc: func(c fiber.Ctx) string { return middleware.GetUserID(c) },
		CountOnlyFailures: true,
	})
	auth := v1.Group("/auth")
	auth.Post("/step-up", requireAuth, rl, h.challenge)
	auth.Post("/step-up/passkeys/begin", requireAuth, rl, h.passkeyBegin)
	auth.Post("/step-up/passkeys/complete", requireAuth, rl, h.passkeyComplete)
}

type stepUpRequest struct {
	Method string `json:"method" validate:"required,oneof=totp"`
	Code   string `json:"code" validate:"required,len=6,numeric"`
}

func (h *StepUpHandler) challenge(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)
	sessionID := middleware.GetSessionID(c)

	var req stepUpRequest
	if err := c.BodyParser(&req); err != nil {
		return apierror.InvalidRequest("Request body is required.", c.Path()).Send(c)
	}
	if err := validate.Struct(req); err != nil {
		if ve, ok := validate.IsValidationError(err); ok {
			return apierror.ValidationFailed(ve.Detail(), c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}

	if !h.userHasMFA(c, userID) {
		return apierror.MFAEnrollmentRequired(c.Path()).Send(c)
	}

	ok, err := h.totpSvc.Validate(c.Context(), userID, req.Code)
	if err != nil || !ok {
		h.auditSvc.Record(c.Context(), audit.Entry{UserID: userID, Type: audit.EventStepUpFailed,
			IP: clientIP(c), UserAgent: c.Get("User-Agent"), Metadata: map[string]string{"method": stepUpMethodTOTP}})
		return apierror.InvalidCredentials(c.Path()).Send(c)
	}

	if err := h.sessionSvc.RecordMFA(c.Context(), userID, sessionID, session.AMRTOTP); err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	h.auditSvc.Record(c.Context(), audit.Entry{UserID: userID, Type: audit.EventStepUpSuccess,
		IP: clientIP(c), UserAgent: c.Get("User-Agent"), Metadata: map[string]string{"method": stepUpMethodTOTP}})
	return c.SendStatus(fiber.StatusNoContent)
}

// userHasMFA reports whether the user has TOTP or at least one passkey enrolled.
func (h *StepUpHandler) userHasMFA(c fiber.Ctx, userID string) bool {
	if _, err := h.totpSvc.Get(c.Context(), userID); err == nil {
		return true
	}
	creds, err := h.passkeySvc.List(c.Context(), userID) // adapt to the actual passkey service method (see internal/handler/passkey.go)
	return err == nil && len(creds) > 0
}
```

`passkeyBegin`/`passkeyComplete`: copy the WebAuthn assertion flow from `passkey.go`'s authenticate begin/complete, but for the already-authenticated user (challenge stored in Valkey under `stepup_webauthn:{userID}`, 5-min TTL, same as login challenges) and finishing with `RecordMFA(..., session.AMRWebAuthn)` + audit, returning 204.

Adapt interface names to what `auth.go`/`passkey.go` actually define — do not invent parallel interfaces if one exists.

- [ ] **Step 4: Wire** — main.go: `stepUpH := handler.NewStepUpHandler(sessionSvc, totpSvc, passkeySvc, auditSvc, valkeyClient)`; `stepUpH.Register(v1, requireAuth)` (where `requireAuth` is the existing `middleware.RequireAuth(jwtSvc)` value used by the account group). Same in testhelpers.

- [ ] **Step 5: Run — verify PASS** — `go test ./internal/handler/ -run TestStepUp -v`, then `go test ./...`.

- [ ] **Step 6: Commit**

```bash
git add cmd/ internal/
git commit -m "feat: add POST /v1.0/auth/step-up challenge (TOTP + passkey)"
```

---

### Task 5: Protect sensitive routes

**Files:**
- Modify: `internal/handler/profile.go`, `internal/handler/mfa.go`, `internal/handler/passkey.go`, `internal/handler/apikeys.go`, `internal/handler/oauth_clients.go` (Register methods)
- Modify: `cmd/api/main.go` + `internal/handler/testhelpers_test.go` (pass the middleware)
- Modify: integration tests for the protected routes

**Interfaces:**
- Consumes: `middleware.RequireRecentMFA(middleware.StepUpMaxAge)`.
- Produces: step-up enforced on: change password, TOTP remove, backup-codes regenerate, passkey delete, API key create, OAuth client create/update/delete.

- [ ] **Step 1: Thread the middleware** — each listed handler's `Register` gains a `stepUp fiber.Handler` parameter applied only to the routes above, e.g. in `mfa.go`:

```go
func (h *MFAHandler) Register(account fiber.Router, stepUp fiber.Handler) {
	account.Get("/mfa/totp/setup", h.setup)
	account.Post("/mfa/totp/confirm", h.confirm)
	account.Delete("/mfa/totp", stepUp, h.remove)
	account.Post("/mfa/totp/backup-codes", stepUp, h.regenerateBackupCodes)
}
```

main.go: `stepUp := middleware.RequireRecentMFA(middleware.StepUpMaxAge)` passed to the five Register calls.

**Deliberate exception:** TOTP setup/confirm and passkey **register** stay unprotected — they are the enrollment path a no-MFA user must be able to reach.

- [ ] **Step 2: Failing-then-passing integration test** (`internal/handler/stepup_test.go`)

```go
func TestSensitiveRouteRejectsStaleMFA(t *testing.T) {
	// token minted with last_mfa_at = 0 → DELETE /v1.0/account/mfa/totp must 403 step-up-required
}

func TestSensitiveRouteAllowsFreshMFA(t *testing.T) {
	// token minted with last_mfa_at = now → same call passes auth (2xx/404 from domain is fine)
}
```

Write both bodies fully; mint tokens directly with `ta.jwtSvc.SignAccessToken(...)` to control claims.

- [ ] **Step 3: Run full suite** — `go test ./...` → PASS. Existing tests for the protected routes must mint fresh-MFA tokens now — update helpers once in `testhelpers_test.go` (default test token: `last_mfa_at = now`), so most tests stay untouched.

- [ ] **Step 4: Commit**

```bash
git add cmd/ internal/
git commit -m "feat: enforce step-up auth on sensitive account routes"
```

---

### Task 6: UI — step-up modal + enrollment redirect + README

**Files:**
- Create: `ui/src/components/step-up-dialog.tsx`
- Modify: the UI's central API-error handling (find where Problem responses are parsed — `rg -n "problem|application/problem" ui/src`) to surface `step-up-required` / `mfa-enrollment-required`
- Modify: i18n en + pt-BR (`stepUp` namespace: title, description, totp label, passkey button, enroll CTA)
- Modify: `README.md` — routes table (`POST /v1.0/auth/step-up`, `.../passkeys/begin`, `.../passkeys/complete`), claims documentation (`auth_time`, `amr`, `last_mfa_at`), protected-routes list

**Interfaces:**
- Consumes: `POST /v1.0/auth/step-up` (+ passkey begin/complete), existing silent-refresh helper (`lib/auth-hint.ts` flow), existing `otp-input.tsx` component.

- [ ] **Step 1: Read `ui/CLAUDE.md`** and the existing mutation/error-handling pattern (Server Actions vs client fetch) before writing anything.

- [ ] **Step 2: Implement dialog** — behavior contract:
  1. Any API response with Problem `type` ending `step-up-required` opens the dialog instead of showing an error toast.
  2. Dialog offers TOTP (6-digit `otp-input`) and, when WebAuthn is available, a "Use passkey" button.
  3. On challenge success → call the existing silent-refresh routine → retry the original request → close.
  4. Problem `mfa-enrollment-required` (from the challenge) → dialog swaps to an enroll CTA linking `/account/security`.
  5. Wrong code → inline error, input cleared; 429 → show rate-limit message.

- [ ] **Step 3: Verify** — `cd ui && npm run build`; manual flow with API running: change password with stale MFA → dialog → TOTP → succeeds.

- [ ] **Step 4: Commit**

```bash
git add README.md ui/
git commit -m "feat(ui): step-up dialog with silent refresh and enrollment redirect"
```
