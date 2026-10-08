# Account Deletion — Phase 1 (request lifecycle + lock) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a user request deletion of their CTech account, confirm it by e-mail, be locked out
everywhere at once, cancel during the 7-day grace, and have the worker take the request to
`locked` → `purging` at the end of grace (publishing `user.erase` when participants are configured).

**Architecture:** New `internal/domain/deletion` package: a `Request` stored in
`{env}_account_deletion_requests` (one `SUB#{user}/OPEN` marker enforces a single open request),
a `Service` whose every state change is a conditional write made **before** its side effects, and a
sparse `gsi_state_due` index the worker polls to retry side effects until they are confirmed.
Side effects go through an `AccountLocker` (user flag, sessions, API keys, JWT revocation list,
SNS publish). Sign-in refuses an account with `deletion_state` set.

**Tech Stack:** Go 1.27, Fiber v3, DynamoDB (aws-sdk-go-v2), Valkey, SNS, `gopkg.aoctech.app/api-commons`
(`erasure`, `jwtverify`, `cache`, `dynamo`), CDK (TypeScript).

**Spec:** `docs/specs/2026-10-06-account-deletion-ctech-account.md` (with
`2026-10-06-account-deletion-overview.md` decisions D1–D14 and
`2026-10-06-account-deletion-saga-protocol.md`).

**Phases (this plan is Phase 1):**
1. **Lifecycle + lock** (this plan): request, e-mail confirm, lock, cancel, reminder, grace end → `locked` → `purging`.
2. Own purge: ctech-account's data per inventory §3 (orgs, memberships, consents, MFA, passkeys, support, audit anonymization, KYC retention), tombstone, local blockers (multi-member org ownership).
3. Participants: eligibility fan-out, `/internal/erasure/ack`, reconciler, `blocked`, admin legal hold / redrive, risk-signal review flag.
4. UI (`ui/`): settings screens, `/account-deletion/confirm` and `/account-deletion/cancel` pages, new privacy-policy version.

The feature stays behind `ACCOUNT_DELETION_ENABLED` (unset = routes and worker not wired) until Phase 2 ships.

## Global Constraints

