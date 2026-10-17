# Service unlink (`scope: service`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A person leaves one CTech product (wallet, billing, dfe, poker) without closing their account, and can come back to it later as a new user.

**Architecture:** The existing deletion request/worker gains `Scope` and `Service`. A service-scoped request locks and erases at one participant only (no global sign-in block, no `jwtverify` revocation entry, no workspaces), then records the product on the user (`unlinked_services`). The next code exchange for that product's login client publishes a new `user.relinked` message, which the shared `erasure.Consumer` turns into `Store.Clear`. Participants change by a module bump only.

**Tech Stack:** Go 1.26 (Fiber v3, DynamoDB, SNS), `gopkg.aoctech.app/api-commons/erasure`, Next.js 16 + vitest for the UI.

**Spec:** [`docs/specs/2026-10-17-service-unlink.md`](../specs/2026-10-17-service-unlink.md) (decisions U1–U10 are referenced below). Parent: `docs/specs/2026-10-06-account-deletion-*.md`.

## Global Constraints

- Branch per repo `feat/service-unlink`, Conventional Commits, **no trailer** (`Co-Authored-By` or any attribution), PR to `main`, never a direct push.
- ctech-account: `api/CLAUDE.md` rules apply (RFC 7807 via `apierror`, constants for every string, conditional writes for every read-modify-write, no production scans, `handler → service → repository`). Every code change updates `README.md` / `PLAN.md` / `ui/FRONTEND.md` in the same commit set.
- Grace period 7 days, confirmation window 24 h (existing constants `GracePeriod`, `ConfirmWindow`).
- Message contract: `Scope: service` messages carry `Services: [S]` (exactly one) and empty `Workspaces`/`Organizations`. Message `Version` stays `1`.
- Typed phrase for this scope: `SAIR DO ` + product name in capitals (`SAIR DO POKER`).
- Cross-project impact stated in every PR: go-common (new tag), participants (bump), ui, cdk (none).
- **Rollout gate:** no participant's `login_clients` is set in any environment until every participant has bumped `api-commons` (the old consumer sends an unknown type to the DLQ). `wallet` never gets `login_clients` until the Asaas ruling (spec § 13).
- Privacy policy 3.4 is approved by the owner before the UI ships (Task 8, Step 6 stops and asks).

## Review Focus

1. **A service request must not sign the person out of anything else.** Test: Task 4 `TestServiceLockLeavesTheAccountAndOtherProductsAlone` (no `MarkPendingDeletion`, no `RevokeAll`, no `jwtverify` entry, refresh tokens of other clients intact).
2. **Cancel during grace restores the product**, including refresh tokens that were revoked at lock (the person signs in again, nothing else). Test: Task 5 `TestCancelingAServiceRequestUnlocksOnlyThatService`.
3. **A crash between publishing `user.relinked` and clearing `unlinked_services`.** The next sign-in republishes; `Clear` on a non-erased record is a no-op. Tests: Task 6 `TestRelinkPublishesBeforeItClears`, Task 1 `TestRelinkedOnAnActiveSubIsANoOp`.
4. **A late duplicate `user.erase` after the person came back** must not wipe their new data. Test: Task 1 `TestStaleEraseAfterRelinkIsAckedNotPurged`.
5. **The relink publish failing must not mint a token and must not lose the unlink.** Test: Task 6 `TestRelinkFailureReturns503AndMintsNothing`.
6. **Two requests at once** (service then account, or two services). Test: Task 3 `TestAnOpenServiceRequestBlocksANewRequest`.

---

### Task 1: `user.relinked` in `api-commons/erasure` (repo `ctech-go-common`)

**Files:**
- Modify: `erasure/message.go` (`TypeRelinked`, validation), `erasure/consumer.go` (`handle`), `erasure/message_test.go`, `erasure/consumer_test.go`, `README.md` (or `erasure/README.md`, whichever documents the contract), `CLAUDE.md` consumer list if it names the version.