- Branch `feat/account-deletion`. Commits: Conventional Commits, **no `Co-Authored-By` or any Claude attribution trailer**.
- Layering `handler → service → repository`; no AWS SDK in `internal/domain/*/service.go`; services take interfaces.
- Every HTTP error is an `*apierror.Problem`; handlers **return** problems (Fiber's error handler sends them) or `return problem.Send(c)`, never both.
- Fiber v3: `c.Context()`, never `c.UserContext()`.
- Grace period **7 days** (D8), confirmation window **24 h**, reminder **24 h** before lock, typed phrase **`EXCLUIR MINHA CONTA`**.
- One open request per user. Request rate limit: **3 per user per 30 days**.
- Messages to participants carry only the `sub` (`erasure.Message`); never log CPF, e-mail, tokens.
- Revocation entries via `jwtverify.Revoke`/`Unrevoke` in Valkey **DB 0** (account's base `VALKEY_URL`).
- Tests: `cd api && go test ./...` must pass after every task; `go vet ./...` clean.

## Rulings against the spec (apply in Task 8's spec update)

- **R1 Cancel only by e-mail link** (no restricted "cancel" session). The cancel link is sent at confirmation and again in the 24 h reminder; support can cancel in Phase 3. Removes a second, scoped login mode from a high-blast-radius flow. Cost if wrong: a user who lost every e-mail must go through support.
- **R2 Identity check at request:** recent MFA proof (`last_mfa_at` within `StepUpMaxAge`) **or** the account password; an account with no password (Google-only) relies on the e-mail confirmation alone. Spec's "repeat Google login" is dropped. Cost if wrong: a stolen Google-only session still needs mailbox access, which the confirmation already demands.
- **R3 Account's own API also honours the revocation list** (via `jwtverify.CheckRevoked`), otherwise a locked user's 15-minute access token could create a fresh API key during grace.

## Review Focus

1. **Cancel racing the grace-end lock.** Exactly one wins; a cancel after `locked` is refused. Test: Task 4 `TestCancel_RejectsWrongTokenAndAfterLock`.
2. **Lock side effect fails right after confirmation** (Valkey/SNS down). The request is still confirmed and the worker retries the lock until it sticks. Test: Task 4 `TestConfirm_LockFailureRetriedByWorker`.
3. **A stale worker lock lands after a cancel.** The stale lock must be undone. Test: Task 4 `TestApplyLock_CancelRaceCompensates`.
4. **The user lost the confirmation e-mail** and asks again. The new request replaces the unconfirmed one instead of a 24 h `409`. Test: Task 4 `TestRequest_ReplacesUnconfirmedRequest`.
5. **The pre-lock access token is used on the account API** (e.g. to mint an API key during grace). It gets `401`. Test: Task 6 `TestDeletion_FullFlow`.

---

### Task 0: `ctech-go-common` — export `jwtverify.CheckRevoked` (v1.13.1)

ctech-account signs and verifies its own tokens (no `jwtverify.Verifier`), so it needs the revocation check as a function.
The key format must stay owned by `jwtverify`.

**Files (repo `/home/artur/Documents/Projects/Ctech/ctech-go-common`):**
- Modify: `jwtverify/revocation.go`
- Test: `jwtverify/revocation_test.go`
- Modify: `README.md` (JWT revocation section), `AGENTS.md` (jwtverify revocation line)

**Interfaces:**
- Produces: `func CheckRevoked(ctx context.Context, c cache.Backend, sub string, iat int64) error` → `nil`, `ErrTokenRevoked`, or an error wrapping `ErrRevocationUnavailable`.

- [ ] **Step 1: Write the failing test** — append to `jwtverify/revocation_test.go`:

```go
func TestCheckRevoked(t *testing.T) {
	ctx := context.Background()
	rev := cache.NewMemoryBackend(16)
	now := time.Now()
	_ = jwtverify.Revoke(ctx, rev, "user-1", now, jwtverify.RevocationTTL)

	if err := jwtverify.CheckRevoked(ctx, rev, "user-1", now.Add(-time.Minute).Unix()); !errors.Is(err, jwtverify.ErrTokenRevoked) {
		t.Fatalf("before cutoff: err = %v, want ErrTokenRevoked", err)
	}
	if err := jwtverify.CheckRevoked(ctx, rev, "user-1", now.Add(time.Minute).Unix()); err != nil {
		t.Fatalf("after cutoff: err = %v, want nil", err)
	}
	if err := jwtverify.CheckRevoked(ctx, rev, "user-2", 0); err != nil {
		t.Fatalf("other sub: err = %v, want nil", err)
	}
	if err := jwtverify.CheckRevoked(ctx, downBackend{}, "user-1", now.Unix()); !errors.Is(err, jwtverify.ErrRevocationUnavailable) {
		t.Fatalf("backend down: err = %v, want ErrRevocationUnavailable", err)
	}
}
```

- [ ] **Step 2: Run, verify it fails**

Run: `cd /home/artur/Documents/Projects/Ctech/ctech-go-common && go test ./jwtverify/ -run TestCheckRevoked -count=1`
Expected: FAIL `undefined: jwtverify.CheckRevoked`

- [ ] **Step 3: Implement** — in `jwtverify/revocation.go`, replace the whole `checkRevoked` method with:

```go
// CheckRevoked reports whether sub's token issued at iat is on the revocation
// list. For services that verify tokens without a Verifier (ctech-account signs
// and verifies its own). A backend failure is returned wrapped in
// ErrRevocationUnavailable; the caller chooses to fail open or closed.
func CheckRevoked(ctx context.Context, c cache.Backend, sub string, iat int64) error {
	raw, ok, err := c.Get(ctx, revokedSubPrefix+sub)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRevocationUnavailable, err)
	}
	if !ok {
		return nil
	}
	cutoff, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || iat <= cutoff {
		return ErrTokenRevoked // an unparseable entry fails safe
	}
	return nil
}

func (v *Verifier) checkRevoked(ctx context.Context, cl *Claims, strict bool) error {
	if v.revocation == nil {
		return nil
	}
	err := CheckRevoked(ctx, v.revocation, cl.Sub, cl.IssuedAt)
	if errors.Is(err, ErrRevocationUnavailable) && !strict {
		slog.WarnContext(ctx, "jwtverify: revocation check skipped", "error", err)
		return nil
	}
	return err
}
```

- [ ] **Step 4: Run the suite**

Run: `go vet ./... && go test ./... -race -count=1`
Expected: all packages `ok`.

- [ ] **Step 5: Docs** — README "### JWT revocation": append the sentence
  ``Services that verify tokens themselves (ctech-account) call `jwtverify.CheckRevoked(ctx, backend, sub, iat)` directly.``
  AGENTS.md jwtverify revocation line: add `CheckRevoked`.

- [ ] **Step 6: Commit** (on a branch in ctech-go-common)

```bash
git checkout -b feat/jwtverify-check-revoked
git add jwtverify/ README.md AGENTS.md
git commit -m "feat(jwtverify): export CheckRevoked for self-verifying services"
```

- [ ] **Step 7: Release — ask the user first** (publishing a module version is outward-facing):

```bash
git checkout main && git merge --ff-only feat/jwtverify-check-revoked && git push origin main
git tag v1.13.1 && git push origin v1.13.1 && git branch -d feat/jwtverify-check-revoked
```

---

### Task 1: Bump `api-commons` to v1.13.1

v1.9.1 → v1.13.1 crosses the `dynamo.IsConditionFailed` reclassification (v1.10, `ctech-go-common` CLAUDE.md). Account uses `dynamo.Base`, so the full suite is the gate.

**Files:** Modify `api/go.mod`, `api/go.sum`.

- [ ] **Step 1: Bump**

Run: `cd api && go get gopkg.aoctech.app/api-commons@v1.13.1 && go mod tidy`
Expected: `go.mod` pins `gopkg.aoctech.app/api-commons v1.13.1`.

- [ ] **Step 2: Build and test**

Run: `go vet ./... && go build ./... && go test ./... 2>&1 | tail -30`
Expected: every package `ok`. A failure here is a behaviour change from the upgrade: debug it (superpowers:systematic-debugging) before continuing; do not edit tests to pass.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore(api): bump api-commons to v1.13.1"
```

---

### Task 2: Sign-in gates for a pending deletion

**Files:**
- Modify: `api/internal/domain/user/model.go` (fields + const)
- Modify: `api/internal/domain/user/service.go` (`ErrPendingDeletion`, `Login`, `MarkPendingDeletion`, `ClearPendingDeletion`, `CheckPassword`)
- Modify: `api/internal/apierror/problem.go` (`AccountPendingDeletion`)
- Modify: `api/internal/handler/auth.go` (`login`), `api/internal/handler/passkey.go`, `api/internal/handler/social.go`
- Modify: `api/internal/domain/user/service_test.go` (`mockRepo.Update`), `api/internal/handler/testhelpers_test.go` (`memUserRepo.Update`)
- Test: `api/internal/domain/user/service_test.go`, `api/internal/handler/auth_test.go`

**Interfaces:**
- Produces:
  - `user.DeletionStatePending = "pending"`; `User.DeletionState`, `User.DeletionRequestID`
  - `user.ErrPendingDeletion`
  - `func (s *user.Service) MarkPendingDeletion(ctx context.Context, userID, requestID string) error`
  - `func (s *user.Service) ClearPendingDeletion(ctx context.Context, userID, requestID string) error`
  - `func (s *user.Service) CheckPassword(ctx context.Context, userID, password string) error` (`ErrInvalidCredentials` when wrong or no password)
  - `func apierror.AccountPendingDeletion(instance string) *Problem` (type slug `account-pending-deletion`, 403)

- [ ] **Step 1: Teach both mock repos the new fields**

In `api/internal/domain/user/service_test.go`, inside `mockRepo.Update` before `return nil`:

```go
	if _, ok := updates["deletion_state"]; ok {
		u.DeletionState, _ = updates["deletion_state"].(string) // nil = REMOVE
	}
	if _, ok := updates["deletion_request_id"]; ok {
		u.DeletionRequestID, _ = updates["deletion_request_id"].(string)
	}
```

In `api/internal/handler/testhelpers_test.go`, inside `memUserRepo.Update`'s `switch k`:

```go
		case "deletion_state":
			u.DeletionState, _ = v.(string) // nil = REMOVE
		case "deletion_request_id":
			u.DeletionRequestID, _ = v.(string)
```

- [ ] **Step 2: Write the failing tests**

Append to `api/internal/domain/user/service_test.go` (uses the file's existing `newMockRepo`, `user.NewService`, and `Register`; adapt the registration call to the helper the file's `TestRegister_Success` uses if its signature differs):

```go
func TestPendingDeletion_BlocksLoginAndClearsOnlyOwnRequest(t *testing.T) {
	ctx := context.Background()
	svc := user.NewService(newMockRepo())
	u, err := svc.Register(ctx, "del@example.com", "Sup3rSecret!", "Ana", "Silva")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := svc.MarkEmailVerified(ctx, u.ID()); err != nil {
		t.Fatalf("MarkEmailVerified: %v", err)
	}
	if err := svc.MarkPendingDeletion(ctx, u.ID(), "req-1"); err != nil {
		t.Fatalf("MarkPendingDeletion: %v", err)
	}
	if _, err := svc.Login(ctx, "del@example.com", "Sup3rSecret!"); !errors.Is(err, user.ErrPendingDeletion) {
		t.Fatalf("Login err = %v, want ErrPendingDeletion", err)
	}
	if err := svc.ClearPendingDeletion(ctx, u.ID(), "req-OTHER"); err != nil {
		t.Fatalf("ClearPendingDeletion(other): %v", err)
	}
	if _, err := svc.Login(ctx, "del@example.com", "Sup3rSecret!"); !errors.Is(err, user.ErrPendingDeletion) {
		t.Fatal("a stale request id must not lift the block")
	}
	if err := svc.ClearPendingDeletion(ctx, u.ID(), "req-1"); err != nil {
		t.Fatalf("ClearPendingDeletion: %v", err)
	}
	if _, err := svc.Login(ctx, "del@example.com", "Sup3rSecret!"); err != nil {
		t.Fatalf("Login after clear: %v", err)
	}
}

func TestCheckPassword(t *testing.T) {
	ctx := context.Background()
	svc := user.NewService(newMockRepo())
	u, _ := svc.Register(ctx, "pw@example.com", "Sup3rSecret!", "Ana", "Silva")
	if err := svc.CheckPassword(ctx, u.ID(), "Sup3rSecret!"); err != nil {
		t.Fatalf("right password: %v", err)
	}
	if err := svc.CheckPassword(ctx, u.ID(), "wrong"); !errors.Is(err, user.ErrInvalidCredentials) {
		t.Fatalf("wrong password: err = %v", err)
	}
}
```

Append to `api/internal/handler/auth_test.go`:

```go
func TestLogin_PendingDeletionRefused(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "pending@example.com", "Sup3rSecret!", "Ana")
	if err := ta.userSvc.MarkPendingDeletion(context.Background(), u.ID(), "req-1"); err != nil {
		t.Fatalf("MarkPendingDeletion: %v", err)
	}
	resp := ta.do("POST", "/v1.0/auth/login", map[string]string{"email": "pending@example.com", "password": "Sup3rSecret!"})
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(bodyString(resp), "account-pending-deletion") {
		t.Fatalf("status %d body %s, want 403 account-pending-deletion", resp.StatusCode, bodyString(resp))
	}
}
```

- [ ] **Step 3: Run, verify they fail**

Run: `cd api && go test ./internal/domain/user/ ./internal/handler/ -run 'PendingDeletion|CheckPassword' -count=1`
Expected: build FAIL (`MarkPendingDeletion undefined`, `ErrPendingDeletion undefined`, ...).

- [ ] **Step 4: Implement**

`api/internal/domain/user/model.go` — add to `User` after `SupportRole`:

```go
	// DeletionState is set while a confirmed deletion request is open
	// (docs/specs/2026-10-06-account-deletion-ctech-account.md). Every sign-in
	// path refuses a non-empty value.
	DeletionState     string `dynamodbav:"deletion_state,omitempty"`
	DeletionRequestID string `dynamodbav:"deletion_request_id,omitempty"`
```

and next to the `SupportRole*` consts:

```go
// DeletionStatePending marks an account locked by a confirmed deletion request.
const DeletionStatePending = "pending"
```

`api/internal/domain/user/service.go` — next to `ErrAccountDisabled`:

```go
var ErrPendingDeletion = errors.New("account is scheduled for deletion")
```

In `Login`, right after the `if !u.IsEnabled { ... }` block:

```go
	if u.DeletionState != "" {
		return nil, ErrPendingDeletion
	}
```

New methods at the end of the file:

```go
// MarkPendingDeletion blocks every sign-in for userID on behalf of deletion
// request requestID.
func (s *Service) MarkPendingDeletion(ctx context.Context, userID, requestID string) error {
	return s.repo.Update(ctx, userID, map[string]any{
		"deletion_state":      DeletionStatePending,
		"deletion_request_id": requestID,
	})
}

// ClearPendingDeletion lifts the block only while it still belongs to
// requestID, so a late unlock of an old request cannot free a newer one.
// ponytail: read-then-write, not a conditional update; only the deletion flow
// writes these fields and a user has at most one open request.
func (s *Service) ClearPendingDeletion(ctx context.Context, userID, requestID string) error {
	u, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.DeletionRequestID != requestID {
		return nil
	}
	return s.repo.Update(ctx, userID, map[string]any{"deletion_state": nil, "deletion_request_id": nil})
}

// CheckPassword proves the caller knows the account password (identity check
// for sensitive actions). ErrInvalidCredentials when wrong or when the account
// has no password.
func (s *Service) CheckPassword(ctx context.Context, userID, password string) error {
	u, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.PasswordHash == "" {
		return ErrInvalidCredentials
	}
	ok, err := crypto.VerifyPassword(password, u.PasswordHash)
	if err != nil {
		return fmt.Errorf("verifying password: %w", err)
	}
	if !ok {
		return ErrInvalidCredentials
	}
	return nil
}
```

`api/internal/apierror/problem.go` — after `AccountDisabled`:

```go
// AccountPendingDeletion answers a sign-in for an account with a confirmed
// deletion request. Sent only after the credentials were proven, so it is not
// an enumeration oracle.
func AccountPendingDeletion(instance string) *Problem {
	return newProblem("account-pending-deletion", "Account Scheduled for Deletion", http.StatusForbidden,
		"This account is scheduled for deletion. Use the cancel link sent to your e-mail to keep it.", instance)
}
```

`api/internal/handler/auth.go` — in `login`, right after the `ErrEmailNotVerified` branch:

```go
		if errors.Is(err, user.ErrPendingDeletion) {
			return apierror.AccountPendingDeletion(c.Path()).Send(c)
		}
```

`api/internal/handler/passkey.go` — right after the `if !u.IsEnabled { ... }` block:

```go
	if u.DeletionState != "" {
		recordAudit(c, h.audit, userID, audit.EventLoginFailed, map[string]string{"method": session.AMRWebAuthn})
		return apierror.AccountPendingDeletion(c.Path()).Send(c)
	}
```

`api/internal/handler/social.go` — immediately before **each** of the two `h.sessionSvc.Create(` calls (the one in the `if !payload.Reaccept {` block and the one in `issueSessionFromSocial`):

```go
	if u.DeletionState != "" {
		return c.Redirect().Status(fiber.StatusFound).To(h.cfg.AppURL + "/login?error=account_pending_deletion")
	}
```

(Google sign-in had no `IsEnabled` check either; that pre-existing gap is out of scope here — note it in the final report.)

- [ ] **Step 5: Run, verify they pass**

Run: `go test ./internal/domain/user/ ./internal/handler/ -count=1`
Expected: `ok` for both.

- [ ] **Step 6: Commit**

```bash
git add internal/domain/user internal/apierror internal/handler/auth.go internal/handler/passkey.go internal/handler/social.go internal/handler/auth_test.go internal/handler/testhelpers_test.go
git commit -m "feat(api): refuse sign-in for accounts pending deletion"
```

---

### Task 3: `deletion` model and DynamoDB repository

**Files:**
- Create: `api/internal/domain/deletion/model.go`
- Create: `api/internal/domain/deletion/repository.go`
- Test: `api/internal/domain/deletion/model_test.go`

**Interfaces:**
- Produces (package `deletion`):
  - consts `GracePeriod`, `ConfirmWindow`, `ReminderLead`, `ConfirmationPhrase`, `ScopeAccount`, `TableSuffix = "account_deletion_requests"`, `DueIndex = "gsi_state_due"`
  - `type State string` + `StateAwaitingConfirmation`, `StatePending`, `StateCancelled`, `StateExpired`, `StateLocked`, `StatePurging`; `func (s State) Open() bool`
  - errors `ErrNotFound`, `ErrOpenRequest`, `ErrStateChanged`, `ErrInvalidToken`, `ErrNotCancellable`
  - `type Request struct` (fields below), `func BuildPK(id string) string`, `func (r *Request) setDue(time.Time)`, `func (r *Request) clearDue()`, helpers `ts(time.Time) string`, `parseTime(string) time.Time`
  - `type Repository interface { Create; Get; GetOpen; Save; ListDue }` (signatures below), `func NewRepository(db *dynamodb.Client, tablePrefix string) Repository`

- [ ] **Step 1: Write the failing test** — `api/internal/domain/deletion/model_test.go`:

```go
package deletion

import (
	"testing"
	"time"
)

func TestStateOpen(t *testing.T) {
	for _, s := range []State{StateAwaitingConfirmation, StatePending, StateLocked, StatePurging} {
		if !s.Open() {
			t.Errorf("%s must hold the open-request marker", s)
		}
	}
	for _, s := range []State{StateCancelled, StateExpired} {
		if s.Open() {
			t.Errorf("%s must release the open-request marker", s)
		}
	}
}

func TestDueMarksCurrentState(t *testing.T) {
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	r := &Request{State: StatePending}
	r.setDue(at)
	if r.DueState != string(StatePending) || r.NextActionAt != "2026-10-07T12:00:00Z" {
		t.Fatalf("due = %q %q", r.DueState, r.NextActionAt)
	}
	r.clearDue()
	if r.DueState != "" || r.NextActionAt != "" {
		t.Fatal("clearDue must drop the request from the due index")
	}
	if !parseTime(ts(at)).Equal(at) {
		t.Fatal("ts/parseTime must round-trip")
	}
}
```

- [ ] **Step 2: Run, verify it fails**

Run: `cd api && go test ./internal/domain/deletion/ -count=1`
Expected: build FAIL (`undefined: State`, ...).

- [ ] **Step 3: Implement** — `api/internal/domain/deletion/model.go`:

```go
// Package deletion implements the LGPD account-deletion request lifecycle
// (docs/specs/2026-10-06-account-deletion-ctech-account.md). Every state change
// is a conditional write made before its side effects; a request whose side
// effects are not yet confirmed stays "due" for the worker to retry.
package deletion

import (
	"errors"
	"time"
)

const (
	// GracePeriod is fixed by decision D8 (overview spec §11).
	GracePeriod = 7 * 24 * time.Hour
	// ConfirmWindow is how long the e-mail confirmation link is valid.
	ConfirmWindow = 24 * time.Hour
	// ReminderLead is how long before the lock the reminder e-mail is sent.
	ReminderLead = 24 * time.Hour
	// ConfirmationPhrase must be typed verbatim by the user.
	ConfirmationPhrase = "EXCLUIR MINHA CONTA"
	ScopeAccount       = "account"
)

type State string

const (
	StateAwaitingConfirmation State = "awaiting_confirmation"
	StatePending              State = "pending_deletion"
	StateCancelled            State = "cancelled"
	StateExpired              State = "expired"
	StateLocked               State = "locked"
	StatePurging              State = "purging"
)

// Open reports whether the state holds the user's single open-request marker.
func (s State) Open() bool { return s != StateCancelled && s != StateExpired }

var (
	ErrNotFound       = errors.New("deletion: request not found")
	ErrOpenRequest    = errors.New("deletion: user already has an open request")
	ErrStateChanged   = errors.New("deletion: request state changed concurrently")
	ErrInvalidToken   = errors.New("deletion: link is invalid or expired")
	ErrNotCancellable = errors.New("deletion: request can no longer be cancelled")
)

// Request is one deletion request (pk REQ#{id}, sk META).
type Request struct {
	PK                string   `dynamodbav:"pk"`
	SK                string   `dynamodbav:"sk"`
	ID                string   `dynamodbav:"id"`
	UserID            string   `dynamodbav:"user_id"`
	Scope             string   `dynamodbav:"scope"`
	State             State    `dynamodbav:"request_state"`
	ConfirmTokenHash  string   `dynamodbav:"confirm_token_hash,omitempty"`
	CancelTokenHashes []string `dynamodbav:"cancel_token_hashes,omitempty"` // one per e-mail that carried a cancel link
	LockApplied       bool     `dynamodbav:"lock_applied"`
	Reminded          bool     `dynamodbav:"reminded"`
	RequestedAt       string   `dynamodbav:"requested_at"`
	ConfirmBy         string   `dynamodbav:"confirm_by"`
	ConfirmedAt       string   `dynamodbav:"confirmed_at,omitempty"`
	GraceUntil        string   `dynamodbav:"grace_until,omitempty"`
	CancelledAt       string   `dynamodbav:"cancelled_at,omitempty"`
	LockedAt          string   `dynamodbav:"locked_at,omitempty"`
	UpdatedAt         string   `dynamodbav:"updated_at"`
	// DueState/NextActionAt feed the sparse gsi_state_due index. Empty means
	// the worker has nothing to do for this request.
	DueState     string `dynamodbav:"due_state,omitempty"`
	NextActionAt string `dynamodbav:"next_action_at,omitempty"`
}

const metaSK = "META"

func BuildPK(id string) string { return "REQ#" + id }

func (r *Request) setDue(at time.Time) { r.DueState, r.NextActionAt = string(r.State), ts(at) }
func (r *Request) clearDue()           { r.DueState, r.NextActionAt = "", "" }

// ts formats in UTC RFC3339 so lexical order equals time order in the index.
func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}
```

`api/internal/domain/deletion/repository.go`:

```go
package deletion

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/account/api/internal/database"
	"gopkg.aoctech.app/api-commons/dynamo"
)

const (
	TableSuffix = "account_deletion_requests"
	DueIndex    = "gsi_state_due"
	markerSK    = "OPEN"
)

// Repository persists requests. Save is the only way to change a request and
// is conditional on the state it was read in.
type Repository interface {
	// Create stores r and the user's open marker; ErrOpenRequest if one exists.
	Create(ctx context.Context, r *Request) error
	Get(ctx context.Context, id string) (*Request, error)
	// GetOpen returns the user's open request, or ErrNotFound.
	GetOpen(ctx context.Context, userID string) (*Request, error)
	// Save writes r if the stored state is still from (ErrStateChanged
	// otherwise) and releases the open marker when r leaves the open states.
	Save(ctx context.Context, r *Request, from State) error
	// ListDue returns ids of requests in state whose next action is at or before now.
	ListDue(ctx context.Context, state State, now time.Time, limit int32) ([]string, error)
}

type marker struct {
	PK        string `dynamodbav:"pk"`
	SK        string `dynamodbav:"sk"`
	RequestID string `dynamodbav:"request_id"`
}

func markerPK(userID string) string { return "SUB#" + userID }

func key(pk, sk string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: pk},
		"sk": &types.AttributeValueMemberS{Value: sk},
	}
}

func str(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }

type dynamoRepository struct {
	db    *dynamodb.Client
	table string
}

func NewRepository(db *dynamodb.Client, tablePrefix string) Repository {
	return &dynamoRepository{db: db, table: database.TableName(tablePrefix, TableSuffix)}
}

func (r *dynamoRepository) Create(ctx context.Context, req *Request) error {
	item, err := attributevalue.MarshalMap(req)
	if err != nil {
		return err
	}
	mk, err := attributevalue.MarshalMap(marker{PK: markerPK(req.UserID), SK: markerSK, RequestID: req.ID})
	if err != nil {
		return err
	}
	_, err = r.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Put: &types.Put{TableName: aws.String(r.table), Item: mk, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
		{Put: &types.Put{TableName: aws.String(r.table), Item: item, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
	}})
	if dynamo.IsConditionFailed(err) {
		return ErrOpenRequest
	}
	if err != nil {
		return fmt.Errorf("deletion: create: %w", err)
	}
	return nil
}

func (r *dynamoRepository) Get(ctx context.Context, id string) (*Request, error) {
	out, err := r.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(r.table), Key: key(BuildPK(id), metaSK), ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("deletion: get %s: %w", id, err)
	}
	if len(out.Item) == 0 {
		return nil, ErrNotFound
	}
	var req Request
	if err := attributevalue.UnmarshalMap(out.Item, &req); err != nil {
		return nil, fmt.Errorf("deletion: decode %s: %w", id, err)
	}
	return &req, nil
}

func (r *dynamoRepository) GetOpen(ctx context.Context, userID string) (*Request, error) {
	out, err := r.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(r.table), Key: key(markerPK(userID), markerSK), ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("deletion: get open marker: %w", err)
	}
	if len(out.Item) == 0 {
		return nil, ErrNotFound
	}
	var mk marker
	if err := attributevalue.UnmarshalMap(out.Item, &mk); err != nil {
		return nil, err
	}
	return r.Get(ctx, mk.RequestID)
}

func (r *dynamoRepository) Save(ctx context.Context, req *Request, from State) error {
	item, err := attributevalue.MarshalMap(req)
	if err != nil {
		return err
	}
	items := []types.TransactWriteItem{{Put: &types.Put{
		TableName:                 aws.String(r.table),
		Item:                      item,
		ConditionExpression:       aws.String("request_state = :from"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":from": str(string(from))},
	}}}
	if from.Open() && !req.State.Open() {
		items = append(items, types.TransactWriteItem{Delete: &types.Delete{
			TableName:                 aws.String(r.table),
			Key:                       key(markerPK(req.UserID), markerSK),
			ConditionExpression:       aws.String("request_id = :id"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":id": str(req.ID)},
		}})
	}
	_, err = r.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items})
	if dynamo.IsConditionFailed(err) {
		return ErrStateChanged
	}
	if err != nil {
		return fmt.Errorf("deletion: save %s: %w", req.ID, err)
	}
	return nil
}

// ListDue reads the KEYS_ONLY sparse index; callers re-read each request
// with Get (the index is eventually consistent).
func (r *dynamoRepository) ListDue(ctx context.Context, state State, now time.Time, limit int32) ([]string, error) {
	out, err := r.db.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(r.table),
		IndexName:              aws.String(DueIndex),
		KeyConditionExpression: aws.String("due_state = :s AND next_action_at <= :now"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":s": str(string(state)), ":now": str(ts(now)),
		},
		Limit: aws.Int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("deletion: list due %s: %w", state, err)
	}
	ids := make([]string, 0, len(out.Items))
	for _, it := range out.Items {
		if pk, ok := it["pk"].(*types.AttributeValueMemberS); ok {
			ids = append(ids, strings.TrimPrefix(pk.Value, "REQ#"))
		}
	}
	return ids, nil
}
```

Ruling to ledger: the DynamoDB repository has no unit test (same as the other `dynamoRepository` types here); its contract is exercised through the in-memory doubles in Tasks 4 and 6 and end to end in dev.

- [ ] **Step 4: Run, verify it passes**

Run: `go vet ./internal/domain/deletion/ && go test ./internal/domain/deletion/ -count=1`
Expected: `ok`

- [ ] **Step 5: Commit**

```bash
git add internal/domain/deletion
git commit -m "feat(api): deletion request model and DynamoDB repository"
```

---

### Task 4: `deletion.Service` (request, confirm, cancel, worker steps)

**Files:**
- Create: `api/internal/domain/deletion/service.go`
- Test: `api/internal/domain/deletion/service_test.go`

**Interfaces:**
- Consumes: Task 3 model/repository; `user.User` (`Email`, `FirstName`); `crypto.GenerateMFAToken() (raw, hashHex string, err error)`, `crypto.HashToken(raw string) string`.
- Produces:
  - `type Users interface { GetByID(ctx context.Context, userID string) (*user.User, error) }`
  - `type Locker interface { Lock(ctx context.Context, r *Request) error; Unlock(ctx context.Context, r *Request) error; Erase(ctx context.Context, r *Request) error }` (all idempotent)
  - `type Mailer interface` with `SendDeletionConfirmEmail(ctx, to, firstName, requestID, token string) error`, `SendDeletionScheduledEmail(ctx, to, firstName, requestID, cancelToken string, graceUntil time.Time) error`, `SendDeletionReminderEmail(ctx, to, firstName, requestID, cancelToken string, graceUntil time.Time) error`, `SendDeletionCancelledEmail(ctx, to, firstName string) error`
  - `func NewService(repo Repository, users Users, locker Locker, mail Mailer) *Service`
  - `func (s *Service) Request(ctx, userID string) (*Request, error)`, `Confirm(ctx, id, token string) (*Request, error)`, `Cancel(ctx, id, token string) (*Request, error)`, `Status(ctx, userID string) (*Request, error)`, `ProcessDue(ctx context.Context)`

- [ ] **Step 1: Write the failing tests** — `api/internal/domain/deletion/service_test.go`:

```go
package deletion

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gopkg.aoctech.app/account/api/internal/domain/user"
)

// memRepo mirrors dynamoRepository's conditional semantics.
type memRepo struct {
	mu         sync.Mutex
	reqs       map[string]Request
	markers    map[string]string // userID -> open request id
	beforeSave func()            // runs once, before the next Save's condition check
}

func newMemRepo() *memRepo { return &memRepo{reqs: map[string]Request{}, markers: map[string]string{}} }

func clone(r Request) Request {
	r.CancelTokenHashes = append([]string(nil), r.CancelTokenHashes...)
	return r
}

func (m *memRepo) Create(_ context.Context, r *Request) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.markers[r.UserID]; ok {
		return ErrOpenRequest
	}
	m.markers[r.UserID] = r.ID
	m.reqs[r.ID] = clone(*r)
	return nil
}

func (m *memRepo) Get(_ context.Context, id string) (*Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.reqs[id]
	if !ok {
		return nil, ErrNotFound
	}
	c := clone(r)
	return &c, nil
}

func (m *memRepo) GetOpen(ctx context.Context, userID string) (*Request, error) {
	m.mu.Lock()
	id, ok := m.markers[userID]
	m.mu.Unlock()
	if !ok {
		return nil, ErrNotFound
	}
	return m.Get(ctx, id)
}