**Interfaces:**
- Produces: `erasure.TypeRelinked Type = "user.relinked"`. `Decode` returns an error for it unless `Scope == ScopeService`, `len(Services) == 1`, and `Workspaces`, `Organizations` empty. `Consumer.handle` on it: `store.Clear(ctx, m.Sub)`, no ack, return nil.
- Consumes: `(*Store).Clear(ctx, sub) error` (exists).

- [ ] **Step 1: Failing tests** in `message_test.go`: `TestRelinkedNeedsServiceScopeAndOneService` (account scope → error; two services → error; with workspaces → error; valid → ok). In `consumer_test.go`, using the file's fake SQS/DynamoDB: `TestRelinkedClearsAnErasedSub` (erased record becomes active, no ack posted), `TestRelinkedOnAnActiveSubIsANoOp` (record unchanged, no error), `TestRelinkedForAnotherServiceIsIgnored` (`Targets` false), `TestStaleEraseAfterRelinkIsAckedNotPurged` (erase → relink → redelivered erase of the same request: `PurgeFunc` not called, ack `done` sent).
- [ ] **Step 2:** `go test ./erasure/ -run 'Relink'` → FAIL (`undefined: TypeRelinked`).
- [ ] **Step 3:** Implement the constant, the validation in the same place the other types are validated, and the `handle` branch before the `TypeErase` code path.
- [ ] **Step 4:** `go test ./... -race -count=1` → PASS.
- [ ] **Step 5:** Document the type and the rollout note ("bump before account enables relink") in the contract doc. Commit `feat(erasure): user.relinked clears a tombstone when a person comes back to a service`. Tag a minor version after merge.

---

### Task 2: Model, config and participants (repo `ctech-account`, `api/`)

**Files:**
- Modify: `go.mod`/`go.sum` (api-commons to the Task 1 tag), `internal/config/config.go` (`ErasureParticipant.LoginClients`), `internal/domain/deletion/model.go`, `internal/domain/user/model.go`, `internal/domain/user/repository.go` and `service.go`, `README.md`
- Test: `internal/config/config_test.go`, `internal/domain/deletion/model_test.go`, `internal/domain/user/service_test.go` (+ the in-memory repo in `testhelpers_test.go`)

**Interfaces:**
- Produces:
  - `config.ErasureParticipant.LoginClients []string` (`json:"login_clients"`), optional.
  - `config.Config.LeavableService(name string) (ErasureParticipant, bool)`: true only when the participant exists **and** has at least one login client.
  - `deletion.ScopeService = "service"`; `Request.Service string` (`dynamodbav:"service,omitempty"`).
  - `user.User.UnlinkedServices []string` (`dynamodbav:"unlinked_services,omitempty"`).
  - `(*user.Service).AddUnlinkedService(ctx, userID, service string) error` and `RemoveUnlinkedService(ctx, userID, service string) error`: conditional, idempotent set add/remove.

- [ ] **Step 1: Failing tests:** `TestParticipantsParseLoginClients`, `TestLeavableServiceNeedsALoginClient` (config); `TestAddUnlinkedServiceIsIdempotent`, `TestRemoveUnlinkedServiceKeepsTheOthers` (user service).
- [ ] **Step 2:** run them → FAIL (undefined).
- [ ] **Step 3:** Implement. DynamoDB: `ADD unlinked_services :s` / `DELETE unlinked_services :s` on a string set through `database.ConditionalUpdate` (condition `attribute_exists(pk)`); the in-memory repo mirrors set semantics. Bump api-commons.
- [ ] **Step 4:** `go test ./internal/config/ ./internal/domain/user/ ./internal/domain/deletion/ -count=1` → PASS.
- [ ] **Step 5:** README: `login_clients` in the `ERASURE_PARTICIPANTS` row. Commit `feat(deletion): service scope model, login clients per participant`.

---

### Task 3: Requesting a service unlink (`deletion.Service`)