func (m *memRepo) Save(_ context.Context, r *Request, from State) error {
	if h := m.beforeSave; h != nil {
		m.beforeSave = nil
		h()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.reqs[r.ID]
	if !ok || cur.State != from {
		return ErrStateChanged
	}
	m.reqs[r.ID] = clone(*r)
	if from.Open() && !r.State.Open() && m.markers[r.UserID] == r.ID {
		delete(m.markers, r.UserID)
	}
	return nil
}

func (m *memRepo) ListDue(_ context.Context, st State, now time.Time, _ int32) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []string
	for id, r := range m.reqs {
		if r.DueState == string(st) && r.NextActionAt <= ts(now) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

type fakeUsers struct{ u *user.User }

func (f fakeUsers) GetByID(context.Context, string) (*user.User, error) { return f.u, nil }

type fakeLocker struct {
	locks, unlocks, erases int
	lockErr, unlockErr     error
	onLock                 func()
}

func (f *fakeLocker) Lock(context.Context, *Request) error {
	f.locks++
	if h := f.onLock; h != nil {
		f.onLock = nil
		h()
	}
	return f.lockErr
}
func (f *fakeLocker) Unlock(context.Context, *Request) error { f.unlocks++; return f.unlockErr }
func (f *fakeLocker) Erase(context.Context, *Request) error  { f.erases++; return nil }

type fakeMailer struct {
	confirmToken, cancelToken string
	reminders, cancelled      int
	confirmErr                error
}

func (f *fakeMailer) SendDeletionConfirmEmail(_ context.Context, _, _, _, token string) error {
	f.confirmToken = token
	return f.confirmErr
}
func (f *fakeMailer) SendDeletionScheduledEmail(_ context.Context, _, _, _, token string, _ time.Time) error {
	f.cancelToken = token
	return nil
}
func (f *fakeMailer) SendDeletionReminderEmail(_ context.Context, _, _, _, token string, _ time.Time) error {
	f.reminders++
	f.cancelToken = token
	return nil
}
func (f *fakeMailer) SendDeletionCancelledEmail(context.Context, string, string) error {
	f.cancelled++
	return nil
}

type fixture struct {
	svc    *Service
	repo   *memRepo
	locker *fakeLocker
	mail   *fakeMailer
	now    time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{repo: newMemRepo(), locker: &fakeLocker{}, mail: &fakeMailer{},
		now: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	f.svc = NewService(f.repo, fakeUsers{u: &user.User{Email: "ana@example.com", FirstName: "Ana"}}, f.locker, f.mail)
	f.svc.now = func() time.Time { return f.now }
	return f
}

var ctx = context.Background()

func (f *fixture) request(t *testing.T) *Request {
	t.Helper()
	r, err := f.svc.Request(ctx, "u1")
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	return r
}

func (f *fixture) confirm(t *testing.T) *Request {
	t.Helper()
	r := f.request(t)
	r, err := f.svc.Confirm(ctx, r.ID, f.mail.confirmToken)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	return r
}

func (f *fixture) stored(t *testing.T, id string) *Request {
	t.Helper()
	r, err := f.repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return r
}

func TestRequest_CreatesAwaitingAndEmailsConfirmLink(t *testing.T) {
	f := newFixture(t)
	r := f.request(t)
	if r.State != StateAwaitingConfirmation || f.mail.confirmToken == "" || r.ConfirmBy != ts(f.now.Add(ConfirmWindow)) {
		t.Fatalf("unexpected request %+v (token %q)", r, f.mail.confirmToken)
	}
	if f.locker.locks != 0 {
		t.Fatal("nothing may be locked before the e-mail confirmation")
	}
}

func TestRequest_ReplacesUnconfirmedRequest(t *testing.T) {
	f := newFixture(t)
	first := f.request(t)
	second := f.request(t)
	if second.ID == first.ID || f.stored(t, first.ID).State != StateExpired {
		t.Fatalf("first request must expire and be replaced: first=%+v", f.stored(t, first.ID))
	}
	if open, err := f.svc.Status(ctx, "u1"); err != nil || open.ID != second.ID {
		t.Fatalf("Status = %v, %v; want the second request", open, err)
	}
}

func TestRequest_RejectsWhilePending(t *testing.T) {
	f := newFixture(t)
	f.confirm(t)
	if _, err := f.svc.Request(ctx, "u1"); !errors.Is(err, ErrOpenRequest) {
		t.Fatalf("err = %v, want ErrOpenRequest", err)
	}
}

func TestRequest_MailFailureReleasesMarker(t *testing.T) {
	f := newFixture(t)
	f.mail.confirmErr = errors.New("ses down")
	if _, err := f.svc.Request(ctx, "u1"); err == nil {
		t.Fatal("a request whose confirmation e-mail failed must fail")
	}
	f.mail.confirmErr = nil
	f.request(t)
}

func TestConfirm_LocksAndSchedulesGrace(t *testing.T) {
	f := newFixture(t)
	r := f.confirm(t)
	s := f.stored(t, r.ID)
	if s.State != StatePending || !s.LockApplied || f.locker.locks != 1 {
		t.Fatalf("stored %+v locks %d", s, f.locker.locks)
	}
	if s.GraceUntil != ts(f.now.Add(GracePeriod)) || s.NextActionAt != ts(f.now.Add(GracePeriod-ReminderLead)) {
		t.Fatalf("grace %s next %s", s.GraceUntil, s.NextActionAt)
	}
	if f.mail.cancelToken == "" {
		t.Fatal("the scheduled e-mail must carry a cancel link")
	}
}

func TestConfirm_RejectsBadTokenExpiredAndSecondClick(t *testing.T) {
	f := newFixture(t)
	r := f.request(t)
	tok := f.mail.confirmToken
	if _, err := f.svc.Confirm(ctx, r.ID, "wrong"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("wrong token: %v", err)
	}
	f.now = f.now.Add(ConfirmWindow)
	if _, err := f.svc.Confirm(ctx, r.ID, tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired link: %v", err)
	}
	f.now = f.now.Add(-ConfirmWindow)
	if _, err := f.svc.Confirm(ctx, r.ID, tok); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if _, err := f.svc.Confirm(ctx, r.ID, tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("second click: %v", err)
	}
	if f.locker.locks != 1 {
		t.Fatalf("locks = %d, want 1", f.locker.locks)
	}
}

func TestConfirm_LockFailureRetriedByWorker(t *testing.T) {
	f := newFixture(t)
	f.locker.lockErr = errors.New("valkey down")
	r := f.confirm(t)
	if f.stored(t, r.ID).LockApplied {
		t.Fatal("a failed lock must not be recorded as applied")
	}
	f.locker.lockErr = nil
	f.svc.ProcessDue(ctx)
	if !f.stored(t, r.ID).LockApplied || f.locker.locks != 2 {
		t.Fatalf("worker must re-apply the lock: locks=%d", f.locker.locks)
	}
}

func TestCancel_UnlocksAndAllowsNewRequest(t *testing.T) {
	f := newFixture(t)
	r := f.confirm(t)
	c, err := f.svc.Cancel(ctx, r.ID, f.mail.cancelToken)
	if err != nil || c.State != StateCancelled {
		t.Fatalf("Cancel = %+v, %v", c, err)
	}
	if f.locker.unlocks != 1 || f.stored(t, r.ID).DueState != "" || f.mail.cancelled != 1 {
		t.Fatalf("unlocks=%d due=%q cancelled-mails=%d", f.locker.unlocks, f.stored(t, r.ID).DueState, f.mail.cancelled)
	}
	f.request(t)
}

func TestCancel_ReminderLinkWorks(t *testing.T) {
	f := newFixture(t)
	r := f.confirm(t)
	f.now = f.now.Add(GracePeriod - ReminderLead)
	f.svc.ProcessDue(ctx)
	if f.mail.reminders != 1 {
		t.Fatalf("reminders = %d", f.mail.reminders)
	}
	if _, err := f.svc.Cancel(ctx, r.ID, f.mail.cancelToken); err != nil {
		t.Fatalf("cancel with reminder link: %v", err)
	}
}

func TestCancel_RejectsWrongTokenAndAfterLock(t *testing.T) {
	f := newFixture(t)
	r := f.confirm(t)
	tok := f.mail.cancelToken
	if _, err := f.svc.Cancel(ctx, r.ID, "wrong"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("wrong token: %v", err)
	}
	f.now = f.now.Add(GracePeriod)
	f.svc.ProcessDue(ctx) // one tick: pending -> locked -> purging
	if f.stored(t, r.ID).State != StatePurging {
		t.Fatalf("state = %s, want purging after grace end", f.stored(t, r.ID).State)
	}
	if _, err := f.svc.Cancel(ctx, r.ID, tok); !errors.Is(err, ErrNotCancellable) {
		t.Fatalf("cancel after lock: %v", err)
	}
}

func TestProcessDue_FullTimeline(t *testing.T) {
	f := newFixture(t)
	r := f.confirm(t)
	f.now = f.now.Add(GracePeriod - ReminderLead)
	f.svc.ProcessDue(ctx)
	if s := f.stored(t, r.ID); !s.Reminded || s.State != StatePending {
		t.Fatalf("after reminder: %+v", s)
	}
	f.now = f.now.Add(ReminderLead)
	f.svc.ProcessDue(ctx) // states are walked in order, so pending -> locked -> purging in one tick
	if s := f.stored(t, r.ID); s.State != StatePurging || s.DueState != "" {
		t.Fatalf("after erase: %+v", s)
	}
	if f.locker.erases != 1 || f.locker.locks != 2 {
		t.Fatalf("erases=%d locks=%d, want 1 and 2 (lock re-asserted before erase)", f.locker.erases, f.locker.locks)
	}
}

func TestProcessDue_ExpiresUnconfirmed(t *testing.T) {
	f := newFixture(t)
	r := f.request(t)
	f.now = f.now.Add(ConfirmWindow)
	f.svc.ProcessDue(ctx)
	if f.stored(t, r.ID).State != StateExpired {
		t.Fatal("an unconfirmed request must expire")
	}
	f.request(t)
}

func TestProcessDue_CancelRetriesUnlock(t *testing.T) {
	f := newFixture(t)
	r := f.confirm(t)
	f.locker.unlockErr = errors.New("valkey down")
	if _, err := f.svc.Cancel(ctx, r.ID, f.mail.cancelToken); err != nil {
		t.Fatalf("Cancel must succeed even if the unlock is deferred: %v", err)
	}
	if f.stored(t, r.ID).DueState != string(StateCancelled) {
		t.Fatal("a failed unlock must stay due")
	}
	f.locker.unlockErr = nil
	f.svc.ProcessDue(ctx)
	if f.stored(t, r.ID).DueState != "" || f.locker.unlocks != 2 {
		t.Fatalf("worker must retry the unlock: unlocks=%d", f.locker.unlocks)
	}
}

func TestApplyLock_CancelRaceCompensates(t *testing.T) {
	f := newFixture(t)
	f.locker.lockErr = errors.New("valkey down")
	r := f.confirm(t)
	f.locker.lockErr = nil
	cancelToken := f.mail.cancelToken
	// While the worker's retried Lock is in flight, the user cancels.
	f.locker.onLock = func() {
		if _, err := f.svc.Cancel(ctx, r.ID, cancelToken); err != nil {
			t.Errorf("Cancel: %v", err)
		}
	}
	f.svc.ProcessDue(ctx)
	if f.stored(t, r.ID).State != StateCancelled || f.locker.unlocks != 2 {
		t.Fatalf("state=%s unlocks=%d, want cancelled and the stale lock undone", f.stored(t, r.ID).State, f.locker.unlocks)
	}
}
```

- [ ] **Step 2: Run, verify they fail**

Run: `cd api && go test ./internal/domain/deletion/ -count=1`
Expected: build FAIL (`undefined: NewService`, ...).

- [ ] **Step 3: Implement** — `api/internal/domain/deletion/service.go`:

```go
package deletion

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"gopkg.aoctech.app/account/api/internal/crypto"
	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/api-commons/observability"
)

type Users interface {
	GetByID(ctx context.Context, userID string) (*user.User, error)
}

// Locker applies the side effects of a request. Every method is idempotent:
// the worker re-runs them until the request records them as done.
type Locker interface {
	Lock(ctx context.Context, r *Request) error
	Unlock(ctx context.Context, r *Request) error
	Erase(ctx context.Context, r *Request) error
}

type Mailer interface {
	SendDeletionConfirmEmail(ctx context.Context, to, firstName, requestID, token string) error
	SendDeletionScheduledEmail(ctx context.Context, to, firstName, requestID, cancelToken string, graceUntil time.Time) error
	SendDeletionReminderEmail(ctx context.Context, to, firstName, requestID, cancelToken string, graceUntil time.Time) error
	SendDeletionCancelledEmail(ctx context.Context, to, firstName string) error
}

const dueBatch = 25

type Service struct {
	repo     Repository
	users    Users
	locker   Locker
	mail     Mailer
	now      func() time.Time
	newID    func() string
	newToken func() (raw, hash string, err error)
}

func NewService(repo Repository, users Users, locker Locker, mail Mailer) *Service {
	return &Service{
		repo: repo, users: users, locker: locker, mail: mail,
		now:      time.Now,
		newID:    func() string { return uuid.New().String() },
		newToken: crypto.GenerateMFAToken,
	}
}

// Request opens a deletion request awaiting e-mail confirmation. An earlier
// unconfirmed request is replaced, so a lost e-mail never blocks the user.
func (s *Service) Request(ctx context.Context, userID string) (*Request, error) {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	raw, hash, err := s.newToken()
	if err != nil {
		return nil, err
	}
	now := s.now()
	r := &Request{
		ID: s.newID(), UserID: userID, Scope: ScopeAccount, State: StateAwaitingConfirmation,
		ConfirmTokenHash: hash, RequestedAt: ts(now), ConfirmBy: ts(now.Add(ConfirmWindow)), UpdatedAt: ts(now),
	}
	r.PK, r.SK = BuildPK(r.ID), metaSK
	r.setDue(now.Add(ConfirmWindow))

	err = s.repo.Create(ctx, r)
	if errors.Is(err, ErrOpenRequest) {
		if err = s.expireUnconfirmed(ctx, userID); err == nil {
			err = s.repo.Create(ctx, r)
		}
	}
	if err != nil {
		return nil, err
	}
	if err := s.mail.SendDeletionConfirmEmail(ctx, u.Email, u.FirstName, r.ID, raw); err != nil {
		// Without the e-mail the request can never be confirmed: release it now.
		s.expire(r)
		if saveErr := s.repo.Save(ctx, r, StateAwaitingConfirmation); saveErr != nil {
			return nil, errors.Join(err, saveErr)
		}
		return nil, fmt.Errorf("deletion: sending confirmation e-mail: %w", err)
	}
	return r, nil
}

// expireUnconfirmed frees the open marker if it belongs to an unconfirmed
// request; any other open request keeps ErrOpenRequest.
func (s *Service) expireUnconfirmed(ctx context.Context, userID string) error {
	open, err := s.repo.GetOpen(ctx, userID)
	if err != nil {
		return err
	}
	if open.State != StateAwaitingConfirmation {
		return ErrOpenRequest
	}
	s.expire(open)
	return s.repo.Save(ctx, open, StateAwaitingConfirmation)
}

func (s *Service) expire(r *Request) {
	r.State, r.ConfirmTokenHash, r.UpdatedAt = StateExpired, "", ts(s.now())
	r.clearDue()
}

// Confirm turns an e-mail-confirmed request into a pending deletion and locks
// the account. A failed lock is retried by the worker (the request stays due).
func (s *Service) Confirm(ctx context.Context, id, token string) (*Request, error) {
	r, err := s.repo.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	if r.State != StateAwaitingConfirmation || !now.Before(parseTime(r.ConfirmBy)) || !tokenMatches(token, r.ConfirmTokenHash) {
		return nil, ErrInvalidToken
	}
	raw, hash, err := s.newToken()
	if err != nil {
		return nil, err
	}
	r.State, r.ConfirmTokenHash, r.CancelTokenHashes = StatePending, "", []string{hash}
	r.ConfirmedAt, r.GraceUntil, r.UpdatedAt = ts(now), ts(now.Add(GracePeriod)), ts(now)
	r.setDue(now) // lock not applied yet
	if err := s.repo.Save(ctx, r, StateAwaitingConfirmation); err != nil {
		if errors.Is(err, ErrStateChanged) {
			return nil, ErrInvalidToken
		}
		return nil, err
	}
	if err := s.applyLock(ctx, r); err != nil {
		observability.Error(ctx, "deletion: lock failed, worker will retry", err, "request_id", r.ID)
	}
	if u, err := s.users.GetByID(ctx, r.UserID); err == nil {
		if mErr := s.mail.SendDeletionScheduledEmail(ctx, u.Email, u.FirstName, r.ID, raw, parseTime(r.GraceUntil)); mErr != nil {
			observability.Error(ctx, "deletion: scheduled e-mail failed", mErr, "request_id", r.ID)
		}
	}
	return r, nil
}

// applyLock runs the lock, records it and schedules the reminder. If a cancel
// won the race while the lock was in flight, the lock is undone.
func (s *Service) applyLock(ctx context.Context, r *Request) error {
	if err := s.locker.Lock(ctx, r); err != nil {
		return err
	}
	r.LockApplied, r.UpdatedAt = true, ts(s.now())
	r.setDue(parseTime(r.GraceUntil).Add(-ReminderLead))
	err := s.repo.Save(ctx, r, StatePending)
	if errors.Is(err, ErrStateChanged) {
		if cur, getErr := s.repo.Get(ctx, r.ID); getErr == nil && cur.State == StateCancelled {
			return s.locker.Unlock(ctx, cur)
		}
	}
	return err
}

// Cancel ends a pending deletion. The state change wins first; the unlock is
// retried by the worker if it fails now.
func (s *Service) Cancel(ctx context.Context, id, token string) (*Request, error) {
	r, err := s.repo.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	if !anyTokenMatches(token, r.CancelTokenHashes) {
		return nil, ErrInvalidToken
	}
	if r.State != StatePending {
		return nil, ErrNotCancellable
	}
	now := s.now()
	r.State, r.CancelledAt, r.UpdatedAt = StateCancelled, ts(now), ts(now)
	r.setDue(now) // unlock not applied yet
	if err := s.repo.Save(ctx, r, StatePending); err != nil {
		if errors.Is(err, ErrStateChanged) {
			return nil, ErrNotCancellable
		}
		return nil, err
	}
	if err := s.finishCancel(ctx, r); err != nil {
		observability.Error(ctx, "deletion: unlock failed, worker will retry", err, "request_id", r.ID)
	}
	return r, nil
}

func (s *Service) finishCancel(ctx context.Context, r *Request) error {
	if err := s.locker.Unlock(ctx, r); err != nil {
		return err
	}
	r.UpdatedAt = ts(s.now())
	r.clearDue()
	if err := s.repo.Save(ctx, r, StateCancelled); err != nil {
		return err
	}
	if u, err := s.users.GetByID(ctx, r.UserID); err == nil {
		if mErr := s.mail.SendDeletionCancelledEmail(ctx, u.Email, u.FirstName); mErr != nil {
			observability.Error(ctx, "deletion: cancelled e-mail failed", mErr, "request_id", r.ID)
		}
	}
	return nil
}

// Status returns the user's open request, or ErrNotFound.
func (s *Service) Status(ctx context.Context, userID string) (*Request, error) {
	return s.repo.GetOpen(ctx, userID)
}

// ProcessDue advances every request whose next action is due. A failing
// request never blocks the others; it stays due and is retried next tick.
func (s *Service) ProcessDue(ctx context.Context) {
	now := s.now()
	for _, st := range []State{StateAwaitingConfirmation, StatePending, StateCancelled, StateLocked} {
		ids, err := s.repo.ListDue(ctx, st, now, dueBatch)
		if err != nil {
			observability.Error(ctx, "deletion: listing due requests", err, "state", string(st))
			continue
		}
		for _, id := range ids {
			r, err := s.repo.Get(ctx, id) // the index is eventually consistent
			if err == nil && r.DueState == string(st) && !now.Before(parseTime(r.NextActionAt)) {
				err = s.step(ctx, r, now)
			}
			if err != nil && !errors.Is(err, ErrStateChanged) {
				observability.Error(ctx, "deletion: step failed", err, "request_id", id, "state", string(st))
			}
		}
	}
}

func (s *Service) step(ctx context.Context, r *Request, now time.Time) error {
	switch r.State {
	case StateAwaitingConfirmation: // the confirmation window elapsed
		s.expire(r)
		return s.repo.Save(ctx, r, StateAwaitingConfirmation)
	case StateCancelled:
		return s.finishCancel(ctx, r)
	case StatePending:
		switch {
		case !r.LockApplied:
			return s.applyLock(ctx, r)
		case !r.Reminded && now.Before(parseTime(r.GraceUntil)):
			return s.remind(ctx, r, now)
		case !now.Before(parseTime(r.GraceUntil)):
			r.State, r.LockedAt, r.UpdatedAt = StateLocked, ts(now), ts(now)
			r.setDue(now)
			return s.repo.Save(ctx, r, StatePending)
		}
	case StateLocked:
		// Re-assert the lock: a cancel may have raced the end of grace.
		if err := s.locker.Lock(ctx, r); err != nil {
			return err
		}
		if err := s.locker.Erase(ctx, r); err != nil {
			return err
		}
		r.State, r.UpdatedAt = StatePurging, ts(now)
		r.clearDue() // Phase 2 adds the account's own purge from here
		return s.repo.Save(ctx, r, StateLocked)
	}
	return nil
}

func (s *Service) remind(ctx context.Context, r *Request, now time.Time) error {
	u, err := s.users.GetByID(ctx, r.UserID)
	if err != nil {
		return err
	}
	raw, hash, err := s.newToken()
	if err != nil {
		return err
	}
	r.CancelTokenHashes = append(r.CancelTokenHashes, hash)
	r.Reminded, r.UpdatedAt = true, ts(now)
	r.setDue(parseTime(r.GraceUntil))
	if err := s.repo.Save(ctx, r, StatePending); err != nil {
		return err
	}
	return s.mail.SendDeletionReminderEmail(ctx, u.Email, u.FirstName, r.ID, raw, parseTime(r.GraceUntil))
}

func tokenMatches(raw, hash string) bool {
	return hash != "" && subtle.ConstantTimeCompare([]byte(crypto.HashToken(raw)), []byte(hash)) == 1
}

func anyTokenMatches(raw string, hashes []string) bool {
	for _, h := range hashes {
		if tokenMatches(raw, h) {
			return true
		}
	}
	return false
}
```

If `go build` reports `github.com/google/uuid` as indirect-only, run `go mod tidy` (it becomes a direct dependency).

- [ ] **Step 4: Run, verify they pass**

Run: `go vet ./internal/domain/deletion/ && go test ./internal/domain/deletion/ -race -count=1`
Expected: `ok`

- [ ] **Step 5: Commit**

```bash
git add internal/domain/deletion go.mod go.sum
git commit -m "feat(api): deletion request lifecycle service and worker steps"
```

---

### Task 5: Side effects — `AccountLocker`, JWT revocation, SNS publisher, e-mails

**Files:**
- Create: `api/internal/domain/deletion/locker.go`, `api/internal/domain/deletion/locker_test.go`
- Create: `api/internal/erasurepub/sns.go`
- Modify: `api/internal/crypto/jwt.go` (`JWTService` field), create `api/internal/crypto/revocation.go`
- Modify: `api/internal/middleware/auth.go` (`extractAndVerify`)
- Modify: `api/internal/email/ses.go` (4 senders + 4 templates)
- Modify: `api/go.mod`, `api/go.sum` (`github.com/aws/aws-sdk-go-v2/service/sns`)

**Interfaces:**
- Consumes: Task 4 `Locker`, `Request`; Task 2 `user.Service.MarkPendingDeletion/ClearPendingDeletion`; `session.Service.RevokeAll(ctx, userID, exceptSessionID string) error`; `apikey.Service.List(ctx, userID) ([]*apikey.APIKey, error)`, `Revoke(ctx, userID, keyID string) error`; Task 0 `jwtverify.CheckRevoked`.
- Produces:
  - `deletion.UserBlocker`, `deletion.SessionRevoker`, `deletion.APIKeys`, `deletion.TokenRevoker`, `deletion.Publisher` interfaces
  - `func deletion.NewAccountLocker(users UserBlocker, sessions SessionRevoker, keys APIKeys, tokens TokenRevoker, pub Publisher, services []string) *AccountLocker`
  - `func deletion.NewJWTRevoker(c commoncache.Backend) *JWTRevoker`
  - `func erasurepub.New(client *sns.Client, topicARN string) *SNS` with `Publish(ctx, erasure.Message) error`
  - `func (s *crypto.JWTService) SetRevocation(c commoncache.Backend)`, `CheckRevoked(ctx context.Context, sub string, iat int64) error`
  - `(*email.Client)` methods matching Task 4 `Mailer`

- [ ] **Step 1: Write the failing test** — `api/internal/domain/deletion/locker_test.go`:

```go
package deletion

import (
	"context"
	"testing"
	"time"

	"gopkg.aoctech.app/account/api/internal/domain/apikey"
	"gopkg.aoctech.app/api-commons/erasure"
)

type fakeBlocker struct{ marked, cleared string }

func (f *fakeBlocker) MarkPendingDeletion(_ context.Context, _, rid string) error  { f.marked = rid; return nil }
func (f *fakeBlocker) ClearPendingDeletion(_ context.Context, _, rid string) error { f.cleared = rid; return nil }

type fakeSessions struct{ revokedAll int }

func (f *fakeSessions) RevokeAll(context.Context, string, string) error { f.revokedAll++; return nil }

type fakeKeys struct {
	keys    []*apikey.APIKey
	revoked []string
}

func (f *fakeKeys) List(context.Context, string) ([]*apikey.APIKey, error) { return f.keys, nil }
func (f *fakeKeys) Revoke(_ context.Context, _, id string) error {
	f.revoked = append(f.revoked, id)
	return nil
}

type fakeTokens struct{ revoked, unrevoked int }

func (f *fakeTokens) Revoke(context.Context, string, time.Time) error { f.revoked++; return nil }
func (f *fakeTokens) Unrevoke(context.Context, string) error          { f.unrevoked++; return nil }

type fakePub struct{ msgs []erasure.Message }

func (f *fakePub) Publish(_ context.Context, m erasure.Message) error {
	f.msgs = append(f.msgs, m)
	return nil
}

func lockedRequest() *Request {
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	return &Request{ID: "r1", UserID: "u1", ConfirmedAt: ts(at), CancelledAt: ts(at.Add(time.Hour)), LockedAt: ts(at.Add(GracePeriod))}
}

func TestAccountLocker_LockRevokesEverythingAndPublishes(t *testing.T) {
	users, sessions, tokens, pub := &fakeBlocker{}, &fakeSessions{}, &fakeTokens{}, &fakePub{}
	keys := &fakeKeys{keys: []*apikey.APIKey{{PK: apikey.BuildPK("u1"), SK: apikey.BuildSK("k1")}}}
	l := NewAccountLocker(users, sessions, keys, tokens, pub, []string{"wallet"})
	r := lockedRequest()
	if err := l.Lock(context.Background(), r); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if users.marked != "r1" || sessions.revokedAll != 1 || len(keys.revoked) != 1 || keys.revoked[0] != "k1" || tokens.revoked != 1 {
		t.Fatalf("marked=%q sessions=%d keys=%v tokens=%d", users.marked, sessions.revokedAll, keys.revoked, tokens.revoked)
	}
	if len(pub.msgs) != 1 || pub.msgs[0].Type != erasure.TypeLocked || !pub.msgs[0].IssuedAt.Equal(parseTime(r.ConfirmedAt)) || pub.msgs[0].Sub != "u1" {
		t.Fatalf("published %+v", pub.msgs)
	}
}

func TestAccountLocker_UnlockAndErasePublishWithTheirOwnTimestamps(t *testing.T) {
	users, tokens, pub := &fakeBlocker{}, &fakeTokens{}, &fakePub{}
	l := NewAccountLocker(users, &fakeSessions{}, &fakeKeys{}, tokens, pub, []string{"wallet"})
	r := lockedRequest()
	if err := l.Unlock(context.Background(), r); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if err := l.Erase(context.Background(), r); err != nil {
		t.Fatalf("Erase: %v", err)
	}
	if users.cleared != "r1" || tokens.unrevoked != 1 || len(pub.msgs) != 2 {
		t.Fatalf("cleared=%q unrevoked=%d msgs=%d", users.cleared, tokens.unrevoked, len(pub.msgs))
	}
	if pub.msgs[0].Type != erasure.TypeUnlocked || !pub.msgs[0].IssuedAt.Equal(parseTime(r.CancelledAt)) ||
		pub.msgs[1].Type != erasure.TypeErase || !pub.msgs[1].IssuedAt.Equal(parseTime(r.LockedAt)) {
		t.Fatalf("published %+v", pub.msgs)
	}
}

func TestAccountLocker_NoServicesPublishesNothing(t *testing.T) {
	pub := &fakePub{}
	l := NewAccountLocker(&fakeBlocker{}, &fakeSessions{}, &fakeKeys{}, &fakeTokens{}, pub, nil)
	if err := l.Lock(context.Background(), lockedRequest()); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if len(pub.msgs) != 0 {
		t.Fatal("with no participants configured nothing is published")
	}
}
```

- [ ] **Step 2: Run, verify it fails**

Run: `cd api && go test ./internal/domain/deletion/ -run AccountLocker -count=1`
Expected: build FAIL (`undefined: NewAccountLocker`).

- [ ] **Step 3: Implement the locker** — `api/internal/domain/deletion/locker.go`:

```go
package deletion

import (
	"context"
	"fmt"
	"time"

	"gopkg.aoctech.app/account/api/internal/domain/apikey"
	commoncache "gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/erasure"
	"gopkg.aoctech.app/api-commons/jwtverify"
)

type UserBlocker interface {
	MarkPendingDeletion(ctx context.Context, userID, requestID string) error
	ClearPendingDeletion(ctx context.Context, userID, requestID string) error
}

type SessionRevoker interface {
	RevokeAll(ctx context.Context, userID, exceptSessionID string) error
}

type APIKeys interface {
	List(ctx context.Context, userID string) ([]*apikey.APIKey, error)
	Revoke(ctx context.Context, userID, keyID string) error
}

type TokenRevoker interface {
	Revoke(ctx context.Context, sub string, cutoff time.Time) error
	Unrevoke(ctx context.Context, sub string) error
}

type Publisher interface {
	Publish(ctx context.Context, m erasure.Message) error
}

// AccountLocker is the production Locker: it blocks sign-in, kills every
// credential, cuts live access tokens and tells the participants.
type AccountLocker struct {
	users    UserBlocker
	sessions SessionRevoker
	keys     APIKeys
	tokens   TokenRevoker
	pub      Publisher
	services []string
}

func NewAccountLocker(users UserBlocker, sessions SessionRevoker, keys APIKeys, tokens TokenRevoker, pub Publisher, services []string) *AccountLocker {
	return &AccountLocker{users: users, sessions: sessions, keys: keys, tokens: tokens, pub: pub, services: services}
}

func (l *AccountLocker) Lock(ctx context.Context, r *Request) error {
	if err := l.users.MarkPendingDeletion(ctx, r.UserID, r.ID); err != nil {
		return fmt.Errorf("blocking sign-in: %w", err)
	}
	if err := l.sessions.RevokeAll(ctx, r.UserID, ""); err != nil {
		return fmt.Errorf("revoking sessions: %w", err)
	}
	keys, err := l.keys.List(ctx, r.UserID)
	if err != nil {
		return fmt.Errorf("listing api keys: %w", err)
	}
	for _, k := range keys {
		if err := l.keys.Revoke(ctx, r.UserID, k.ID()); err != nil {
			return fmt.Errorf("revoking api key: %w", err)
		}
	}
	if err := l.tokens.Revoke(ctx, r.UserID, time.Now()); err != nil {
		return fmt.Errorf("revoking access tokens: %w", err)
	}
	return l.publish(ctx, r, erasure.TypeLocked, parseTime(r.ConfirmedAt))
}

// Unlock re-enables sign-in. Revoked sessions and API keys stay revoked: the
// user signs in again.
func (l *AccountLocker) Unlock(ctx context.Context, r *Request) error {
	if err := l.users.ClearPendingDeletion(ctx, r.UserID, r.ID); err != nil {
		return fmt.Errorf("unblocking sign-in: %w", err)
	}
	if err := l.tokens.Unrevoke(ctx, r.UserID); err != nil {
		return fmt.Errorf("clearing token revocation: %w", err)
	}
	return l.publish(ctx, r, erasure.TypeUnlocked, parseTime(r.CancelledAt))
}

func (l *AccountLocker) Erase(ctx context.Context, r *Request) error {
	return l.publish(ctx, r, erasure.TypeErase, parseTime(r.LockedAt))
}

// publish uses the request's own timestamps as issued_at, so a retried
// publish is a duplicate the participants ignore, never a newer event.
func (l *AccountLocker) publish(ctx context.Context, r *Request, typ erasure.Type, at time.Time) error {
	if len(l.services) == 0 {
		return nil // no participant subscribed yet (Phase 1)
	}
	return l.pub.Publish(ctx, erasure.Message{
		Version: erasure.Version, Type: typ, RequestID: r.ID, Sub: r.UserID,
		Scope: erasure.ScopeAccount, Services: l.services, Attempt: 1, IssuedAt: at,
	})
}

// JWTRevoker writes the shared revocation list read by every service.
type JWTRevoker struct{ c commoncache.Backend }

func NewJWTRevoker(c commoncache.Backend) *JWTRevoker { return &JWTRevoker{c: c} }

func (r *JWTRevoker) Revoke(ctx context.Context, sub string, cutoff time.Time) error {
	return jwtverify.Revoke(ctx, r.c, sub, cutoff, jwtverify.RevocationTTL)
}

func (r *JWTRevoker) Unrevoke(ctx context.Context, sub string) error {
	return jwtverify.Unrevoke(ctx, r.c, sub)
}
```

- [ ] **Step 4: Run, verify the locker tests pass**

Run: `go test ./internal/domain/deletion/ -race -count=1`
Expected: `ok`

- [ ] **Step 5: SNS publisher** — `api/internal/erasurepub/sns.go`, then `go get github.com/aws/aws-sdk-go-v2/service/sns`:

```go
// Package erasurepub publishes account-deletion saga messages to the
// {env}-account-user-erasure SNS topic (saga protocol spec §3).
package erasurepub

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"

	"gopkg.aoctech.app/api-commons/erasure"
)

type SNS struct {
	client   *sns.Client
	topicARN string
}

func New(client *sns.Client, topicARN string) *SNS { return &SNS{client: client, topicARN: topicARN} }

func (p *SNS) Publish(ctx context.Context, m erasure.Message) error {
	body, err := erasure.Encode(m)
	if err != nil {
		return err
	}
	_, err = p.client.Publish(ctx, &sns.PublishInput{TopicArn: aws.String(p.topicARN), Message: aws.String(string(body))})
	return err
}
```

Ruling to ledger: no unit test for this thin SDK adapter (same as `internal/storage`); `erasure.Encode` is tested in ctech-go-common.

- [ ] **Step 6: Account API honours the revocation list (R3)**

`api/internal/crypto/jwt.go` — add to the `JWTService` struct:

```go
	revocation commoncache.Backend // nil disables the revocation check; see SetRevocation
```

(import `commoncache "gopkg.aoctech.app/api-commons/cache"`). Create `api/internal/crypto/revocation.go`:

```go
package crypto

import (
	"context"
	"errors"

	commoncache "gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/jwtverify"
	"gopkg.aoctech.app/api-commons/observability"
)

// SetRevocation enables the account-lock revocation check on this service's
// own API. Call once at startup, before serving.
func (s *JWTService) SetRevocation(c commoncache.Backend) { s.revocation = c }

// CheckRevoked fails open when Valkey is unreachable (logged), like
// jwtverify.VerifyClaims: authentication must not depend on the cache.
func (s *JWTService) CheckRevoked(ctx context.Context, sub string, iat int64) error {
	if s.revocation == nil {
		return nil
	}
	err := jwtverify.CheckRevoked(ctx, s.revocation, sub, iat)
	if errors.Is(err, jwtverify.ErrRevocationUnavailable) {
		observability.Warn(ctx, "account: revocation check skipped", err)
		return nil
	}
	return err
}
```

`api/internal/middleware/auth.go` — in `extractAndVerify`, right after the `verifyErr` block:

```go
	sub, _ := claims["sub"].(string)
	iat, _ := claims["iat"].(float64)
	if err := jwtSvc.CheckRevoked(c.Context(), sub, int64(iat)); err != nil {
		return id, apierror.InvalidToken("The access token is invalid or has expired.", c.Path())
	}
```

If `observability.Warn` has a different signature in the pinned api-commons, match the call style used in `internal/handler/social.go` (`observability.Warn(ctx, msg, err, ...)`).

- [ ] **Step 7: E-mails** — `api/internal/email/ses.go`, next to `SendPasswordResetEmail`:

```go
var brt = time.FixedZone("BRT", -3*60*60)

func (c *Client) deletionLink(page, requestID, token string) string {
	return c.baseURL + "/account-deletion/" + page + "?request=" + url.QueryEscape(requestID) + "&token=" + url.QueryEscape(token)
}

func (c *Client) SendDeletionConfirmEmail(ctx context.Context, to, firstName, requestID, token string) error {
	return c.send(ctx, to, "Confirme a exclusão da sua conta — ctech",
		deletionConfirmEmailHTML(firstName, c.deletionLink("confirm", requestID, token)))
}

func (c *Client) SendDeletionScheduledEmail(ctx context.Context, to, firstName, requestID, cancelToken string, graceUntil time.Time) error {
	return c.send(ctx, to, "Sua conta será excluída — ctech",
		deletionScheduledEmailHTML(firstName, c.deletionLink("cancel", requestID, cancelToken), graceUntil))
}

func (c *Client) SendDeletionReminderEmail(ctx context.Context, to, firstName, requestID, cancelToken string, graceUntil time.Time) error {
	return c.send(ctx, to, "Último aviso: sua conta será excluída amanhã — ctech",
		deletionReminderEmailHTML(firstName, c.deletionLink("cancel", requestID, cancelToken), graceUntil))
}

func (c *Client) SendDeletionCancelledEmail(ctx context.Context, to, firstName string) error {
	return c.send(ctx, to, "Exclusão da conta cancelada — ctech", deletionCancelledEmailHTML(firstName))
}
```

and next to `passwordResetEmailHTML`:

```go
func deletionConfirmEmailHTML(firstName, link string) string {
	body := `<p>Recebemos um pedido para <strong>excluir sua conta CTech</strong> e os seus dados em todos os produtos CTech. Confirme pelo botão abaixo. O link expira em 24 horas.</p>
  <p>Ao confirmar, sua conta é bloqueada na hora e excluída definitivamente em 7 dias. Até lá você pode cancelar pelo link que enviaremos.</p>
  ` + ctaButton("Confirmar exclusão", link)
	return emailLayout("Confirme a exclusão da conta", firstName, body,
		"Se não foi você, ignore este e-mail: nada acontece sem a confirmação. Recomendamos trocar sua senha.")
}

func deletionScheduledEmailHTML(firstName, cancelLink string, graceUntil time.Time) string {
	body := `<p>Sua conta CTech está bloqueada e será <strong>excluída definitivamente em ` +
		graceUntil.In(brt).Format("02/01/2006 às 15:04") + `</strong> (horário de Brasília).</p>
  <p>Depois disso a exclusão não pode ser desfeita. Para manter a conta, cancele antes desse prazo.</p>
  ` + ctaButton("Cancelar exclusão", cancelLink)
	return emailLayout("Exclusão agendada", firstName, body,
		"Se você não pediu a exclusão, cancele agora e troque sua senha.")
}

func deletionReminderEmailHTML(firstName, cancelLink string, graceUntil time.Time) string {
	body := `<p>Sua conta CTech será <strong>excluída definitivamente em ` +
		graceUntil.In(brt).Format("02/01/2006 às 15:04") + `</strong> (horário de Brasília). Esta é a última chance de cancelar.</p>
  ` + ctaButton("Cancelar exclusão", cancelLink)
	return emailLayout("Último aviso de exclusão", firstName, body,
		"Se você quer mesmo excluir a conta, não precisa fazer nada.")
}

func deletionCancelledEmailHTML(firstName string) string {
	body := `<p>A exclusão da sua conta CTech foi cancelada. Entre novamente para voltar a usar os produtos CTech; por segurança, todas as sessões e chaves de API anteriores foram encerradas.</p>`
	return emailLayout("Exclusão cancelada", firstName, body,
		"Se não foi você quem cancelou, fale com o suporte.")
}
```

(add `net/url` to the imports if missing).

- [ ] **Step 8: Build and run the whole suite**

Run: `go mod tidy && go vet ./... && go build ./... && go test ./... 2>&1 | tail -30`
Expected: every package `ok`.

- [ ] **Step 9: Commit**

```bash
git add internal/domain/deletion internal/erasurepub internal/crypto internal/middleware/auth.go internal/email/ses.go go.mod go.sum
git commit -m "feat(api): deletion side effects (account lock, jwt revocation, sns, e-mails)"
```

---

### Task 6: HTTP endpoints, scope, config, worker wiring

**Files:**
- Create: `api/internal/handler/deletion.go`, `api/internal/handler/deletion_test.go`
- Create: `api/internal/domain/deletion/worker.go`
- Modify: `api/internal/scopes/account_resource.go`, `api/internal/scopes/account-scope-manifest.json`
- Modify: `api/internal/domain/audit/events.go`
- Modify: `api/internal/config/config.go`
- Modify: `api/cmd/api/main.go`
- Modify: `api/internal/handler/testhelpers_test.go`

**Interfaces:**
- Consumes: Tasks 2–5.
- Produces:
  - `scopes.AccountDeletionWrite = "account:deletion:write"`
  - audit events `EventDeletionRequested = "account.deletion.requested"`, `EventDeletionConfirmed = "account.deletion.confirmed"`, `EventDeletionCancelled = "account.deletion.cancelled"`
  - `config.Config.AccountDeletionEnabled bool`, `ErasureTopicARN string`, `ErasureServices []string`
  - `func handler.NewDeletionHandler(svc *deletion.Service, users *user.Service, auditSvc *audit.Service) *DeletionHandler`; `Register(account, auth fiber.Router, requestLimiter fiber.Handler)`
  - `deletion.WorkerInterval`, `func deletion.RunWorker(ctx context.Context, svc *Service, tryLock func(context.Context) (bool, error), interval time.Duration)`
  - Routes: `POST /v1.0/account/deletion`, `GET /v1.0/account/deletion`, `POST /v1.0/auth/deletion/confirm`, `POST /v1.0/auth/deletion/cancel`

- [ ] **Step 1: Wire the handler into the test app** — `api/internal/handler/testhelpers_test.go`:

Add the in-memory repository (mirrors `dynamoRepository` semantics):

```go
type memDeletionRepo struct {
	mu      sync.Mutex
	reqs    map[string]deletion.Request
	markers map[string]string
}

func newMemDeletionRepo() *memDeletionRepo {
	return &memDeletionRepo{reqs: map[string]deletion.Request{}, markers: map[string]string{}}
}

func (m *memDeletionRepo) Create(_ context.Context, r *deletion.Request) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.markers[r.UserID]; ok {
		return deletion.ErrOpenRequest
	}
	m.markers[r.UserID] = r.ID
	m.reqs[r.ID] = *r
	return nil
}

func (m *memDeletionRepo) Get(_ context.Context, id string) (*deletion.Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.reqs[id]
	if !ok {
		return nil, deletion.ErrNotFound
	}
	r.CancelTokenHashes = append([]string(nil), r.CancelTokenHashes...)
	return &r, nil
}

func (m *memDeletionRepo) GetOpen(ctx context.Context, userID string) (*deletion.Request, error) {
	m.mu.Lock()
	id, ok := m.markers[userID]
	m.mu.Unlock()
	if !ok {
		return nil, deletion.ErrNotFound
	}
	return m.Get(ctx, id)
}

func (m *memDeletionRepo) Save(_ context.Context, r *deletion.Request, from deletion.State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.reqs[r.ID]
	if !ok || cur.State != from {
		return deletion.ErrStateChanged
	}
	m.reqs[r.ID] = *r
	if from.Open() && !r.State.Open() && m.markers[r.UserID] == r.ID {
		delete(m.markers, r.UserID)
	}
	return nil
}

func (m *memDeletionRepo) ListDue(_ context.Context, st deletion.State, now time.Time, _ int32) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []string
	for id, r := range m.reqs {
		if r.DueState == string(st) && r.NextActionAt <= now.UTC().Format(time.RFC3339) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

type fakeDeletionMailer struct{ confirmToken, cancelToken string }

func (f *fakeDeletionMailer) SendDeletionConfirmEmail(_ context.Context, _, _, _, token string) error {
	f.confirmToken = token
	return nil
}
func (f *fakeDeletionMailer) SendDeletionScheduledEmail(_ context.Context, _, _, _, token string, _ time.Time) error {
	f.cancelToken = token
	return nil
}
func (f *fakeDeletionMailer) SendDeletionReminderEmail(_ context.Context, _, _, _, token string, _ time.Time) error {
	f.cancelToken = token
	return nil
}
func (f *fakeDeletionMailer) SendDeletionCancelledEmail(context.Context, string, string) error { return nil }
```

Add fields to `testApp`: `deletionMail *fakeDeletionMailer`. In `newTestAppWithTOTP`, after `handler.NewTermsHandler(userSvc, auditSvc).Register(account)`:

```go
	revocation := commoncache.NewMemoryBackend(1024)
	jwtSvc.SetRevocation(revocation)
	deletionMail := &fakeDeletionMailer{}
	deletionSvc := deletion.NewService(newMemDeletionRepo(), userSvc,
		deletion.NewAccountLocker(userSvc, sessionSvc, apiKeySvc, deletion.NewJWTRevoker(revocation), nil, nil),
		deletionMail)
	handler.NewDeletionHandler(deletionSvc, userSvc, auditSvc).Register(account, v1.Group("/auth"),
		func(c fiber.Ctx) error { return c.Next() })
```

and `deletionMail: deletionMail,` in the returned `&testApp{...}`. Imports: `commoncache "gopkg.aoctech.app/api-commons/cache"`, `"gopkg.aoctech.app/account/api/internal/domain/deletion"`.

- [ ] **Step 2: Write the failing tests** — `api/internal/handler/deletion_test.go`:

```go
package handler_test

import (
	"net/http"
	"strings"
	"testing"
)

const deletionPhrase = "EXCLUIR MINHA CONTA"

func TestDeletion_FullFlow(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "bye@example.com", "Sup3rSecret!", "Ana")
	token := ta.issueToken(t, u.ID())

	resp := ta.doWithToken("POST", "/v1.0/account/deletion", map[string]string{"confirmation_phrase": deletionPhrase}, token)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("request: %d %s", resp.StatusCode, bodyString(resp))
	}
	var created struct {
		RequestID string `json:"request_id"`
	}
	readJSON(t, resp, &created)

	resp = ta.do("POST", "/v1.0/auth/deletion/confirm", map[string]string{"request_id": created.RequestID, "token": ta.deletionMail.confirmToken})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm: %d %s", resp.StatusCode, bodyString(resp))
	}

	// The pre-lock access token is dead on the account API (R3).
	if resp = ta.doWithToken("GET", "/v1.0/account/sessions", nil, token); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token after lock: %d, want 401", resp.StatusCode)
	}
	login := map[string]string{"email": "bye@example.com", "password": "Sup3rSecret!"}
	if resp = ta.do("POST", "/v1.0/auth/login", login); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("login while pending: %d, want 403", resp.StatusCode)
	}

	resp = ta.do("POST", "/v1.0/auth/deletion/cancel", map[string]string{"request_id": created.RequestID, "token": ta.deletionMail.cancelToken})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel: %d %s", resp.StatusCode, bodyString(resp))
	}
	if resp = ta.do("POST", "/v1.0/auth/login", login); resp.StatusCode != http.StatusOK {
		t.Fatalf("login after cancel: %d %s", resp.StatusCode, bodyString(resp))
	}
}