**Files:**
- Modify: `internal/domain/deletion/service.go` (`Request`), `internal/domain/deletion/service_test.go`

**Interfaces:**
- Consumes: Task 2 types; `Eligibility.Check`.
- Produces:
  - `(*Service).Request(ctx, userID string, scope, service string) (*Request, error)` replaces `Request(ctx, userID)`; every existing caller passes `ScopeAccount, ""`.
  - `Participants` seam: `WithLeavable(func(name string) (ServiceTarget, bool))` where `ServiceTarget{Service string; LoginClients []string}`.
  - `deletion.ErrUnknownService`.
  - For `scope == service` the assessment is `eligibility.CheckOne(service)` with no workspaces and **no** `Ownership.Assess` (U1, U2). `Eligibility` gains `CheckService(ctx, userID, service string) ([]erasure.Blocker, error)` (reuses `checkOne`).

- [ ] **Step 1: Failing tests:** `TestRequestForAServiceAsksOnlyThatParticipant` (fake eligibility records calls: one participant, no ownership call, no workspaces); `TestRequestForAnUnknownServiceIsRefused` (`ErrUnknownService`); `TestRequestForAServiceReportsItsBlockers` (→ `*BlockedError`); `TestAnOpenServiceRequestBlocksANewRequest` (`ErrOpenRequest` for both an account and another service request); `TestAccountScopeIsUnchanged` (existing behavior pinned through the new signature).
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3:** Implement. The request stores `Scope` and `Service`; the e-mail confirmation call takes the product name (Task 5 adds the template text; here pass it through `Mailer.SendDeletionConfirmEmail(ctx, to, firstName, requestID, token, product string)`, `product == ""` for the account).
- [ ] **Step 4:** `go test ./internal/domain/deletion/ ./internal/erasurepub/ -count=1` → PASS.
- [ ] **Step 5:** Commit `feat(deletion): request a service unlink`.

---

### Task 4: Locking and erasing one service (`AccountLocker` → scope-aware)

**Files:**
- Modify: `internal/domain/deletion/locker.go`, `internal/domain/session/service.go` + `repository.go` (`RevokeClients`), `internal/domain/apikey` (revoke by service scope), `internal/domain/deletion/locker_test.go`
- Test: same plus `session/service_test.go`

**Interfaces:**
- Consumes: `ServiceTarget` (Task 3), `scopes` registry (which scopes belong to which service: the existing `scopes.PublicServiceScopes`/manifest per audience).
- Produces:
  - `(*session.Service).RevokeClients(ctx, userID string, clientIDs []string) error`: deletes the user's refresh tokens whose client is in the set (lists sessions, then `ListRefreshTokensBySession`, then `DeleteRefreshToken`); sessions themselves stay.
  - `SessionRevoker` interface gains `RevokeClients`.
  - `APIKeys` interface gains `RevokeForService(ctx, userID, service string) error` (revokes keys holding any scope of that service).
  - `Locker.Lock/Unlock/Erase` branch on `r.Scope`; for `service`: `Lock` = `RevokeClients` + `RevokeForService` + publish `TypeLocked` to `[r.Service]`; `Unlock` = publish `TypeUnlocked` to `[r.Service]`; `Erase` = publish `TypeErase` to `[r.Service]` with empty workspaces. `publish` sets `Scope: erasure.ScopeService` for these.