func TestDeletion_RequestValidation(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "check@example.com", "Sup3rSecret!", "Ana")

	fresh := ta.issueToken(t, u.ID())
	if resp := ta.doWithToken("POST", "/v1.0/account/deletion", map[string]string{"confirmation_phrase": "excluir"}, fresh); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong phrase: %d, want 400", resp.StatusCode)
	}

	stale := ta.issueStaleToken(t, u.ID()) // no recent MFA proof
	resp := ta.doWithToken("POST", "/v1.0/account/deletion", map[string]string{"confirmation_phrase": deletionPhrase}, stale)
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(bodyString(resp), "step-up-required") {
		t.Fatalf("no MFA, no password: %d %s", resp.StatusCode, bodyString(resp))
	}
	resp = ta.doWithToken("POST", "/v1.0/account/deletion", map[string]string{"confirmation_phrase": deletionPhrase, "password": "wrong"}, stale)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d, want 401", resp.StatusCode)
	}
	resp = ta.doWithToken("POST", "/v1.0/account/deletion", map[string]string{"confirmation_phrase": deletionPhrase, "password": "Sup3rSecret!"}, stale)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("right password: %d %s", resp.StatusCode, bodyString(resp))
	}
	if resp = ta.doWithToken("GET", "/v1.0/account/deletion", nil, fresh); resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestDeletion_BadLinks(t *testing.T) {
	ta := newTestApp(t)
	if resp := ta.do("POST", "/v1.0/auth/deletion/confirm", map[string]string{"request_id": "nope", "token": "nope"}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown confirm link: %d, want 401", resp.StatusCode)
	}
	if resp := ta.do("POST", "/v1.0/auth/deletion/cancel", map[string]string{"request_id": "nope", "token": "nope"}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown cancel link: %d, want 401", resp.StatusCode)
	}
}
```

Before running, check the status codes of `apierror.InvalidCredentials`, `apierror.InvalidToken` and `apierror.InvalidRequest` in `internal/apierror/problem.go` and align the expected codes above with them (they are the existing constructors; do not change them).

- [ ] **Step 3: Run, verify they fail**

Run: `cd api && go test ./internal/handler/ -run Deletion -count=1`
Expected: build FAIL (`undefined: handler.NewDeletionHandler`).

- [ ] **Step 4: Scope, audit events, config**

`api/internal/scopes/account_resource.go`: add `AccountDeletionWrite = "account:deletion:write"` to the const block and to `AccountUserScopes()` after `AccountTermsWrite` (or the last entry). `account-scope-manifest.json`: add to `scopes`:

```json
  {
   "name": "account:deletion:write",
   "descriptions": {
    "en": "Request and manage the deletion of the account.",
    "pt-BR": "Solicitar e gerenciar a exclusão da conta."
   },
   "visibility": "public",
   "status": "active"
  }
```

`api/internal/domain/audit/events.go` (own `const` group, with a one-line comment):

```go
// Account deletion (docs/specs/2026-10-06-account-deletion-ctech-account.md §10).
const (
	EventDeletionRequested = "account.deletion.requested"
	EventDeletionConfirmed = "account.deletion.confirmed"
	EventDeletionCancelled = "account.deletion.cancelled"
)
```

`api/internal/config/config.go` — fields in `Config`:

```go
	// Account deletion (docs/specs/2026-10-06-account-deletion-ctech-account.md).
	// Disabled unless ACCOUNT_DELETION_ENABLED=true; ERASURE_SERVICES lists the
	// participants that must ack (empty until Phase 3).
	AccountDeletionEnabled bool
	ErasureTopicARN        string
	ErasureServices        []string
```

In `Load`, before `return &Config{`:

```go
	var erasureServices []string
	if raw := os.Getenv("ERASURE_SERVICES"); raw != "" {
		for _, s := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				erasureServices = append(erasureServices, trimmed)
			}
		}
	}
```

and in the literal:

```go
		AccountDeletionEnabled: os.Getenv("ACCOUNT_DELETION_ENABLED") == "true",
		ErasureTopicARN:        os.Getenv("ACCOUNT_ERASURE_TOPIC_ARN"),
		ErasureServices:        erasureServices,
```

- [ ] **Step 5: Handler** — `api/internal/handler/deletion.go`:

```go
package handler

import (
	"errors"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/audit"
	"gopkg.aoctech.app/account/api/internal/domain/deletion"
	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/middleware"
	"gopkg.aoctech.app/account/api/internal/scopes"
)

type DeletionHandler struct {
	svc   *deletion.Service
	users *user.Service
	audit *audit.Service
}

func NewDeletionHandler(svc *deletion.Service, users *user.Service, auditSvc *audit.Service) *DeletionHandler {
	return &DeletionHandler{svc: svc, users: users, audit: auditSvc}
}

// Register mounts the signed-in routes on account and the e-mail-link routes
// on auth (public: the link token is the credential).
func (h *DeletionHandler) Register(account, auth fiber.Router, requestLimiter fiber.Handler) {
	account.Post("/deletion", middleware.RequireScope(scopes.AccountDeletionWrite), requestLimiter, h.request)
	account.Get("/deletion", middleware.RequireScope(scopes.AccountProfileRead), h.status)
	auth.Post("/deletion/confirm", h.confirm)
	auth.Post("/deletion/cancel", h.cancel)
}

type deletionRequestBody struct {
	ConfirmationPhrase string `json:"confirmation_phrase" validate:"required"`
	Password           string `json:"password"`
}

type deletionLinkBody struct {
	RequestID string `json:"request_id" validate:"required"`
	Token     string `json:"token"      validate:"required"`
}

func (h *DeletionHandler) request(c fiber.Ctx) error {
	var req deletionRequestBody
	if err := parseBody(c, &req); err != nil {
		return err
	}
	if req.ConfirmationPhrase != deletion.ConfirmationPhrase {
		return apierror.InvalidRequest(`confirmation_phrase must be exactly "`+deletion.ConfirmationPhrase+`".`, c.Path())
	}
	userID := middleware.GetUserID(c)
	if err := h.verifyIdentity(c, userID, req.Password); err != nil {
		return err
	}
	r, err := h.svc.Request(c.Context(), userID)
	if errors.Is(err, deletion.ErrOpenRequest) {
		return apierror.Conflict("A deletion request is already in progress for this account.", c.Path())
	}
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err)
	}
	recordAudit(c, h.audit, userID, audit.EventDeletionRequested, map[string]string{"request_id": r.ID})
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"request_id": r.ID, "state": r.State, "confirm_by": r.ConfirmBy})
}

// verifyIdentity (ruling R2): a recent MFA proof, or the account password.
// A password-less (Google-only) account relies on the e-mail confirmation.
func (h *DeletionHandler) verifyIdentity(c fiber.Ctx, userID, password string) error {
	if last := middleware.GetLastMFAAt(c); last != 0 && time.Since(time.Unix(last, 0)) <= middleware.StepUpMaxAge {
		return nil
	}
	hasPassword, err := h.users.HasPassword(c.Context(), userID)
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err)
	}
	if !hasPassword {
		return nil
	}
	if password == "" {
		return apierror.StepUpRequired(middleware.StepUpMaxAge, c.Path())
	}
	if err := h.users.CheckPassword(c.Context(), userID, password); err != nil {
		if errors.Is(err, user.ErrInvalidCredentials) {
			return apierror.InvalidCredentials(c.Path())
		}
		return apierror.ServerError(c.Path()).WithCause(err)
	}
	return nil
}

func (h *DeletionHandler) status(c fiber.Ctx) error {
	r, err := h.svc.Status(c.Context(), middleware.GetUserID(c))
	if errors.Is(err, deletion.ErrNotFound) {
		return apierror.NotFound("deletion request", c.Path())
	}
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err)
	}
	return c.JSON(fiber.Map{
		"request_id": r.ID, "state": r.State, "requested_at": r.RequestedAt,
		"confirm_by": r.ConfirmBy, "grace_until": r.GraceUntil,
	})
}