- [ ] **Step 1: Failing tests:** `TestServiceLockLeavesTheAccountAndOtherProductsAlone` (Review Focus 1: fake `users`/`tokens` fail the test if `MarkPendingDeletion` or `Revoke` is called; `RevokeAll` not called; other clients' refresh tokens still present; message is `locked`, `Scope service`, `Services [S]`); `TestServiceEraseCarriesNoWorkspaces`; `TestRevokeClientsDeletesOnlyThoseClients` (session); `TestRevokeForServiceMatchesByScope` (apikey).
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3:** Implement; reuse `publish` with a `scope` parameter instead of a second publisher.
- [ ] **Step 4:** `go test ./internal/domain/... -count=1` → PASS.
- [ ] **Step 5:** Commit `feat(deletion): lock and erase one service without touching the account`.

---

### Task 5: Worker transitions, cancel, and the `purged` step

**Files:**
- Modify: `internal/domain/deletion/service.go` (`step`, `applyLock`, `Cancel`, `finishCancel`), new `internal/domain/deletion/unlink.go` (`ServiceUnlinker`), `internal/email` (templates), `cmd/api/main.go` (wiring), `internal/domain/deletion/service_test.go`

**Interfaces:**
- Consumes: Tasks 2–4; `consent.Service.Revoke(ctx, userID, clientID)`; `user.Service.AddUnlinkedService`; `audit.Service`.
- Produces:
  - `type ServiceUnlinker interface { Finish(ctx context.Context, r *Request) error }` implemented by `deletion.NewServiceUnlinker(consents, users, audit, mail, leavable)`: revokes consents of the service's login clients, `AddUnlinkedService`, audit `account.service_unlinked` `{service, request_id}`, sends the "you left {product}" e-mail. Idempotent.
  - `(*Service).WithServiceUnlinker(u ServiceUnlinker) *Service`.
  - In `step`: for `ScopeService` the `StatePending → locked` branch skips `ownership.Assess` and sets no workspaces; `StateLocked` dispatches only `participants = [r.Service]`; `StatePurging` reconciles only that service, then calls `unlinker.Finish` **instead of** `purger.Purge`, then `StatePurged`.
  - Mailer: the four existing deletion e-mails take the `product` string (empty = account) and say the account stays when it is set.

- [ ] **Step 1: Failing tests:** `TestAServiceRequestDispatchesAndWaitsForOneAck`; `TestAServiceRequestEndsWithoutPurgingAccountData` (`purger.Purge` never called, `unlinker.Finish` once, user still ACTIVE); `TestCancelingAServiceRequestUnlocksOnlyThatService` (Review Focus 2); `TestFinishIsIdempotent` (consent revoked, `UnlinkedServices` has S once, one audit row even if run twice); `TestABlockerAtTheEndOfGraceBlocksAServiceRequestToo`.
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3:** Implement. E-mail copy in pt-BR and en, names the product; the template tests assert the product name and the sentence that the account continues.
- [ ] **Step 4:** `go test ./... -count=1` (api) → PASS.
- [ ] **Step 5:** Commit `feat(deletion): finish a service unlink without erasing the account`.

---

### Task 6: Coming back — relink in the code exchange

**Files:**
- Modify: `internal/handler/token.go` (`authorizationCode`), `internal/erasurepub/sns.go` (publisher already exists; add `PublishRelink` or reuse `Publish` with the new type), `internal/handler/token_test.go`
- Create: `internal/domain/deletion/relink.go`

**Interfaces:**
- Consumes: `user.User.UnlinkedServices`, `config.ErasureParticipant.LoginClients`, `Publisher.Publish(ctx, erasure.Message)`.
- Produces:
  - `type Relinker interface { Relink(ctx context.Context, u *user.User, clientID string) error }`, implemented by `deletion.NewRelinker(pub Publisher, users interface{ RemoveUnlinkedService(ctx, userID, service string) error }, participants []config.ErasureParticipant)`.
  - `Relink`: for each service in `u.UnlinkedServices` whose `LoginClients` contains `clientID`: publish `TypeRelinked` (`Scope service`, `Services [S]`, new `RequestID`, `IssuedAt now`), **then** `RemoveUnlinkedService`. A publish error returns the error and removes nothing.
  - `TokenHandler.WithRelinker(Relinker)`. In `authorizationCode`, after the user and client are resolved and before minting: `if err := h.relinker.Relink(...); err != nil { return apierror.ServiceUnavailable(...) }` (503, no token).

- [ ] **Step 1: Failing tests:** `TestRelinkPublishesBeforeItClears` (fake records call order; Review Focus 3), `TestRelinkIgnoresOtherClients`, `TestRelinkFailureReturns503AndMintsNothing` (handler test; Review Focus 5; user's `UnlinkedServices` unchanged), `TestRelinkForAUserWhoNeverLeftCostsNothing` (no publish, no write).
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3:** Implement and wire in `cmd/api/main.go`. The hook reads only the already-loaded user.
- [ ] **Step 4:** `go test ./internal/handler/... ./internal/domain/deletion/ -count=1` → PASS.
- [ ] **Step 5:** README + `CONDUCT.md` (token issuance now can return 503 for a person who left a product). Commit `feat(oauth): relink a product the person left when they sign in to it again`.

---

### Task 7: HTTP surface

**Files:**
- Modify: `internal/handler/deletion.go`, `internal/handler/deletion_test.go`, `README.md` (endpoint table), `docs/specs/2026-10-06-account-deletion-ctech-account.md` § 4 (point to the new spec)

**Interfaces:**
- Produces:
  - `deletionRequestBody` gains `Scope string` (`validate:"omitempty,oneof=account service"`) and `Service string` (`validate:"required_if=Scope service"`).
  - `POST /v1.0/account/deletion`: for `service`, the phrase must equal `SAIR DO ` + upper-case product name (`deletion.ServicePhrase(service)`); same identity check; 422 for an unknown service (`ErrUnknownService` → problem `unknown-service`).
  - `GET /v1.0/account/deletion` response adds `scope`, `service`.
  - `GET /v1.0/account/deletion/services` → `200 [{"service":"poker","can_leave":true}]`, `can_leave` false while `GetOpen` finds a request; only `LeavableService` participants listed.

- [ ] **Step 1: Failing tests:** `TestRequestingAServiceNeedsItsOwnPhrase` (account phrase → 422), `TestRequestingAnUnknownServiceIs422`, `TestServicesListsOnlyLeavableProducts`, `TestServicesCanLeaveIsFalseWhileARequestIsOpen`, `TestStatusReportsScopeAndService`.
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3:** Implement; no new OAuth scope.
- [ ] **Step 4:** `go test ./... -count=1` → PASS. `go vet ./...` clean.
- [ ] **Step 5:** Commit `feat(deletion): service scope on the deletion endpoints`.

---

### Task 8: UI (`ui/`)

**Files:**
- Modify: `src/lib/deletion.ts` (+ `.test.ts`), `src/app/account/delete/page.tsx` (+ test), `src/components/deletion-link-page.tsx`, `src/locales/{pt-BR,en}.json`, `src/app/privacy/page.tsx` + frozen `v4/page.tsx`, `api/internal/legal/version.go` (`CurrentPrivacyVersion = "3.4"`), `ui/FRONTEND.md`
- Create: `src/app/account/leave/[service]/page.tsx` (+ test), `src/components/leave-products-card.tsx`

**Interfaces:**
- Consumes: Task 7 endpoints.
- Produces: `useLeavableServices()` (`GET /deletion/services`), `requestServiceUnlink(service, {confirmation_phrase, password?})`, `servicePhrase(service)` mirroring the API's.

- [ ] **Step 1: Failing tests:** `lists one card per leavable product and none when the list is empty`; `the leave page asks for the product phrase and rejects the account phrase`; `shows blockers from the participant`; `the confirm and cancel pages name the product when the request has one`; `the status card says "Saindo do {produto}" with a cancel link`.
- [ ] **Step 2:** `cd ui && npx vitest run src/app/account/leave src/lib/deletion.test.ts` → FAIL.
- [ ] **Step 3:** Implement by reusing the account deletion components; copy in both locales, product names from `deletion.services.{id}`. Consequences text per product comes from the data inventory and says the account and the other products continue.
- [ ] **Step 4:** `npm test && npm run build && npx eslint src --ext .ts,.tsx` → clean; the build lists `/account/leave/[service]`.
- [ ] **Step 5:** Privacy policy 3.4: freeze 3.3 at `/privacy/v4`... (follow Phase 4 Task 6 of `2026-10-07-account-deletion-phase4-ui.md` exactly: copy the current page verbatim to the next frozen path, set version `3.4`, one sentence "você pode sair de um único produto CTech sem encerrar sua conta, e os dados daquele produto são apagados ou anonimizados nos termos desta política", prepend history, set `CurrentPrivacyVersion`).
- [ ] **Step 6: Stop and ask the owner** to approve the sentence before committing or merging this step. Then commit `feat(ui): leave one CTech product` and `feat(legal): privacy policy 3.4, leaving a single product`.

---

### Task 9: Participants bump (repos `ctech-wallet`, `ctech-dfe`, `ctech-billing`, `ctech-poker`)

**Files per repo:** `api/go.mod`, `api/go.sum`, `README.md` (the account-deletion section), and the repo's participant plan/runbook line.

- [ ] **Step 1 (each repo):** `go get gopkg.aoctech.app/api-commons@<Task 1 tag> && go mod tidy`.
- [ ] **Step 2:** `go build ./... && go vet ./... && go test ./... -race -count=1` → PASS (no call site changes).
- [ ] **Step 3:** In the README section, add: "A service-scoped request (`scope: service`) arrives with no workspaces: `PurgeFunc` erases the person's own data only. `user.relinked` is handled by the consumer (`Store.Clear`)." For **dfe** and **billing** also add a test in the repo's erasure integration suite, `TestServiceScopePurgeKeepsTheWorkspaces`: a `user.erase` with `Scope service` and no workspaces leaves the person's surviving organization/space rows and invoices in place (billing: the personal finance space **and** the customer are still handled as the person's own data; dfe: certificates of the person erased, organization data intact). If the test fails, the repo's plan task for that step is "make the purge skip workspace steps when `WorkspaceIDs()` is empty" and is done in the same PR.
- [ ] **Step 4:** Commit `chore: api-commons <tag> (user.relinked)` and open a PR per repo. Merge and deploy each.

---

### Task 10: Rollout and documentation closure (repo `ctech-account`)

**Files:** `PLAN.md`, `docs/specs/2026-10-06-account-deletion-overview.md` (§ 4 link to the new spec), `README.md`, `ui/FRONTEND.md`

- [ ] **Step 1:** `PLAN.md`: check "Single-service unlink" and list the operator steps: after every participant deployed Task 9, set `login_clients` per product in `/ctech-account/{env}/erasure-participants`, **poker → billing → dfe**; `wallet` waits for the Asaas ruling.
- [ ] **Step 2:** In stage and prod, add the poker entry's `login_clients` first and verify end to end with a test user: request → confirm → locked at poker only (other products and sign-in still work) → purged → sign in to poker again → `user.relinked` → poker answers normally. Record the result in `PLAN.md`.
- [ ] **Step 3:** Repeat for billing, then dfe.
- [ ] **Step 4:** Commit `docs: service unlink rollout`.

---

## Self-review notes

- Spec coverage: U1 (Tasks 3, 4, 5, 9), U2 (3), U3/U4 (4, 5), U5 (3), U6/U7 (1, 6), U8 (5), U9 (2, 7, 8), U10 (9). § 4 phrase (7, 8), § 5.2 endpoints (7), § 9 policy (8), § 11 rollout (10).
- Names reused across tasks: `ServiceTarget`, `LoginClients`, `LeavableService`, `RevokeClients`, `RevokeForService`, `ServiceUnlinker.Finish`, `Relinker.Relink`, `TypeRelinked`, `AddUnlinkedService`/`RemoveUnlinkedService`.
- Known soft spots to resolve while executing, not before: the apikey scope→service mapping (Task 4) depends on how `scopes` exposes a scope's audience, and whether `ctech-poker`'s participant purge already skips workspace steps on an empty list (Task 9).