func (h *DeletionHandler) confirm(c fiber.Ctx) error {
	var req deletionLinkBody
	if err := parseBody(c, &req); err != nil {
		return err
	}
	r, err := h.svc.Confirm(c.Context(), req.RequestID, req.Token)
	if errors.Is(err, deletion.ErrInvalidToken) {
		return apierror.InvalidToken("This confirmation link is invalid or has expired.", c.Path())
	}
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err)
	}
	recordAudit(c, h.audit, r.UserID, audit.EventDeletionConfirmed, map[string]string{"request_id": r.ID})
	return c.JSON(fiber.Map{"request_id": r.ID, "state": r.State, "grace_until": r.GraceUntil})
}

func (h *DeletionHandler) cancel(c fiber.Ctx) error {
	var req deletionLinkBody
	if err := parseBody(c, &req); err != nil {
		return err
	}
	r, err := h.svc.Cancel(c.Context(), req.RequestID, req.Token)
	switch {
	case errors.Is(err, deletion.ErrInvalidToken):
		return apierror.InvalidToken("This cancel link is invalid or has expired.", c.Path())
	case errors.Is(err, deletion.ErrNotCancellable):
		return apierror.Conflict("This deletion can no longer be cancelled.", c.Path())
	case err != nil:
		return apierror.ServerError(c.Path()).WithCause(err)
	}
	recordAudit(c, h.audit, r.UserID, audit.EventDeletionCancelled, map[string]string{"request_id": r.ID})
	return c.JSON(fiber.Map{"request_id": r.ID, "state": r.State})
}
```

`api/internal/domain/deletion/worker.go`:

```go
package deletion

import (
	"context"
	"time"

	"gopkg.aoctech.app/api-commons/observability"
)

// WorkerInterval is how often the due index is polled.
const WorkerInterval = time.Minute

// RunWorker runs ProcessDue on whichever instance wins tryLock for the tick.
// ponytail: tick-scoped lock that just expires (TTL < interval), no renewal;
// fine while one tick finishes well inside a minute.
func RunWorker(ctx context.Context, svc *Service, tryLock func(context.Context) (bool, error), interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			ok, err := tryLock(ctx)
			if err != nil {
				observability.Error(ctx, "deletion worker: lock failed", err)
				continue
			}
			if ok {
				svc.ProcessDue(ctx)
			}
		}
	}
}
```

- [ ] **Step 6: Run the handler tests**

Run: `go test ./internal/handler/ -run Deletion -count=1 -v 2>&1 | tail -20`
Expected: `ok`. If a status-code assertion differs from an existing constructor's code, fix the test expectation (Step 2 note), never the constructor.

- [ ] **Step 7: Wire `main.go`**

With the other `v1.Use(...)` lines (before `authH.Register(v1)`):

```go
	v1.Use("/auth/deletion", pwResetLimiter, lockoutMiddleware)
```

After `supportH.RegisterAccount(account)`:

```go
	if cfg.AccountDeletionEnabled {
		if emailCli == nil {
			log.Fatal("ACCOUNT_DELETION_ENABLED requires the e-mail client (FROM_EMAIL / SES)")
		}
		var revocation commoncache.Backend = commoncache.NewMemoryBackend(10000)
		if cfg.ValkeyURL != "" {
			// DB 0 = the base URL, where every service reads revocations.
			if revocation, err = commoncache.NewRedisBackend(cfg.ValkeyURL); err != nil {
				log.Fatalf("connecting revocation backend: %v", err)
			}
		}
		jwtSvc.SetRevocation(revocation)
		var publisher deletion.Publisher
		if len(cfg.ErasureServices) > 0 {
			if cfg.ErasureTopicARN == "" {
				log.Fatal("ERASURE_SERVICES is set but ACCOUNT_ERASURE_TOPIC_ARN is empty")
			}
			awsCfg, awsErr := awsconfig.Load(ctx, cfg.AWSRegion)
			if awsErr != nil {
				log.Fatalf("loading AWS config for SNS: %v", awsErr)
			}
			publisher = erasurepub.New(sns.NewFromConfig(awsCfg), cfg.ErasureTopicARN)
		}
		deletionSvc := deletion.NewService(
			deletion.NewRepository(db, cfg.TablePrefix), userSvc,
			deletion.NewAccountLocker(userSvc, sessionSvc, apiKeySvc, deletion.NewJWTRevoker(revocation), publisher, cfg.ErasureServices),
			emailCli,
		)
		deletionLimiter := middleware.RateLimit(middleware.RateLimitConfig{
			Cache: valkeyClient, Prefix: "deletion_req", Max: 3, Window: 30 * 24 * time.Hour,
			KeyFunc: middleware.GetUserID, CountOnlyFailures: false, FailClosed: true,
		})
		handler.NewDeletionHandler(deletionSvc, userSvc, auditSvc).Register(account, v1.Group("/auth"), deletionLimiter)
		workerLockKey := "deletion_worker_lock:" + cfg.Environment
		go deletion.RunWorker(ctx, deletionSvc, func(ctx context.Context) (bool, error) {
			if !valkeyClient.Enabled() {
				return true, nil // dev: single instance
			}
			return valkeyClient.SetNX(ctx, workerLockKey, "1", deletion.WorkerInterval-5*time.Second)
		}, deletion.WorkerInterval)
	}
```

Imports: `commoncache "gopkg.aoctech.app/api-commons/cache"`, `"github.com/aws/aws-sdk-go-v2/service/sns"`, `"gopkg.aoctech.app/account/api/internal/domain/deletion"`, `"gopkg.aoctech.app/account/api/internal/erasurepub"`. If `RateLimitConfig.Max` is not an `int`, match the type of `middleware.FailedLoginMax`.

- [ ] **Step 8: Full suite**

Run: `go vet ./... && go build ./... && go test ./... 2>&1 | tail -30`
Expected: every package `ok`.

- [ ] **Step 9: Commit**

```bash
git add cmd/api/main.go internal/handler/deletion.go internal/handler/deletion_test.go internal/handler/testhelpers_test.go internal/domain/deletion/worker.go internal/scopes internal/domain/audit/events.go internal/config/config.go
git commit -m "feat(api): account deletion endpoints, worker and feature flag"
```

---

### Task 7: Infrastructure (CDK)

**Files:**
- Modify: `cdk/lib/dynamodb-stack.ts`, `cdk/lib/iam-stack.ts`, `cdk/lib/api-stack.ts`

**Interfaces:**
- Produces: table `{env}_account_deletion_requests` (+ `gsi_state_due`), SNS topic `{env}-account-user-erasure`, SSM `/ctech/{env}/account/erasure-topic-arn`, env `ACCOUNT_ERASURE_TOPIC_ARN` on the API instances.

- [ ] **Step 1: Table** — `cdk/lib/dynamodb-stack.ts`, after the audit table:

```ts
    // LGPD deletion requests (docs/specs/2026-10-06-account-deletion-ctech-account.md).
    // pk=REQ#{id} sk=META, plus one SUB#{user_id}/OPEN marker per open request.
    // gsi_state_due is sparse: only requests with a pending next action carry due_state.
    const deletionRequestsTable = new dynamodb.TableV2(this, 'DeletionRequestsTableV2', {
      tableName: `${environment}_account_deletion_requests`,
      partitionKey: {name: 'pk', type: dynamodb.AttributeType.STRING},
      sortKey: {name: 'sk', type: dynamodb.AttributeType.STRING},
      billing: dynamodb.Billing.onDemand({
        maxReadRequestUnits: 1000,
        maxWriteRequestUnits: 1000,
      }),
      pointInTimeRecoverySpecification: {
        pointInTimeRecoveryEnabled: pitr,
      },
      removalPolicy,
      globalSecondaryIndexes: [
        {
          indexName: 'gsi_state_due',
          partitionKey: {name: 'due_state', type: dynamodb.AttributeType.STRING},
          sortKey: {name: 'next_action_at', type: dynamodb.AttributeType.STRING},
          projectionType: dynamodb.ProjectionType.KEYS_ONLY,
          maxReadRequestUnits: 1000,
          maxWriteRequestUnits: 1000,
        },
      ],
    });
    this.tables.set('account_deletion_requests', deletionRequestsTable);
```

(The IAM policy is built from `dynamoDBTables`, so the table and its index are covered automatically.)

- [ ] **Step 2: Topic + publish grant + SSM** — `cdk/lib/iam-stack.ts`, after the existing `appRole.addToPolicy(...)` calls (add `import * as sns from 'aws-cdk-lib/aws-sns';` and `import * as ssm from 'aws-cdk-lib/aws-ssm';` if missing):

```ts
    // Account-deletion saga fan-out (docs/specs/2026-10-06-account-deletion-saga-protocol.md §3).
    // Participants subscribe their own SQS queues; the ARN is published in SSM for them.
    const erasureTopic = new sns.Topic(this, 'UserErasureTopic', {
      topicName: `${environment}-account-user-erasure`,
    });
    erasureTopic.grantPublish(appRole);
    new ssm.StringParameter(this, 'UserErasureTopicArn', {
      parameterName: `/ctech/${environment}/account/erasure-topic-arn`,
      stringValue: erasureTopic.topicArn,
    });
```

- [ ] **Step 3: Env var** — `cdk/lib/api-stack.ts`, in the `/etc/app-static.env` block after `KYC_DOCUMENTS_BUCKET=...`:

```ts
      `ACCOUNT_ERASURE_TOPIC_ARN=arn:aws:sns:${this.region}:${this.account}:${environment}-account-user-erasure`,
```

(`ACCOUNT_DELETION_ENABLED` is deliberately not set: Phase 1 ships dark.)

- [ ] **Step 4: Type-check**

Run: `cd cdk && npx tsc --noEmit`
Expected: no output, exit 0.

- [ ] **Step 5: Commit**

```bash
git add cdk/lib/dynamodb-stack.ts cdk/lib/iam-stack.ts cdk/lib/api-stack.ts
git commit -m "feat(cdk): deletion requests table and user-erasure SNS topic"
```

Deploying is a separate, user-approved step (`--profile ctech`).

---

### Task 8: Documentation

**Files:**
- Modify: `README.md` (endpoints + config vars), `api/ENDPOINTS.md`, `docs/resource-server-scope-registry.md`, `PLAN.md`
- Modify: `docs/specs/2026-10-06-account-deletion-ctech-account.md`, `docs/specs/2026-10-06-account-deletion-overview.md`

- [ ] **Step 1: `api/ENDPOINTS.md`** — add a "Account deletion" section listing the four routes from Task 6 with auth (`account:deletion:write` + R2 identity rule / `account:profile:read` / public link token), request bodies (`confirmation_phrase`, `password`; `request_id`, `token`), success codes (202 / 200 / 200 / 200) and problem types (`step-up-required`, `invalid-credentials`, `conflict`, `invalid-token`, `account-pending-deletion` on login).
- [ ] **Step 2: `README.md`** — config table: `ACCOUNT_DELETION_ENABLED` (default off), `ACCOUNT_ERASURE_TOPIC_ARN`, `ERASURE_SERVICES` (csv, empty until Phase 3); endpoint list: the four routes; one line that sign-in returns `account-pending-deletion` while a deletion is pending.
- [ ] **Step 3: `docs/resource-server-scope-registry.md`** — add `account:deletion:write` with its en/pt-BR descriptions.
- [ ] **Step 4: `PLAN.md`** — new section "Account deletion (LGPD)" with Phase 1 items checked and Phases 2–4 unchecked, linking this plan.
- [ ] **Step 5: Specs** — in `2026-10-06-account-deletion-ctech-account.md`: §3 replace the restricted-session paragraph with R1 (cancel by e-mail link only, sent at confirmation and in the reminder; support cancel in Phase 3); §5 item 1 with R2; §6 add that the account's own API honours the revocation list (R3); §4 replace `/deletion/confirm?token=` and `/deletion/cancel` with the `POST /v1.0/auth/deletion/{confirm,cancel}` bodies. In the overview §3, replace "Only the owner can log in to a restricted cancel deletion screen" with "The owner cancels through the link in the e-mails".
- [ ] **Step 6: Commit**

```bash
git add README.md api/ENDPOINTS.md docs/resource-server-scope-registry.md PLAN.md docs/specs/2026-10-06-account-deletion-ctech-account.md docs/specs/2026-10-06-account-deletion-overview.md
git commit -m "docs: account deletion phase 1 endpoints, config and spec rulings"
```

---

## Cross-project impact

- **ctech-go-common:** Task 0 adds `jwtverify.CheckRevoked` (v1.13.1, additive).
- **ui:** none yet. The e-mail links point to `/account-deletion/confirm` and `/account-deletion/cancel`, which Phase 4 builds. Until then, test in dev by calling the API directly.
- **cdk:** new table, topic and SSM parameter (Task 7).
- **wallet, dfe, billing, poker:** none until Phase 3. `ERASURE_SERVICES` stays empty, so nothing is published.
- **Every consumer of account tokens:** an account in deletion stops getting new tokens. Existing tokens die immediately on the account API (R3), and on the other services once they adopt `WithRevocation`.
