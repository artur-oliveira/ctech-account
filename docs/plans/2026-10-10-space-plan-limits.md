# Plan limits on personal spaces (ctech-account) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enforce CTech Finanças plan limits on `personal` workspaces (spaces owned, people per space), read live from ctech-billing, and report both levels (`finance_spaces`, `finance_people`) to billing after every change, through a durable queue.

**Architecture:** The organization domain counts and guards (owned spaces from the membership index, people from strongly consistent member/invitation queries, a conditional counter in the same `TransactWrite` as the write). Billing is reached through a new `internal/billingclient` package (2 s GET with one retry, level POST) authenticated by a token this service signs for itself as OAuth client `account-billing`. A new `internal/domain/planlimit` package turns entitlements into quotas, keeps the `LEVEL_DIRTY` queue in `account_organizations`, reports inline and drains every minute under a Valkey lock. Everything is dark until `BILLING_API_URL` is set.

**Tech Stack:** Go 1.27 + Fiber v3 + DynamoDB (`api/`), standard `testing` + `net/http/httptest`; Next.js static export + vitest + TypeScript (`ui/`); AWS CDK + jest (`cdk/`).

**Spec:** [`docs/specs/2026-10-10-space-plan-limits.md`](../specs/2026-10-10-space-plan-limits.md). Contract: `ctech-billing/docs/specs/2026-10-10-plans-design.md` §§ 4–6. Builds on [`docs/specs/2026-10-09-personal-workspaces.md`](../specs/2026-10-09-personal-workspaces.md) (implemented; plan `docs/plans/2026-10-09-personal-workspaces.md`).

**Cross-project impact:** No change to JWT signing, JWKS, OAuth flows or any existing token: the account signs one more access token, in process, with the existing `JWTService.SignAccessToken` (same shape as the `client_credentials` grant, `api/internal/handler/token.go:201`). ctech-billing must accept it (credential `account-billing`, tenant `ctech`, owner `finance` — billing § 4). ctech-billing's plan screen reads the new counts on `GET /internal/users/:user_id/organizations`. ctech-dfe and ctech-wallet: none. `ui`: new 402/503 handling and a counter. `cdk`: one env var.

## Decisions this plan takes where the spec is silent or does not fit the code

Each is already recorded in the spec's "Amendment, planning (2026-10-10)" section.

- **P1 — Queue keys.** The spec's `pk=LEVEL_DIRTY, sk={owner_sub}` is one row per owner, so the row "due at the invitation's `expires_at`" and a later "due now" row would overwrite each other. Rows are `sk=NOW#{owner}` (collapsing, due now, deleted conditionally on `due_at`) and `sk=AT#{unix_seconds:012d}#{owner}` (scheduled), so the worker reads both with two key-range queries and no filter.
- **P2 — The spaces guard compares `max(real, counter)`.** Owned spaces are read from `lookup-index` (a GSI, eventually consistent), so a just-committed space may be missing from the real count while the counter, read with `ConsistentRead`, already has it. Using the real count alone would let a second request through. The counter is corrected **upward** at every check (as the spec says); **downward** drift (a failed best-effort decrement) is corrected by the worker (`ReconcileSpaceCounter`) once the index has settled. The people guard reads base-table queries with `ConsistentRead`, so it uses the real count exactly as the spec says.
- **P3 — Decrements are best-effort writes after the commit**, not inside the transaction: a decrement inside it would fail the remove/revoke/transfer on any row written before this feature (no counter yet), and reducing usage is never refused. A failed decrement schedules a reconcile one minute later.
- **P4 — Quotas are read from `subscriptions[].items[].metadata`** — that is the shape billing serves (`ctech-billing/api/internal/api/v1/dto.go:249-292`), not `subscription.metadata` as the spec words it. The first item carrying `quota_spaces` wins (Sob demanda carries `-1`/`-1` on both its items, spaces and people, so either is fine); several entitled subscriptions → the most generous. No entitled subscription and no `default` → **503** (billing without `owner_key` support), not 0.
- **P5 — No shared retrying HTTP client exists** in `gopkg.aoctech.app/api-commons` v1.13.1 (only `oauth2client`, which fetches a token and has no retries). The entitlement GET retries once on a transport error or 5xx inside its 2 s budget; level POSTs are not retried in the request (the queue retries them). Extracting a shared billing client for ctech-dfe and this repo is a follow-up for ctech-go-common, not this plan.
- **P6 — New route `GET /v1.0/organizations/:id/plan-usage`** (owner of a space) for the people page's `people + pending of limit` counter; the spec names the counter but no route.
- **P7 — Transfer's 402 carries `resource` only** (no `limit`, `used`, `plan`): those three are the other person's plan.
- **P8 — `Accept` marks the owner dirty.** It is never refused, but it can change `finance_people` (an invited address that becomes a user already in another of the owner's spaces collapses into one person).
- **P9 — After three guard conflicts the write answers 409** ("try again"), never 402 for a limit that was not reached.
- **P10 — `{BILLING}` is two hosts:** the API (`billing-api[-env].aoctech.app`, `BILLING_API_URL`) for the account, and the app (`billing[-env].aoctech.app`, `NEXT_PUBLIC_BILLING_URL`) for *Ver planos*.
- **P11 — Marking dirty happens right after the commit**, not inside the write's transaction (the queue's key layout stays out of the organization repository). A process killed between the two loses that report until the owner's next change; the next report carries the whole level.

## Global Constraints

- Organizations (`kind: organization`, or no `kind`) are never counted, never limited, never reported; with billing down every organization route behaves exactly as today.
- Limits are read **live, no cache**, on every check: `GET {BILLING_API_URL}/v1.0/entitlements?customer_ref=USER_{owner_sub}&owner_key=finance`, 2 s total, one retry on transport error or 5xx.
- `-1` is unlimited. A missing or non-integer quota (or one below `-1`) is **0** and is logged.
- Counted: spaces = `personal` workspaces the person owns; people in a space = members + **unexpired** invitations, owner excluded; `finance_people` = **distinct** people across the owner's `personal` workspaces (member user ids, plus normalised e-mails of unexpired invitations whose address is not already one of those members).
- Checked: create `personal` (owned < `quota_spaces`), invite to `personal` (people < `quota_people_per_space`), transfer of `personal` (the **new owner's** owned < their `quota_spaces`). Never checked: accept, remove, leave, revoke, set role.
- Over the limit nothing is removed (plans spec D6).
- Refusals: **402** problem with `code: "plan_limit"` and `{limit, used, plan, resource: "spaces"|"people"}`; billing unreachable/timing out/5xx/4xx → **503** with `code: "plan_unavailable"`, nothing written. Problem `type` URIs `https://accounts.aoctech.app/problems/plan-limit` and `…/plan-unavailable`.
- Level reports: `POST {BILLING_API_URL}/v1.0/usage/levels`, both meters, whole count, whatever the plan, idempotency key `lvl:{owner_sub}:{meter}:{occurred_at_unix_ms}`. **A report never undoes or fails the write.**
- No new secret: the token is signed in process for client `account-billing` with scopes `billing:entitlements:read billing:usage:write`, audience from the scope catalog, cached until one minute before it expires.
- `BILLING_API_URL` unset → no limits, no reports, no worker: today's behaviour (dev, and every environment until deploy step 2).
- UI copy, Portuguese, verbatim: *"Seu plano permite N espaços e você já tem N."*, *"Este espaço já tem N de N pessoas do seu plano."*, *"Não foi possível verificar seu plano agora. Tente em instantes."*, *"{Name} já está no limite de espaços do plano."*, buttons **Ver planos** and **Voltar**.
- Go: `gofmt` clean on touched files, `go vet ./...` clean. Four files are already not gofmt-clean on `main` (`internal/domain/mfa/totp/model.go`, `internal/domain/session/model.go`, `internal/domain/session/service_test.go`, `internal/geoupdater/updater_test.go`) — leave them alone.
- UI: every UI task is executed with the `/impeccable` skill loaded (user rule). `@aoctech/ui` is **not** a dependency of `ui/package.json`; use the local `@/components/ui/*` (`Alert`, `Button`, `buttonVariants`) — adopting the shared package is out of scope. Vitest runs with limited workers and is judged by its **exit code**: `npx vitest run --maxWorkers=2 <files>; echo "exit=$?"` → `exit=0`. Then `npx tsc --noEmit` and `npm run lint`.
- Commit messages carry no attribution trailer of any kind.

## Review Focus

1. **A person over their limit after a downgrade, or with billing down, shrinking or reshaping their space** — remove, leave, revoke, set role and accept must all work and never call billing. Task 3 `TestOverTheLimitNothingIsTakenAway`, `TestBillingDownNeverBlocksShrinking`.
2. **Re-inviting an address that is already pending, at the limit** — it replaces the row, so it is not a second person and must succeed without moving the counter. Task 3 `TestReinvitingAPendingAddressAtTheLimitSucceeds`.
3. **Billing down while somebody works on an organization** — create, invite and transfer of organizations must answer exactly as before. Task 4 `TestOrganizationsIgnoreBillingEntirely`.
4. **A transfer refused because of the other person's plan** — the body must not carry their plan, limit or usage. Task 4 `TestATransferRefusalRevealsNothingAboutTheOtherPlan`.
5. **`account-billing` not registered, not first-party, or missing a scope** — every check must answer 503 (never 500), and boot must log it loudly without failing (the account is the auth backbone). Task 6 `TestAnUnusableClientIsRefused`, Task 7 `TestTheStartupCheckLogsWithoutFailing`.

---

## API tasks (`api/`)

All Go commands run from `api/`.

### Task 1: Plan types and counting in the organization domain

**Files:**
- Create: `api/internal/domain/organization/planlimit.go`
- Test: `api/internal/domain/organization/planlimit_test.go`

**Interfaces:**
- Consumes: `Service`, `Organization`, `Membership`, `Invitation`, `NormalizeEmail`, `KindPersonal`, `RoleOwner`, `ErrNotFound` (existing); test helpers `newFakeRepo`, `fixedClock`, `join` (`service_test.go:23,9,276`).
- Produces:
  - `const Unlimited int64 = -1`; `ResourceSpaces = "spaces"`, `ResourcePeople = "people"`.
  - `type Quotas struct { Plan string; Spaces, PeoplePerSpace int64 }`; `func allows(limit, used int64) bool`.
  - `type PlanLimits interface { Quotas(ctx, ownerUserID string) (Quotas, error); LevelsChanged(ctx, ownerUserID string); ScheduleLevels(ctx, ownerUserID string, at time.Time) }`.
  - `ErrPlanUnavailable`, `ErrPlanBusy`, `ErrNotASpace`; `type PlanLimitError struct { Resource string; Limit, Used int64; Plan string; Hidden bool }`.
  - `type Levels struct { Spaces, People int64 }`; `type SpaceUsage struct { People, PendingInvitations, Limit int64; Plan string }`.
  - `type EmailOwner func(ctx context.Context, email string) (userID string, err error)`; `(*Service).WithEmailOwner(EmailOwner) *Service`.
  - `func InvitationLive(inv *Invitation, now time.Time) bool`.
  - `(*Service).Levels(ctx, ownerUserID) (Levels, error)`; `(*Service).SpaceCounts(ctx, orgID) (people, pending int64, err error)`; unexported `ownedSpaces`, `peopleIn`.

- [ ] **Step 1: Write the failing test** — `api/internal/domain/organization/planlimit_test.go`

```go
package organization

import (
	"context"
	"errors"
	"testing"
	"time"
)

// limitedRepoAndService is a fake repo and a service whose clock the test moves.
func limitedRepoAndService(t *testing.T) (*fakeRepo, *Service, *time.Time) {
	t.Helper()
	repo := newFakeRepo()
	now := fixedClock()
	svc := NewService(repo, func() time.Time { return now })
	return repo, svc, &now
}

func invite(t *testing.T, repo *fakeRepo, orgID, email string, expires time.Time) {
	t.Helper()
	if err := repo.PutInvitation(context.Background(), &Invitation{
		OrganizationID: orgID, Email: email, Role: RoleMember, TokenHash: "h-" + email, ExpiresAt: expires,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInvitationLive(t *testing.T) {
	now := fixedClock()
	if !InvitationLive(&Invitation{ExpiresAt: now}, now) {
		t.Fatal("an invitation is live up to and including its expiry instant, like Accept")
	}
	if InvitationLive(&Invitation{ExpiresAt: now.Add(-time.Second)}, now) {
		t.Fatal("an expired invitation reads as live")
	}
	if !InvitationLive(&Invitation{}, now) {
		t.Fatal("an invitation with no expiry is live")
	}
}

// Spec § 2: Pessoal is not a workspace; organizations and spaces the person is
// only a member of are not theirs.
func TestOnlyOwnedPersonalSpacesAreCounted(t *testing.T) {
	_, svc, _ := limitedRepoAndService(t)
	ctx := context.Background()
	if _, err := svc.CreateOfKind(ctx, KindPersonal, "usr_owner", "Dono", "Casa"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, "usr_owner", "Dono", "CTech"); err != nil {
		t.Fatal(err)
	}
	other, _ := svc.CreateOfKind(ctx, KindPersonal, "usr_other", "Outra", "Dela")
	join(t, svc, other.ID, "usr_owner", RoleMember)

	lv, err := svc.Levels(ctx, "usr_owner")
	if err != nil {
		t.Fatal(err)
	}
	if lv.Spaces != 1 {
		t.Fatalf("spaces = %d, want 1 (only the owned personal space)", lv.Spaces)
	}
}

// Spec § 2 and test 3: owner excluded; an expired invitation is not counted
// even while its row is still in the table (TTL has not reaped it).
func TestPeopleExcludeTheOwnerAndExpiredInvitations(t *testing.T) {
	repo, svc, now := limitedRepoAndService(t)
	ctx := context.Background()
	space, _ := svc.CreateOfKind(ctx, KindPersonal, "usr_owner", "Dono", "Casa")
	join(t, svc, space.ID, "usr_a", RoleMember)
	invite(t, repo, space.ID, "live@example.com", now.Add(time.Hour))
	invite(t, repo, space.ID, "gone@example.com", now.Add(-time.Hour))

	people, pending, err := svc.SpaceCounts(ctx, space.ID)
	if err != nil {
		t.Fatal(err)
	}
	if people != 1 || pending != 1 {
		t.Fatalf("people=%d pending=%d, want 1 and 1", people, pending)
	}
}

// Spec test 9: a person in two of the owner's spaces is one person, and an
// invited address that already belongs to a member is not a second one.
func TestFinancePeopleAreDistinct(t *testing.T) {
	repo, svc, now := limitedRepoAndService(t)
	ctx := context.Background()
	svc.WithEmailOwner(func(_ context.Context, email string) (string, error) {
		if email == "ana@example.com" {
			return "usr_ana", nil
		}
		return "", nil
	})
	casa, _ := svc.CreateOfKind(ctx, KindPersonal, "usr_owner", "Dono", "Casa")
	viagem, _ := svc.CreateOfKind(ctx, KindPersonal, "usr_owner", "Dono", "Viagem")
	join(t, svc, casa.ID, "usr_ana", RoleMember)
	join(t, svc, viagem.ID, "usr_ana", RoleViewer)
	invite(t, repo, viagem.ID, "ANA@example.com ", now.Add(time.Hour)) // already a member
	invite(t, repo, casa.ID, "bia@example.com", now.Add(time.Hour))
	invite(t, repo, viagem.ID, "bia@example.com", now.Add(time.Hour)) // same address twice

	lv, err := svc.Levels(ctx, "usr_owner")
	if err != nil {
		t.Fatal(err)
	}
	if lv.Spaces != 2 || lv.People != 2 {
		t.Fatalf("levels = %+v, want 2 spaces and 2 people (ana, bia)", lv)
	}
}

func TestALookupFailureIsAnError(t *testing.T) {
	repo, svc, now := limitedRepoAndService(t)
	ctx := context.Background()
	boom := errors.New("users table down")
	svc.WithEmailOwner(func(context.Context, string) (string, error) { return "", boom })
	casa, _ := svc.CreateOfKind(ctx, KindPersonal, "usr_owner", "Dono", "Casa")
	invite(t, repo, casa.ID, "x@example.com", now.Add(time.Hour))
	if _, err := svc.Levels(ctx, "usr_owner"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the lookup failure (a wrong level is worse than a late one)", err)
	}
}

func TestPlanLimitErrorSaysWhatWasRefused(t *testing.T) {
	err := error(&PlanLimitError{Resource: ResourcePeople, Limit: 5, Used: 5, Plan: "basic"})
	var target *PlanLimitError
	if !errors.As(err, &target) || target.Resource != ResourcePeople {
		t.Fatalf("not a PlanLimitError: %v", err)
	}
	if !allows(Unlimited, 1_000_000) || allows(0, 0) || !allows(3, 2) || allows(3, 3) {
		t.Fatal("allows() is wrong about -1, 0 or the boundary")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/domain/organization/ -run 'TestInvitationLive|TestOnlyOwnedPersonalSpacesAreCounted|TestPeopleExclude|TestFinancePeopleAreDistinct|TestALookupFailure|TestPlanLimitError' -v`
Expected: FAIL — `undefined: InvitationLive` (and the other new names).

- [ ] **Step 3: Write minimal implementation** — `api/internal/domain/organization/planlimit.go`, and add three fields to `Service` in `service.go:52-56`.

In `service.go`, the struct becomes:

```go
type Service struct {
	repo    Repository
	now     func() time.Time
	granter ActorGranter
	// limits and counters are set together by WithPlanLimits (planlimit.go);
	// both nil means no plan limits, which is every deployment without
	// BILLING_API_URL.
	limits     PlanLimits
	counters   CounterRepository
	emailOwner EmailOwner
}
```

`CounterRepository` is declared in Task 2; until then add this temporary line at the bottom of `planlimit.go` so the package compiles, and delete it in Task 2 Step 3:

```go
// CounterRepository is defined in counters.go (Task 2).
type CounterRepository interface{}
```

`planlimit.go`:

```go
package organization

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Plan limits on personal spaces (docs/specs/2026-10-10-space-plan-limits.md).
// The numbers come from ctech-billing on every check; this file holds what the
// domain needs to apply them, and the counting rules of spec § 2.

// Unlimited is billing's -1: no ceiling. The level is still reported.
const Unlimited int64 = -1

const (
	ResourceSpaces = "spaces"
	ResourcePeople = "people"
)

// Quotas is one person's Finanças plan as this domain needs it.
type Quotas struct {
	Plan           string
	Spaces         int64
	PeoplePerSpace int64
}

// allows reports whether one more fits under limit.
func allows(limit, used int64) bool { return limit == Unlimited || used < limit }

// PlanLimits is what the organization service needs from billing. Implemented
// by planlimit.Service; an interface so this package never imports billing.
type PlanLimits interface {
	// Quotas reads the owner's plan live. Any failure wraps ErrPlanUnavailable.
	Quotas(ctx context.Context, ownerUserID string) (Quotas, error)
	// LevelsChanged is called after a committed change to the owner's levels.
	// It never fails the caller: the write already happened.
	LevelsChanged(ctx context.Context, ownerUserID string)
	// ScheduleLevels asks for a report at a later instant (an invitation's expiry).
	ScheduleLevels(ctx context.Context, ownerUserID string, at time.Time)
}

var (
	// ErrPlanUnavailable is billing unreachable, slow or erroring. Nothing was written.
	ErrPlanUnavailable = errors.New("the plan could not be read")
	// ErrPlanBusy is a guard that kept moving under concurrent writes. Nothing was written.
	ErrPlanBusy = errors.New("the plan counter kept changing")
	// ErrNotASpace is a plan question about an organization.
	ErrNotASpace = errors.New("not a personal space")
)

// PlanLimitError is a write refused because the plan does not allow it.
type PlanLimitError struct {
	Resource string
	Limit    int64
	Used     int64
	Plan     string
	// Hidden: the plan belongs to somebody other than the caller (a transfer's
	// new owner), so the response carries none of Limit, Used and Plan.
	Hidden bool
}

func (e *PlanLimitError) Error() string {
	return fmt.Sprintf("plan limit reached: %s %d of %d", e.Resource, e.Used, e.Limit)
}

// Levels is what is reported to billing for one owner.
type Levels struct {
	Spaces int64
	People int64
}

// SpaceUsage is the people page's counter: people + pending of limit.
type SpaceUsage struct {
	People             int64
	PendingInvitations int64
	Limit              int64
	Plan               string
}

// EmailOwner resolves the account an address belongs to; "" when none.
type EmailOwner func(ctx context.Context, email string) (userID string, err error)

// WithEmailOwner wires the lookup finance_people needs to recognise an invited
// address that already belongs to a member. Without it every pending address
// counts as a person.
func (s *Service) WithEmailOwner(f EmailOwner) *Service {
	s.emailOwner = f
	return s
}

// InvitationLive is the same rule Accept applies: an invitation is spendable up
// to and including its expiry instant. The TTL reaps the row later; the count
// must not wait for it.
func InvitationLive(inv *Invitation, now time.Time) bool {
	return inv.ExpiresAt.IsZero() || !now.After(inv.ExpiresAt)
}

// ownedSpaces lists the personal workspaces this person owns. It reads the
// membership index, which is eventually consistent — see spaceGuard.
func (s *Service) ownedSpaces(ctx context.Context, ownerUserID string) ([]*Organization, error) {
	memberships, err := s.repo.ListForUser(ctx, ownerUserID)
	if err != nil {
		return nil, err
	}
	out := make([]*Organization, 0, len(memberships))
	for _, m := range memberships {
		if m.Role != RoleOwner {
			continue
		}
		org, err := s.repo.Get(ctx, m.OrganizationID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if org.KindOf() == KindPersonal && org.OwnerUserID == ownerUserID {
			out = append(out, org)
		}
	}
	return out, nil
}

// spacePeople is who counts in one space: member ids (owner excluded) and the
// normalised addresses of live invitations.
type spacePeople struct {
	members []string
	invited []string
}

func (p spacePeople) count() int64 { return int64(len(p.members) + len(p.invited)) }

func (s *Service) peopleIn(ctx context.Context, org *Organization) (spacePeople, error) {
	members, err := s.repo.ListMembers(ctx, org.ID)
	if err != nil {
		return spacePeople{}, err
	}
	invitations, err := s.repo.ListInvitations(ctx, org.ID)
	if err != nil {
		return spacePeople{}, err
	}
	now := s.now().UTC()
	var p spacePeople
	for _, m := range members {
		if m.UserID == org.OwnerUserID || m.Role == RoleOwner {
			continue
		}
		p.members = append(p.members, m.UserID)
	}
	for _, inv := range invitations {
		if InvitationLive(inv, now) {
			p.invited = append(p.invited, NormalizeEmail(inv.Email))
		}
	}
	return p, nil
}

// Levels counts what billing is told: owned personal spaces, and distinct
// people across them (spec § 2).
func (s *Service) Levels(ctx context.Context, ownerUserID string) (Levels, error) {
	spaces, err := s.ownedSpaces(ctx, ownerUserID)
	if err != nil {
		return Levels{}, err
	}
	memberIDs := map[string]bool{}
	emails := map[string]bool{}
	for _, space := range spaces {
		p, err := s.peopleIn(ctx, space)
		if err != nil {
			return Levels{}, err
		}
		for _, id := range p.members {
			memberIDs[id] = true
		}
		for _, e := range p.invited {
			emails[e] = true
		}
	}
	people := int64(len(memberIDs))
	for email := range emails {
		if s.emailOwner != nil {
			id, err := s.emailOwner(ctx, email)
			if err != nil {
				return Levels{}, err
			}
			if id != "" && memberIDs[id] {
				continue
			}
		}
		people++
	}
	return Levels{Spaces: int64(len(spaces)), People: people}, nil
}

// SpaceCounts is the internal route's answer for one owned space (spec § 6).
func (s *Service) SpaceCounts(ctx context.Context, orgID string) (people, pending int64, err error) {
	org, err := s.repo.Get(ctx, orgID)
	if err != nil {
		return 0, 0, err
	}
	p, err := s.peopleIn(ctx, org)
	if err != nil {
		return 0, 0, err
	}
	return int64(len(p.members)), int64(len(p.invited)), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/domain/organization/ -v`
Expected: PASS, every pre-existing test included.

- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/organization/planlimit.go api/internal/domain/organization/planlimit_test.go api/internal/domain/organization/service.go
git commit -m "feat(organization): plan types and the counting rules for personal spaces"
```

---

### Task 2: Guard counters in DynamoDB, and in both fakes

**Files:**
- Create: `api/internal/domain/organization/counters.go`
- Create: `api/internal/domain/organization/counters_test.go`
- Create: `api/internal/domain/organization/counters_fake_test.go`
- Modify: `api/internal/domain/organization/repository.go` (refactor item builders; `ConsistentRead` on `ListMembers`/`ListInvitations`; `invitationsName` field)
- Modify: `api/internal/domain/organization/planlimit.go` (delete the temporary `CounterRepository` line)
- Modify: `api/internal/domain/organization/service_test.go:15-29` (fake fields)
- Modify: `api/internal/database/dynamo.go` (alias `IsTransactionConflict`)
- Modify: `api/internal/handler/organization_test.go:24-38` (fake fields)
- Create: `api/internal/handler/organization_counters_fake_test.go`

**Interfaces:**
- Consumes: `repo`, `orgPK`, `metaSK`, `inviteSK`, `membershipItem`, `unmarshalInvitation`, `database.Base.BuildRawUpdateTxItem/BuildPutTxItemIfAbsent/TransactWrite/UpdateItemRaw` (existing); `InvitationLive` (Task 1).
- Produces:
  - `type Counter struct { N int64; Exists bool }`; `type CounterWrite struct { Expect Counter; Next int64; Blind bool }`; `ErrCounterMoved`.
  - `type CounterRepository interface` with `SpaceCounter(ctx, ownerUserID) (Counter, error)`, `PeopleCounter(ctx, orgID) (Counter, error)`, `GetInvitation(ctx, orgID, email) (*Invitation, error)`, `CreateWithOwnerGuarded(ctx, org *Organization, ownerName string, g CounterWrite) error`, `PutInvitationGuarded(ctx, inv *Invitation, g CounterWrite, now time.Time) error`, `TransferOwnershipGuarded(ctx, orgID, fromUserID, toUserID, demoteTo string, now time.Time, g CounterWrite) error`, `DecrementSpaceCounter(ctx, ownerUserID) error`, `DecrementPeopleCounter(ctx, orgID) error`, `ReconcileSpaceCounter(ctx, ownerUserID string, seen Counter, real int64) error`.
  - `NewCounterRepository(db *dynamodb.Client, tablePrefix string) CounterRepository`.
  - Keys: `OWNER#{sub}` / `PERSONAL_SPACES`, attribute `n`; workspace META attribute `people_n`. Both in `account_organizations`.
  - `database.IsTransactionConflict`.
  - Fakes: `fakeRepo` (package `organization`) and `memOrgRepo` (package `handler_test`) implement `CounterRepository`; `fakeRepo.beforeGuard func()` runs once inside the next guarded write (a concurrent request landing between check and write); `fakeRepo.decrementErr error`.

The transaction is cancelled with one reason per item. `IsConditionFailed` (api-commons ≥ v1.11) answers only `ConditionalCheckFailed`; a `TransactionConflict` (another transaction on the same counter) is **not** a condition failure, wrote nothing, and must be retried — so both map to `ErrCounterMoved` for the guard item, which the service retries (Task 3).

- [ ] **Step 1: Write the failing test** — `api/internal/domain/organization/counters_test.go`

```go
package organization

import (
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func TestCounterKeyShapes(t *testing.T) {
	if got := ownerPK("usr_1"); got != "OWNER#usr_1" {
		t.Errorf("ownerPK = %q", got)
	}
	if spacesCounterSK != "PERSONAL_SPACES" || spacesAttr != "n" || peopleAttr != "people_n" {
		t.Error("the counter keys drifted from spec § 3.2")
	}
}

// The guard pins the value read at the check: a concurrent write that moved
// the counter fails this one instead of being overwritten by it.
func TestGuardExpressions(t *testing.T) {
	update, cond, names, values := guardExpr(peopleAttr, CounterWrite{Expect: Counter{N: 4, Exists: true}, Next: 5}, "attribute_exists(pk)")
	if update != "SET #c = :next" || cond != "attribute_exists(pk) AND #c = :expect" || names["#c"] != "people_n" {
		t.Fatalf("update=%q cond=%q names=%v", update, cond, names)
	}
	if v := values[":expect"].(*types.AttributeValueMemberN).Value; v != "4" {
		t.Fatalf(":expect = %s", v)
	}
	if v := values[":next"].(*types.AttributeValueMemberN).Value; v != "5" {
		t.Fatalf(":next = %s", v)
	}

	_, cond, _, _ = guardExpr(spacesAttr, CounterWrite{Next: 1}, "")
	if cond != "attribute_not_exists(#c)" {
		t.Fatalf("an absent counter: cond = %q", cond)
	}

	// Unlimited still maintains the counter, with no condition (spec § 3.2).
	_, cond, _, values = guardExpr(spacesAttr, CounterWrite{Expect: Counter{N: 9, Exists: true}, Next: 10, Blind: true}, "")
	if cond != "" {
		t.Fatalf("a blind write has a condition: %q", cond)
	}
	if _, ok := values[":expect"]; ok {
		t.Fatal("a blind write carries :expect")
	}
}

func cancelled(reasons ...string) error {
	tce := &types.TransactionCanceledException{Message: aws.String("Transaction cancelled")}
	for _, r := range reasons {
		tce.CancellationReasons = append(tce.CancellationReasons, types.CancellationReason{Code: aws.String(r)})
	}
	return tce
}

func TestTheGuardItemDecidesWhetherTheCounterMoved(t *testing.T) {
	if err := guardOutcome(cancelled("None", "None", "ConditionalCheckFailed"), 2, ErrAlreadyMember); !errors.Is(err, ErrCounterMoved) {
		t.Fatalf("guard condition failed: %v", err)
	}
	if err := guardOutcome(cancelled("ConditionalCheckFailed", "None", "None"), 2, ErrAlreadyMember); !errors.Is(err, ErrAlreadyMember) {
		t.Fatalf("another item's condition failed: %v", err)
	}
	// A concurrent transaction wrote nothing and evaluated nothing: retry.
	if err := guardOutcome(cancelled("None", "TransactionConflict", "None"), 2, ErrAlreadyMember); !errors.Is(err, ErrCounterMoved) {
		t.Fatalf("a transaction conflict: %v", err)
	}
	if err := guardOutcome(nil, 2, ErrAlreadyMember); err != nil {
		t.Fatalf("success: %v", err)
	}
	boom := errors.New("network")
	if err := guardOutcome(boom, 2, ErrAlreadyMember); !errors.Is(err, boom) {
		t.Fatalf("an unrelated failure: %v", err)
	}
}

// Both fakes must honour the guard exactly as the condition does.
func TestTheFakeHonoursTheGuard(t *testing.T) {
	repo := newFakeRepo()
	org := &Organization{ID: "o1", OwnerUserID: "usr_1", Kind: KindPersonal, CreatedAt: fixedClock()}
	if err := repo.CreateWithOwnerGuarded(t.Context(), org, "Dono", CounterWrite{Expect: Counter{N: 3, Exists: true}, Next: 4}); !errors.Is(err, ErrCounterMoved) {
		t.Fatalf("absent counter expected as 3: %v", err)
	}
	if err := repo.CreateWithOwnerGuarded(t.Context(), org, "Dono", CounterWrite{Next: 1}); err != nil {
		t.Fatal(err)
	}
	if c, _ := repo.SpaceCounter(t.Context(), "usr_1"); c != (Counter{N: 1, Exists: true}) {
		t.Fatalf("counter = %+v", c)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/domain/organization/ -run 'TestCounterKeyShapes|TestGuardExpressions|TestTheGuardItem|TestTheFakeHonours' -v`
Expected: FAIL — `undefined: ownerPK`, `undefined: guardExpr`, …

- [ ] **Step 3: Write minimal implementation**

`api/internal/database/dynamo.go` — beside `IsConditionFailed`:

```go
// IsTransactionConflict reports a TransactWrite cancelled because another
// transaction touched one of its items. Nothing was written; retry it. Not a
// condition failure (api-commons narrowed IsConditionFailed in v1.11).
var IsTransactionConflict = dynamo.IsTransactionConflict
```

`repository.go`:
- add `invitationsName string` to `repo` and `invitationsName: database.TableName(tablePrefix, invitationsTable),` to `NewRepository`;
- in `ListMembers` and `ListInvitations`, add `ConsistentRead: true` to the `database.QueryOpts` (the people guard compares against these; one strongly consistent query per check is the price);
- extract the item building so the guarded variants reuse it. `CreateWithOwner` becomes:

```go
func (r *repo) createItems(org *Organization, ownerName string) ([]types.TransactWriteItem, error) {
	orgItem, err := attributevalue.MarshalMap(org)
	if err != nil {
		return nil, fmt.Errorf("marshaling organization: %w", err)
	}
	orgItem["pk"] = &types.AttributeValueMemberS{Value: orgPK(org.ID)}
	orgItem["sk"] = &types.AttributeValueMemberS{Value: metaSK}
	if org.SourceSystem != "" && org.SourceRef != "" {
		orgItem["lookup_pk"] = &types.AttributeValueMemberS{Value: lookupSourcePK(org.SourceSystem, org.SourceRef)}
	}
	memberItem, err := r.membershipItem(&Membership{
		OrganizationID: org.ID, UserID: org.OwnerUserID, Name: ownerName, Role: RoleOwner, CreatedAt: org.CreatedAt,
	})
	if err != nil {
		return nil, err
	}
	return []types.TransactWriteItem{
		r.orgs.BuildPutTxItemIfAbsent(orgItem),
		r.memberships.BuildPutTxItemIfAbsent(memberItem),
	}, nil
}

func (r *repo) CreateWithOwner(ctx context.Context, org *Organization, ownerName string) error {
	items, err := r.createItems(org, ownerName)
	if err != nil {
		return err
	}
	err = r.orgs.TransactWrite(ctx, items)
	if database.IsConditionFailed(err) {
		return ErrAlreadyMember
	}
	return err
}
```

(keep the existing comments above each function); `PutInvitation` gets its item from

```go
func invitationItem(inv *Invitation) (map[string]types.AttributeValue, error) {
	item, err := attributevalue.MarshalMap(inv)
	if err != nil {
		return nil, fmt.Errorf("marshaling invitation: %w", err)
	}
	item["pk"] = &types.AttributeValueMemberS{Value: orgPK(inv.OrganizationID)}
	item["sk"] = &types.AttributeValueMemberS{Value: inviteSK(inv.Email)}
	item["lookup_pk"] = &types.AttributeValueMemberS{Value: inv.TokenHash}
	item["expires_at"] = &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", inv.ExpiresAt.Unix())}
	return item, nil
}
```

and `TransferOwnership` gets its three items from `func (r *repo) transferItems(orgID, fromUserID, toUserID, demoteTo string, now time.Time) []types.TransactWriteItem` (the exact three `BuildRawUpdateTxItem` calls now inline at `repository.go:431-445`), then `TransactWrite` + the existing error mapping.

`counters.go`:

```go
package organization

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/account/api/internal/database"
)

// Guard counters (spec § 3.2). They are guards, not the source of truth: the
// counts in planlimit.go are. Each check re-derives the count, and the write
// carries the counter conditionally on the value that check read, so two
// requests that both saw 2 of 3 cannot both write.
const (
	ownerPKPrefix   = "OWNER#"
	spacesCounterSK = "PERSONAL_SPACES"
	spacesAttr      = "n"
	peopleAttr      = "people_n"

	reasonConditionalCheckFailed = "ConditionalCheckFailed"
)

func ownerPK(userID string) string { return ownerPKPrefix + userID }

// Counter is a guard counter as read. Exists is false for an item or attribute
// written before plan limits existed.
type Counter struct {
	N      int64
	Exists bool
}

// CounterWrite is the guard one write carries: the value it expects to find
// and the value it sets. Blind (unlimited plan) sets without a condition.
type CounterWrite struct {
	Expect Counter
	Next   int64
	Blind  bool
}

// ErrCounterMoved is a guard whose expectation no longer held — a concurrent
// write landed first, or a concurrent transaction conflicted. Nothing was
// written; recount and retry.
var ErrCounterMoved = errors.New("plan counter moved")

// CounterRepository is the guarded half of the repository. A separate interface
// so cmd/migrate-dfe-orgs's fake, which never meets a plan, is untouched.
type CounterRepository interface {
	SpaceCounter(ctx context.Context, ownerUserID string) (Counter, error)
	PeopleCounter(ctx context.Context, orgID string) (Counter, error)
	GetInvitation(ctx context.Context, orgID, email string) (*Invitation, error)
	CreateWithOwnerGuarded(ctx context.Context, org *Organization, ownerName string, g CounterWrite) error
	PutInvitationGuarded(ctx context.Context, inv *Invitation, g CounterWrite, now time.Time) error
	TransferOwnershipGuarded(ctx context.Context, orgID, fromUserID, toUserID, demoteTo string, now time.Time, g CounterWrite) error
	DecrementSpaceCounter(ctx context.Context, ownerUserID string) error
	DecrementPeopleCounter(ctx context.Context, orgID string) error
	ReconcileSpaceCounter(ctx context.Context, ownerUserID string, seen Counter, real int64) error
}

// NewCounterRepository returns the same DynamoDB repository, seen through its
// guarded half.
func NewCounterRepository(db *dynamodb.Client, tablePrefix string) CounterRepository {
	return NewRepository(db, tablePrefix).(*repo)
}

func numberAV(n int64) types.AttributeValue {
	return &types.AttributeValueMemberN{Value: strconv.FormatInt(n, 10)}
}

func keyOf(pk, sk string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: pk},
		"sk": &types.AttributeValueMemberS{Value: sk},
	}
}

// guardExpr is the SET and the condition for one guard counter. extra is
// ANDed in front (e.g. attribute_exists(pk) on a workspace's META).
func guardExpr(attr string, g CounterWrite, extra string) (update, cond string, names map[string]string, values map[string]types.AttributeValue) {
	names = map[string]string{"#c": attr}
	values = map[string]types.AttributeValue{":next": numberAV(g.Next)}
	parts := make([]string, 0, 2)
	if extra != "" {
		parts = append(parts, extra)
	}
	if !g.Blind {
		if g.Expect.Exists {
			parts = append(parts, "#c = :expect")
			values[":expect"] = numberAV(g.Expect.N)
		} else {
			parts = append(parts, "attribute_not_exists(#c)")
		}
	}
	return "SET #c = :next", strings.Join(parts, " AND "), names, values
}

// cancelledAt reports whether item index of a cancelled transaction failed
// with code.
func cancelledAt(err error, index int, code string) bool {
	tce, ok := errors.AsType[*types.TransactionCanceledException](err)
	if !ok || index >= len(tce.CancellationReasons) {
		return false
	}
	r := tce.CancellationReasons[index]
	return r.Code != nil && *r.Code == code
}

// guardOutcome maps a guarded transaction's error: the guard's own condition
// or any TransactionConflict → ErrCounterMoved (retry); any other condition →
// otherCondition; anything else unchanged.
func guardOutcome(err error, guardIndex int, otherCondition error) error {
	switch {
	case err == nil:
		return nil
	case database.IsTransactionConflict(err), cancelledAt(err, guardIndex, reasonConditionalCheckFailed):
		return ErrCounterMoved
	case database.IsConditionFailed(err):
		return otherCondition
	default:
		return err
	}
}

func (r *repo) readCounter(ctx context.Context, pk, sk, attr string) (Counter, error) {
	out, err := r.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:                aws.String(r.orgsName),
		Key:                      keyOf(pk, sk),
		ConsistentRead:           aws.Bool(true),
		ProjectionExpression:     aws.String("#c"),
		ExpressionAttributeNames: map[string]string{"#c": attr},
	})
	if err != nil {
		return Counter{}, fmt.Errorf("reading counter %s: %w", attr, err)
	}
	v, ok := out.Item[attr].(*types.AttributeValueMemberN)
	if !ok {
		return Counter{}, nil
	}
	n, err := strconv.ParseInt(v.Value, 10, 64)
	if err != nil {
		return Counter{}, fmt.Errorf("parsing counter %s: %w", attr, err)
	}
	return Counter{N: n, Exists: true}, nil
}

func (r *repo) SpaceCounter(ctx context.Context, ownerUserID string) (Counter, error) {
	return r.readCounter(ctx, ownerPK(ownerUserID), spacesCounterSK, spacesAttr)
}

func (r *repo) PeopleCounter(ctx context.Context, orgID string) (Counter, error) {
	return r.readCounter(ctx, orgPK(orgID), metaSK, peopleAttr)
}

// GetInvitation reads one pending row, strongly consistent: the invite path
// decides "same person again" or "a new person" from it.
func (r *repo) GetInvitation(ctx context.Context, orgID, email string) (*Invitation, error) {
	out, err := r.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(r.invitationsName),
		Key:            keyOf(orgPK(orgID), inviteSK(email)),
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("reading invitation: %w", err)
	}
	if out.Item == nil {
		return nil, ErrNotFound
	}
	return unmarshalInvitation(out.Item)
}

func (r *repo) spacesGuardItem(ownerUserID string, g CounterWrite) types.TransactWriteItem {
	update, cond, names, values := guardExpr(spacesAttr, g, "")
	return r.orgs.BuildRawUpdateTxItem(ownerPK(ownerUserID), aws.String(spacesCounterSK), update, cond, names, values)
}

func (r *repo) CreateWithOwnerGuarded(ctx context.Context, org *Organization, ownerName string, g CounterWrite) error {
	items, err := r.createItems(org, ownerName)
	if err != nil {
		return err
	}
	items = append(items, r.spacesGuardItem(org.OwnerUserID, g))
	return guardOutcome(r.orgs.TransactWrite(ctx, items), 2, ErrAlreadyMember)
}

// PutInvitationGuarded writes a NEW person's invitation: the row must be absent
// or expired (an expired row is not counted, so replacing it adds a person),
// and the space's people_n moves with it. A live row means a concurrent invite
// of the same address landed first — ErrCounterMoved, and the retry finds it.
func (r *repo) PutInvitationGuarded(ctx context.Context, inv *Invitation, g CounterWrite, now time.Time) error {
	item, err := invitationItem(inv)
	if err != nil {
		return err
	}
	put := types.TransactWriteItem{Put: &types.Put{
		TableName:                 aws.String(r.invitationsName),
		Item:                      item,
		ConditionExpression:       aws.String("attribute_not_exists(pk) OR expires_at < :now"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":now": numberAV(now.Unix())},
	}}
	update, cond, names, values := guardExpr(peopleAttr, g, "attribute_exists(pk)")
	guard := r.orgs.BuildRawUpdateTxItem(orgPK(inv.OrganizationID), aws.String(metaSK), update, cond, names, values)
	err = r.orgs.TransactWrite(ctx, []types.TransactWriteItem{put, guard})
	if database.IsConditionFailed(err) || database.IsTransactionConflict(err) {
		return ErrCounterMoved
	}
	if err != nil {
		return fmt.Errorf("writing invitation: %w", err)
	}
	return nil
}

func (r *repo) TransferOwnershipGuarded(ctx context.Context, orgID, fromUserID, toUserID, demoteTo string, now time.Time, g CounterWrite) error {
	items := append(r.transferItems(orgID, fromUserID, toUserID, demoteTo, now), r.spacesGuardItem(toUserID, g))
	err := guardOutcome(r.memberships.TransactWrite(ctx, items), 3, ErrNotFound)
	if err != nil && !errors.Is(err, ErrCounterMoved) && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("transferring ownership: %w", err)
	}
	return err
}

// decrement is best-effort and never below zero: reducing usage is never
// refused, and the next check re-derives the guard anyway (decision P3).
func (r *repo) decrement(ctx context.Context, pk, sk, attr string) error {
	_, err := r.orgs.UpdateItemRaw(ctx, &dynamodb.UpdateItemInput{
		TableName:                 aws.String(r.orgsName),
		Key:                       keyOf(pk, sk),
		UpdateExpression:          aws.String("SET #c = #c - :one"),
		ConditionExpression:       aws.String("#c > :zero"),
		ExpressionAttributeNames:  map[string]string{"#c": attr},
		ExpressionAttributeValues: map[string]types.AttributeValue{":one": numberAV(1), ":zero": numberAV(0)},
	})
	if database.IsConditionFailed(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("decrementing %s: %w", attr, err)
	}
	return nil
}

func (r *repo) DecrementSpaceCounter(ctx context.Context, ownerUserID string) error {
	return r.decrement(ctx, ownerPK(ownerUserID), spacesCounterSK, spacesAttr)
}

func (r *repo) DecrementPeopleCounter(ctx context.Context, orgID string) error {
	return r.decrement(ctx, orgPK(orgID), metaSK, peopleAttr)
}

// ReconcileSpaceCounter sets the counter to the real count, only if it still
// holds what the caller read — a create that landed meanwhile wins.
func (r *repo) ReconcileSpaceCounter(ctx context.Context, ownerUserID string, seen Counter, real int64) error {
	update, cond, names, values := guardExpr(spacesAttr, CounterWrite{Expect: seen, Next: real}, "")
	_, err := r.orgs.UpdateItemRaw(ctx, &dynamodb.UpdateItemInput{
		TableName:                 aws.String(r.orgsName),
		Key:                       keyOf(ownerPK(ownerUserID), spacesCounterSK),
		UpdateExpression:          aws.String(update),
		ConditionExpression:       aws.String(cond),
		ExpressionAttributeNames:  names,
		ExpressionAttributeValues: values,
	})
	if database.IsConditionFailed(err) {
		return nil
	}
	return err
}
```

Delete the temporary `type CounterRepository interface{}` from `planlimit.go`.

`service_test.go` — add to `fakeRepo`:

```go
	// Guard counters (counters_fake_test.go).
	spaceN       map[string]Counter
	peopleN      map[string]Counter
	beforeGuard  func()
	decrementErr error
```

and to `newFakeRepo`: `spaceN: map[string]Counter{}, peopleN: map[string]Counter{},`.

`counters_fake_test.go`:

```go
package organization

import (
	"context"
	"time"
)

// runRace runs the pending concurrent request once, inside the guarded write,
// between the service's check and the condition.
func (f *fakeRepo) runRace() {
	if f.beforeGuard != nil {
		hook := f.beforeGuard
		f.beforeGuard = nil
		hook()
	}
}

func counterHolds(cur Counter, g CounterWrite) bool {
	return g.Blind || (cur.Exists == g.Expect.Exists && (!cur.Exists || cur.N == g.Expect.N))
}

func (f *fakeRepo) SpaceCounter(_ context.Context, owner string) (Counter, error) { return f.spaceN[owner], nil }
func (f *fakeRepo) PeopleCounter(_ context.Context, orgID string) (Counter, error) { return f.peopleN[orgID], nil }

func (f *fakeRepo) GetInvitation(_ context.Context, orgID, email string) (*Invitation, error) {
	inv, ok := f.invitations[orgID][NormalizeEmail(email)]
	if !ok {
		return nil, ErrNotFound
	}
	copied := *inv
	return &copied, nil
}

func (f *fakeRepo) CreateWithOwnerGuarded(ctx context.Context, org *Organization, ownerName string, g CounterWrite) error {
	f.runRace()
	if !counterHolds(f.spaceN[org.OwnerUserID], g) {
		return ErrCounterMoved
	}
	if err := f.CreateWithOwner(ctx, org, ownerName); err != nil {
		return err
	}
	f.spaceN[org.OwnerUserID] = Counter{N: g.Next, Exists: true}
	return nil
}

func (f *fakeRepo) PutInvitationGuarded(ctx context.Context, inv *Invitation, g CounterWrite, now time.Time) error {
	f.runRace()
	if cur, ok := f.invitations[inv.OrganizationID][NormalizeEmail(inv.Email)]; ok && InvitationLive(cur, now) {
		return ErrCounterMoved
	}
	if !counterHolds(f.peopleN[inv.OrganizationID], g) {
		return ErrCounterMoved
	}
	if err := f.PutInvitation(ctx, inv); err != nil {
		return err
	}
	f.peopleN[inv.OrganizationID] = Counter{N: g.Next, Exists: true}
	return nil
}

func (f *fakeRepo) TransferOwnershipGuarded(ctx context.Context, orgID, from, to, demoteTo string, now time.Time, g CounterWrite) error {
	f.runRace()
	if !counterHolds(f.spaceN[to], g) {
		return ErrCounterMoved
	}
	if err := f.TransferOwnership(ctx, orgID, from, to, demoteTo, now); err != nil {
		return err
	}
	f.spaceN[to] = Counter{N: g.Next, Exists: true}
	return nil
}

func (f *fakeRepo) decrement(m map[string]Counter, key string) error {
	if f.decrementErr != nil {
		return f.decrementErr
	}
	if c := m[key]; c.Exists && c.N > 0 {
		m[key] = Counter{N: c.N - 1, Exists: true}
	}
	return nil
}

func (f *fakeRepo) DecrementSpaceCounter(_ context.Context, owner string) error {
	return f.decrement(f.spaceN, owner)
}

func (f *fakeRepo) DecrementPeopleCounter(_ context.Context, orgID string) error {
	return f.decrement(f.peopleN, orgID)
}

func (f *fakeRepo) ReconcileSpaceCounter(_ context.Context, owner string, seen Counter, real int64) error {
	if f.spaceN[owner] == seen {
		f.spaceN[owner] = Counter{N: real, Exists: true}
	}
	return nil
}

var _ CounterRepository = (*fakeRepo)(nil)
```

`internal/handler/organization_test.go` — add to `memOrgRepo` `spaceN, peopleN map[string]orgDomain.Counter` and initialise both in `newMemOrgRepo`. `internal/handler/organization_counters_fake_test.go`:

```go
package handler_test

import (
	"context"
	"time"

	orgDomain "gopkg.aoctech.app/account/api/internal/domain/organization"
)

// memOrgRepo's guarded half: the same conditions the DynamoDB guard enforces.

func memCounterHolds(cur orgDomain.Counter, g orgDomain.CounterWrite) bool {
	return g.Blind || (cur.Exists == g.Expect.Exists && (!cur.Exists || cur.N == g.Expect.N))
}

func (m *memOrgRepo) SpaceCounter(_ context.Context, owner string) (orgDomain.Counter, error) {
	return m.spaceN[owner], nil
}

func (m *memOrgRepo) PeopleCounter(_ context.Context, orgID string) (orgDomain.Counter, error) {
	return m.peopleN[orgID], nil
}

func (m *memOrgRepo) GetInvitation(_ context.Context, orgID, email string) (*orgDomain.Invitation, error) {
	inv, ok := m.invitations[orgID][orgDomain.NormalizeEmail(email)]
	if !ok {
		return nil, orgDomain.ErrNotFound
	}
	copied := *inv
	return &copied, nil
}

func (m *memOrgRepo) CreateWithOwnerGuarded(ctx context.Context, org *orgDomain.Organization, ownerName string, g orgDomain.CounterWrite) error {
	if !memCounterHolds(m.spaceN[org.OwnerUserID], g) {
		return orgDomain.ErrCounterMoved
	}
	if err := m.CreateWithOwner(ctx, org, ownerName); err != nil {
		return err
	}
	m.spaceN[org.OwnerUserID] = orgDomain.Counter{N: g.Next, Exists: true}
	return nil
}

func (m *memOrgRepo) PutInvitationGuarded(ctx context.Context, inv *orgDomain.Invitation, g orgDomain.CounterWrite, now time.Time) error {
	if cur, ok := m.invitations[inv.OrganizationID][orgDomain.NormalizeEmail(inv.Email)]; ok && orgDomain.InvitationLive(cur, now) {
		return orgDomain.ErrCounterMoved
	}
	if !memCounterHolds(m.peopleN[inv.OrganizationID], g) {
		return orgDomain.ErrCounterMoved
	}
	if err := m.PutInvitation(ctx, inv); err != nil {
		return err
	}
	m.peopleN[inv.OrganizationID] = orgDomain.Counter{N: g.Next, Exists: true}
	return nil
}

func (m *memOrgRepo) TransferOwnershipGuarded(ctx context.Context, orgID, from, to, demoteTo string, now time.Time, g orgDomain.CounterWrite) error {
	if !memCounterHolds(m.spaceN[to], g) {
		return orgDomain.ErrCounterMoved
	}
	if err := m.TransferOwnership(ctx, orgID, from, to, demoteTo, now); err != nil {
		return err
	}
	m.spaceN[to] = orgDomain.Counter{N: g.Next, Exists: true}
	return nil
}

func (m *memOrgRepo) DecrementSpaceCounter(_ context.Context, owner string) error {
	if c := m.spaceN[owner]; c.Exists && c.N > 0 {
		m.spaceN[owner] = orgDomain.Counter{N: c.N - 1, Exists: true}
	}
	return nil
}

func (m *memOrgRepo) DecrementPeopleCounter(_ context.Context, orgID string) error {
	if c := m.peopleN[orgID]; c.Exists && c.N > 0 {
		m.peopleN[orgID] = orgDomain.Counter{N: c.N - 1, Exists: true}
	}
	return nil
}

func (m *memOrgRepo) ReconcileSpaceCounter(_ context.Context, owner string, seen orgDomain.Counter, real int64) error {
	if m.spaceN[owner] == seen {
		m.spaceN[owner] = orgDomain.Counter{N: real, Exists: true}
	}
	return nil
}

var _ orgDomain.CounterRepository = (*memOrgRepo)(nil)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go build ./... && go test ./internal/domain/organization/ ./internal/handler/ ./internal/database/ ./cmd/migrate-dfe-orgs/`
Expected: `ok` for each; `go vet ./internal/domain/organization/ ./internal/handler/` clean.

- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/organization/ api/internal/database/dynamo.go api/internal/handler/organization_test.go api/internal/handler/organization_counters_fake_test.go
git commit -m "feat(organization): conditional guard counters for spaces and people"
```

---

### Task 3: Enforcement in the organization service

**Files:**
- Modify: `api/internal/domain/organization/planlimit.go` (`WithPlanLimits`, guards, `SpaceUsage`, `ReconcileSpaceCounter`, `afterPeopleLeft`)
- Modify: `api/internal/domain/organization/service.go` (`CreateOfKind` `:85-110`, `Remove` `:268-290`, `Transfer` `:296-322`, `Invite` `:358-414`, `Accept` `:430-487`, `RevokeInvitation` `:499-505`)
- Test: `api/internal/domain/organization/enforcement_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1–2; `fakeRepo.beforeGuard`, `fakeRepo.decrementErr`.
- Produces:
  - `(*Service).WithPlanLimits(limits PlanLimits, counters CounterRepository) *Service`.
  - `(*Service).SpaceUsage(ctx, orgID, actorUserID) (SpaceUsage, error)` — owner only; `ErrNotASpace` on an organization; `Limit` is `Unlimited` without limits.
  - `(*Service).ReconcileSpaceCounter(ctx, ownerUserID) error` (used by the worker, Task 7).
  - Errors from create/invite/transfer of a space: `*PlanLimitError`, anything wrapping `ErrPlanUnavailable`, `ErrPlanBusy`.
  - Calls into `PlanLimits`: create → `LevelsChanged(owner)`; invite → `LevelsChanged(owner)` + `ScheduleLevels(owner, inv.ExpiresAt+1s)`; revoke/remove/leave of a live person → `LevelsChanged(owner)`; transfer → `LevelsChanged(from)` + `LevelsChanged(to)`; accept → `LevelsChanged(owner)`.

The expiry report is scheduled one second **after** `ExpiresAt` because `InvitationLive` still counts the invitation at its exact expiry instant.

- [ ] **Step 1: Write the failing test** — `api/internal/domain/organization/enforcement_test.go`

```go
package organization

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type fakeLimits struct {
	quotas    map[string]Quotas
	err       error
	calls     int
	changed   []string
	scheduled []time.Time
}

var free = Quotas{Plan: "free", Spaces: 1, PeoplePerSpace: 1}
var basic = Quotas{Plan: "basic", Spaces: 3, PeoplePerSpace: 5}

func (f *fakeLimits) Quotas(_ context.Context, owner string) (Quotas, error) {
	f.calls++
	if f.err != nil {
		return Quotas{}, f.err
	}
	if q, ok := f.quotas[owner]; ok {
		return q, nil
	}
	return free, nil
}
func (f *fakeLimits) LevelsChanged(_ context.Context, owner string) { f.changed = append(f.changed, owner) }
func (f *fakeLimits) ScheduleLevels(_ context.Context, _ string, at time.Time) {
	f.scheduled = append(f.scheduled, at)
}

func limitedService(t *testing.T, quotas map[string]Quotas) (*fakeRepo, *Service, *fakeLimits) {
	t.Helper()
	repo := newFakeRepo()
	limits := &fakeLimits{quotas: quotas}
	svc := NewService(repo, fixedClock).WithPlanLimits(limits, repo)
	return repo, svc, limits
}

func mustCreateSpace(t *testing.T, svc *Service, owner, name string) *Organization {
	t.Helper()
	space, err := svc.CreateOfKind(context.Background(), KindPersonal, owner, "Dono", name)
	if err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}
	return space
}

func planLimit(t *testing.T, err error) *PlanLimitError {
	t.Helper()
	var le *PlanLimitError
	if !errors.As(err, &le) {
		t.Fatalf("err = %v, want a PlanLimitError", err)
	}
	return le
}

// Spec test 1.
func TestFreeAllowsOneSpaceAndRefusesTheSecond(t *testing.T) {
	repo, svc, _ := limitedService(t, nil)
	mustCreateSpace(t, svc, "usr_owner", "Casa")
	_, err := svc.CreateOfKind(context.Background(), KindPersonal, "usr_owner", "Dono", "Viagem")
	le := planLimit(t, err)
	if le.Resource != ResourceSpaces || le.Limit != 1 || le.Used != 1 || le.Plan != "free" || le.Hidden {
		t.Fatalf("refusal = %+v", le)
	}
	if len(repo.orgs) != 1 {
		t.Fatalf("%d workspaces written, want 1", len(repo.orgs))
	}
}

// Organizations are not counted and never ask billing.
func TestOrganizationsAreNeitherCountedNorChecked(t *testing.T) {
	_, svc, limits := limitedService(t, nil)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := svc.Create(ctx, "usr_owner", "Dono", fmt.Sprintf("Empresa %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	mustCreateSpace(t, svc, "usr_owner", "Casa")
	if limits.calls != 1 {
		t.Fatalf("billing read %d times, want once (the space only)", limits.calls)
	}
}

// Spec test 2, spaces half.
func TestBasicRefusesTheFourthSpace(t *testing.T) {
	_, svc, _ := limitedService(t, map[string]Quotas{"usr_owner": basic})
	for _, name := range []string{"A", "B", "C"} {
		mustCreateSpace(t, svc, "usr_owner", name)
	}
	_, err := svc.CreateOfKind(context.Background(), KindPersonal, "usr_owner", "Dono", "D")
	if le := planLimit(t, err); le.Used != 3 || le.Limit != 3 {
		t.Fatalf("refusal = %+v", le)
	}
}

// Spec test 2, people half: pending invitations count, the owner does not.
func TestTheSixthPersonIsRefusedCountingPendingInvitations(t *testing.T) {
	_, svc, _ := limitedService(t, map[string]Quotas{"usr_owner": basic})
	ctx := context.Background()
	space := mustCreateSpace(t, svc, "usr_owner", "Casa")
	join(t, svc, space.ID, "usr_a", RoleMember)
	join(t, svc, space.ID, "usr_b", RoleViewer)
	for _, e := range []string{"c@example.com", "d@example.com", "e@example.com"} {
		if _, err := svc.Invite(ctx, space.ID, "usr_owner", e, RoleMember, nil); err != nil {
			t.Fatalf("inviting %s: %v", e, err)
		}
	}
	_, err := svc.Invite(ctx, space.ID, "usr_owner", "f@example.com", RoleMember, nil)
	if le := planLimit(t, err); le.Resource != ResourcePeople || le.Used != 5 || le.Limit != 5 {
		t.Fatalf("refusal = %+v", le)
	}
}

// Spec test 3 through the invite path.
func TestAnExpiredInvitationFreesItsPlace(t *testing.T) {
	repo := newFakeRepo()
	now := fixedClock()
	limits := &fakeLimits{}
	svc := NewService(repo, func() time.Time { return now }).WithPlanLimits(limits, repo)
	ctx := context.Background()
	space := mustCreateSpace(t, svc, "usr_owner", "Casa")
	if _, err := svc.Invite(ctx, space.ID, "usr_owner", "a@example.com", RoleMember, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Invite(ctx, space.ID, "usr_owner", "b@example.com", RoleMember, nil); err == nil {
		t.Fatal("Free let a second person in")
	}
	now = now.Add(invitationTTL + time.Second) // a@ expired, row still there
	if _, err := svc.Invite(ctx, space.ID, "usr_owner", "b@example.com", RoleMember, nil); err != nil {
		t.Fatalf("the expired invitation still holds the place: %v", err)
	}
}

// Review Focus 2.
func TestReinvitingAPendingAddressAtTheLimitSucceeds(t *testing.T) {
	repo, svc, _ := limitedService(t, nil)
	ctx := context.Background()
	space := mustCreateSpace(t, svc, "usr_owner", "Casa")
	if _, err := svc.Invite(ctx, space.ID, "usr_owner", "a@example.com", RoleMember, nil); err != nil {
		t.Fatal(err)
	}
	before := repo.peopleN[space.ID]
	if _, err := svc.Invite(ctx, space.ID, "usr_owner", " A@Example.com", RoleViewer, nil); err != nil {
		t.Fatalf("re-inviting the pending address was refused: %v", err)
	}
	if repo.peopleN[space.ID] != before {
		t.Fatalf("people_n moved on a re-invite: %+v → %+v", before, repo.peopleN[space.ID])
	}
}

// Spec test 4.
func TestTwoConcurrentCreationsAtTheLimitLetExactlyOneThrough(t *testing.T) {
	repo, svc, _ := limitedService(t, map[string]Quotas{"usr_owner": basic})
	ctx := context.Background()
	mustCreateSpace(t, svc, "usr_owner", "A")
	mustCreateSpace(t, svc, "usr_owner", "B")
	var concurrentErr error
	repo.beforeGuard = func() {
		_, concurrentErr = svc.CreateOfKind(ctx, KindPersonal, "usr_owner", "Dono", "Outra aba")
	}
	_, err := svc.CreateOfKind(ctx, KindPersonal, "usr_owner", "Dono", "Esta aba")
	if concurrentErr != nil {
		t.Fatalf("the first writer failed: %v", concurrentErr)
	}
	planLimit(t, err)
	if len(repo.orgs) != 3 {
		t.Fatalf("%d spaces, want exactly 3", len(repo.orgs))
	}
}

func TestAGuardThatKeepsMovingAnswersBusy(t *testing.T) {
	repo, svc, _ := limitedService(t, map[string]Quotas{"usr_owner": {Plan: "ondemand", Spaces: 100, PeoplePerSpace: 100}})
	var bump func()
	bump = func() {
		c := repo.spaceN["usr_owner"]
		repo.spaceN["usr_owner"] = Counter{N: c.N + 1, Exists: true}
		repo.beforeGuard = bump
	}
	repo.beforeGuard = bump
	_, err := svc.CreateOfKind(context.Background(), KindPersonal, "usr_owner", "Dono", "Casa")
	if !errors.Is(err, ErrPlanBusy) {
		t.Fatalf("err = %v, want ErrPlanBusy", err)
	}
	repo.beforeGuard = nil
}

// Spec test 5 (domain half): nothing written; organizations unaffected.
func TestBillingDownWritesNothing(t *testing.T) {
	repo, svc, limits := limitedService(t, nil)
	limits.err = fmt.Errorf("%w: timeout", ErrPlanUnavailable)
	ctx := context.Background()
	if _, err := svc.CreateOfKind(ctx, KindPersonal, "usr_owner", "Dono", "Casa"); !errors.Is(err, ErrPlanUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if len(repo.orgs) != 0 || len(limits.changed) != 0 {
		t.Fatal("a space was written or reported with billing down")
	}
	if _, err := svc.Create(ctx, "usr_owner", "Dono", "CTech"); err != nil {
		t.Fatalf("an organization was refused with billing down: %v", err)
	}
}

// Spec test 6 / Review Focus 1: over the limit after a downgrade, everything
// that exists keeps working; only growth is refused.
func TestOverTheLimitNothingIsTakenAway(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, fixedClock)
	ctx := context.Background()
	var spaces []*Organization
	for _, name := range []string{"A", "B", "C"} {
		s, _ := svc.CreateOfKind(ctx, KindPersonal, "usr_owner", "Dono", name)
		spaces = append(spaces, s)
	}
	join(t, svc, spaces[0].ID, "usr_a", RoleMember)
	join(t, svc, spaces[0].ID, "usr_b", RoleMember)
	_, _ = svc.Invite(ctx, spaces[0].ID, "usr_owner", "c@example.com", RoleMember, nil)

	limits := &fakeLimits{} // downgraded to Free
	svc.WithPlanLimits(limits, repo)

	if err := svc.SetRole(ctx, spaces[0].ID, "usr_owner", "usr_a", RoleViewer); err != nil {
		t.Fatalf("set role: %v", err)
	}
	if err := svc.Remove(ctx, spaces[0].ID, "usr_owner", "usr_a"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := svc.Remove(ctx, spaces[0].ID, "usr_b", "usr_b"); err != nil {
		t.Fatalf("leave: %v", err)
	}
	if err := svc.RevokeInvitation(ctx, spaces[0].ID, "usr_owner", "c@example.com"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if limits.calls != 0 {
		t.Fatalf("shrinking read billing %d times", limits.calls)
	}
	if _, err := svc.Invite(ctx, spaces[1].ID, "usr_owner", "d@example.com", RoleMember, nil); err != nil {
		t.Fatalf("Free allows one person per space: %v", err)
	}
	if _, err := svc.CreateOfKind(ctx, KindPersonal, "usr_owner", "Dono", "D"); err == nil {
		t.Fatal("a fourth space was created over the limit")
	}
	if len(repo.orgs) != 3 {
		t.Fatalf("spaces = %d, want the three that existed", len(repo.orgs))
	}
}

// Review Focus 1, billing down: shrinking never waits on billing.
func TestBillingDownNeverBlocksShrinking(t *testing.T) {
	repo, svc, limits := limitedService(t, map[string]Quotas{"usr_owner": basic})
	ctx := context.Background()
	space := mustCreateSpace(t, svc, "usr_owner", "Casa")
	join(t, svc, space.ID, "usr_a", RoleMember)
	token, _ := svc.Invite(ctx, space.ID, "usr_owner", "b@example.com", RoleMember, nil)
	limits.err = fmt.Errorf("%w: down", ErrPlanUnavailable)
	if err := svc.Remove(ctx, space.ID, "usr_owner", "usr_a"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := svc.Accept(ctx, token, "usr_b", "b@example.com", "Bia"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	_ = repo
}

// Spec test 7.
func TestTransferToSomebodyAtTheirLimitIsRefused(t *testing.T) {
	repo, svc, _ := limitedService(t, map[string]Quotas{"usr_owner": basic})
	ctx := context.Background()
	space := mustCreateSpace(t, svc, "usr_owner", "Casa")
	mustCreateSpace(t, svc, "usr_to", "Dele") // usr_to is on Free with one space
	join(t, svc, space.ID, "usr_to", RoleMember)

	le := planLimit(t, svc.Transfer(ctx, space.ID, "usr_owner", "usr_to"))
	if !le.Hidden || le.Resource != ResourceSpaces {
		t.Fatalf("refusal = %+v, want hidden spaces", le)
	}
	if repo.orgs[space.ID].OwnerUserID != "usr_owner" {
		t.Fatal("the space moved")
	}
}

func TestATransferMovesTheCountersAndReportsBoth(t *testing.T) {
	repo, svc, limits := limitedService(t, map[string]Quotas{"usr_owner": basic, "usr_to": basic})
	ctx := context.Background()
	space := mustCreateSpace(t, svc, "usr_owner", "Casa")
	join(t, svc, space.ID, "usr_to", RoleMember)
	limits.changed = nil
	if err := svc.Transfer(ctx, space.ID, "usr_owner", "usr_to"); err != nil {
		t.Fatal(err)
	}
	if repo.spaceN["usr_owner"].N != 0 || repo.spaceN["usr_to"].N != 1 {
		t.Fatalf("counters: from %+v to %+v", repo.spaceN["usr_owner"], repo.spaceN["usr_to"])
	}
	if fmt.Sprint(limits.changed) != "[usr_owner usr_to]" {
		t.Fatalf("reported %v", limits.changed)
	}
}

func TestAFailedDecrementSchedulesAReconcile(t *testing.T) {
	repo, svc, limits := limitedService(t, map[string]Quotas{"usr_owner": basic, "usr_to": basic})
	ctx := context.Background()
	space := mustCreateSpace(t, svc, "usr_owner", "Casa")
	join(t, svc, space.ID, "usr_to", RoleMember)
	repo.decrementErr = errors.New("throttled")
	limits.scheduled = nil
	if err := svc.Transfer(ctx, space.ID, "usr_owner", "usr_to"); err != nil {
		t.Fatalf("a failed decrement failed the transfer: %v", err)
	}
	if len(limits.scheduled) != 1 || !limits.scheduled[0].Equal(fixedClock().Add(counterSettle)) {
		t.Fatalf("scheduled = %v", limits.scheduled)
	}
}

// Spec test 8 (domain half): every change marks the owner.
func TestEveryChangeMarksTheLevels(t *testing.T) {
	_, svc, limits := limitedService(t, map[string]Quotas{"usr_owner": basic})
	ctx := context.Background()
	space := mustCreateSpace(t, svc, "usr_owner", "Casa")
	join(t, svc, space.ID, "usr_a", RoleMember)
	token, _ := svc.Invite(ctx, space.ID, "usr_owner", "b@example.com", RoleMember, nil)
	_, _ = svc.Invite(ctx, space.ID, "usr_owner", "c@example.com", RoleMember, nil)
	_ = svc.RevokeInvitation(ctx, space.ID, "usr_owner", "c@example.com")
	_, _ = svc.Accept(ctx, token, "usr_b", "b@example.com", "Bia")
	_ = svc.Remove(ctx, space.ID, "usr_owner", "usr_a")
	_ = svc.Remove(ctx, space.ID, "usr_b", "usr_b")
	// create, invite, invite, revoke, accept, remove, leave
	if len(limits.changed) != 7 {
		t.Fatalf("changes reported = %d (%v), want 7", len(limits.changed), limits.changed)
	}
	want := fixedClock().Add(invitationTTL + time.Second)
	if len(limits.scheduled) != 2 || !limits.scheduled[0].Equal(want) {
		t.Fatalf("expiry reports = %v, want two at %v", limits.scheduled, want)
	}
}

func TestUnlimitedStillMaintainsTheCounter(t *testing.T) {
	repo, svc, _ := limitedService(t, map[string]Quotas{"usr_owner": {Plan: "ondemand", Spaces: Unlimited, PeoplePerSpace: Unlimited}})
	for i := 0; i < 12; i++ {
		mustCreateSpace(t, svc, "usr_owner", fmt.Sprintf("S%d", i))
	}
	if repo.spaceN["usr_owner"].N != 12 {
		t.Fatalf("counter = %+v", repo.spaceN["usr_owner"])
	}
}

// A counter written before limits (absent) or lower than the truth is set to
// the real count at the next check.
func TestALowCounterIsCorrectedAtTheCheck(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, fixedClock)
	mustCreateSpace(t, svc, "usr_owner", "A")
	mustCreateSpace(t, svc, "usr_owner", "B")
	svc.WithPlanLimits(&fakeLimits{quotas: map[string]Quotas{"usr_owner": basic}}, repo)
	mustCreateSpace(t, svc, "usr_owner", "C")
	if repo.spaceN["usr_owner"].N != 3 {
		t.Fatalf("counter = %+v, want 3", repo.spaceN["usr_owner"])
	}
}

func TestReconcileLowersAHighCounter(t *testing.T) {
	repo, svc, _ := limitedService(t, map[string]Quotas{"usr_owner": basic})
	mustCreateSpace(t, svc, "usr_owner", "A")
	repo.spaceN["usr_owner"] = Counter{N: 3, Exists: true} // a lost decrement
	if err := svc.ReconcileSpaceCounter(context.Background(), "usr_owner"); err != nil {
		t.Fatal(err)
	}
	if repo.spaceN["usr_owner"].N != 1 {
		t.Fatalf("counter = %+v, want 1", repo.spaceN["usr_owner"])
	}
}

func TestSpaceUsageIsTheOwnersCounter(t *testing.T) {
	repo, svc, _ := limitedService(t, map[string]Quotas{"usr_owner": basic})
	ctx := context.Background()
	space := mustCreateSpace(t, svc, "usr_owner", "Casa")
	join(t, svc, space.ID, "usr_a", RoleMember)
	_, _ = svc.Invite(ctx, space.ID, "usr_owner", "b@example.com", RoleMember, nil)
	u, err := svc.SpaceUsage(ctx, space.ID, "usr_owner")
	if err != nil {
		t.Fatal(err)
	}
	if u != (SpaceUsage{People: 1, PendingInvitations: 1, Limit: 5, Plan: "basic"}) {
		t.Fatalf("usage = %+v", u)
	}
	if _, err := svc.SpaceUsage(ctx, space.ID, "usr_a"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a member read the owner's plan: %v", err)
	}
	org, _ := svc.Create(ctx, "usr_owner", "Dono", "CTech")
	if _, err := svc.SpaceUsage(ctx, org.ID, "usr_owner"); !errors.Is(err, ErrNotASpace) {
		t.Fatalf("an organization: %v", err)
	}
	_ = repo
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/domain/organization/ -run 'Test(Free|Organizations|Basic|TheSixth|AnExpired|Reinviting|TwoConcurrent|AGuard|BillingDown|OverTheLimit|Transfer|ATransfer|AFailedDecrement|EveryChange|Unlimited|ALowCounter|Reconcile|SpaceUsage)' -v`
Expected: FAIL — `svc.WithPlanLimits undefined`.

- [ ] **Step 3: Write minimal implementation**

Append to `planlimit.go` (add imports `"gopkg.aoctech.app/api-commons/observability"`):

```go
// guardAttempts bounds the recount-and-retry loop when the guard moves under
// concurrent writes. Three is generous for two tabs; past it the answer is
// "try again" (ErrPlanBusy → 409), never a 402 for a limit nobody reached.
const guardAttempts = 3

// counterSettle is how long after a failed decrement the worker reconciles:
// long enough for the membership index to reflect the change (decision P2).
const counterSettle = time.Minute

// WithPlanLimits turns enforcement on for personal workspaces. Both nil (the
// default) is today's behaviour: nothing limited, nothing reported.
func (s *Service) WithPlanLimits(limits PlanLimits, counters CounterRepository) *Service {
	s.limits, s.counters = limits, counters
	return s
}

func (s *Service) limited() bool { return s.limits != nil && s.counters != nil }

// spaceGuard checks that ownerUserID may own one more space and returns the
// guard the write carries. used is max(real, counter): the real count reads an
// eventually consistent index, the counter a consistent item, and the larger
// is the one that cannot be behind (decision P2).
func (s *Service) spaceGuard(ctx context.Context, ownerUserID string, q Quotas, hidden bool) (CounterWrite, error) {
	owned, err := s.ownedSpaces(ctx, ownerUserID)
	if err != nil {
		return CounterWrite{}, err
	}
	seen, err := s.counters.SpaceCounter(ctx, ownerUserID)
	if err != nil {
		return CounterWrite{}, err
	}
	used := int64(len(owned))
	if seen.Exists && seen.N > used {
		used = seen.N
	}
	if !allows(q.Spaces, used) {
		return CounterWrite{}, &PlanLimitError{Resource: ResourceSpaces, Limit: q.Spaces, Used: used, Plan: q.Plan, Hidden: hidden}
	}
	return CounterWrite{Expect: seen, Next: used + 1, Blind: q.Spaces == Unlimited}, nil
}

func (s *Service) createGuarded(ctx context.Context, org *Organization, ownerName string) error {
	q, err := s.limits.Quotas(ctx, org.OwnerUserID)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < guardAttempts; attempt++ {
		g, err := s.spaceGuard(ctx, org.OwnerUserID, q, false)
		if err != nil {
			return err
		}
		if err := s.counters.CreateWithOwnerGuarded(ctx, org, ownerName, g); !errors.Is(err, ErrCounterMoved) {
			return err
		}
	}
	return ErrPlanBusy
}

// inviteGuarded writes a space invitation under the people guard. A live
// pending row for the same address is replaced without counting — it is the
// same person (spec § 2). The people count reads base-table queries with
// ConsistentRead, so it is exact and the counter is re-derived from it.
func (s *Service) inviteGuarded(ctx context.Context, org *Organization, inv *Invitation) error {
	q, err := s.limits.Quotas(ctx, org.OwnerUserID)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < guardAttempts; attempt++ {
		now := s.now().UTC()
		existing, err := s.counters.GetInvitation(ctx, org.ID, inv.Email)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if existing != nil && InvitationLive(existing, now) {
			return s.repo.PutInvitation(ctx, inv)
		}
		p, err := s.peopleIn(ctx, org)
		if err != nil {
			return err
		}
		seen, err := s.counters.PeopleCounter(ctx, org.ID)
		if err != nil {
			return err
		}
		used := p.count()
		if !allows(q.PeoplePerSpace, used) {
			return &PlanLimitError{Resource: ResourcePeople, Limit: q.PeoplePerSpace, Used: used, Plan: q.Plan}
		}
		g := CounterWrite{Expect: seen, Next: used + 1, Blind: q.PeoplePerSpace == Unlimited}
		if err := s.counters.PutInvitationGuarded(ctx, inv, g, now); !errors.Is(err, ErrCounterMoved) {
			return err
		}
	}
	return ErrPlanBusy
}

func (s *Service) transferGuarded(ctx context.Context, orgID, fromUserID, toUserID, demoteTo string) error {
	q, err := s.limits.Quotas(ctx, toUserID)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < guardAttempts; attempt++ {
		g, err := s.spaceGuard(ctx, toUserID, q, true)
		if err != nil {
			return err
		}
		err = s.counters.TransferOwnershipGuarded(ctx, orgID, fromUserID, toUserID, demoteTo, s.now().UTC(), g)
		if errors.Is(err, ErrCounterMoved) {
			continue
		}
		if err != nil {
			return err
		}
		if err := s.counters.DecrementSpaceCounter(ctx, fromUserID); err != nil {
			observability.Warn(ctx, "plan limits: space counter decrement failed; reconcile scheduled", err)
			s.limits.ScheduleLevels(ctx, fromUserID, s.now().UTC().Add(counterSettle))
		}
		s.limits.LevelsChanged(ctx, fromUserID)
		s.limits.LevelsChanged(ctx, toUserID)
		return nil
	}
	return ErrPlanBusy
}

// personalSpace returns the workspace when limits apply to it, nil otherwise.
func (s *Service) personalSpace(ctx context.Context, orgID string) *Organization {
	if !s.limited() {
		return nil
	}
	org, err := s.repo.Get(ctx, orgID)
	if err != nil || org.KindOf() != KindPersonal {
		return nil
	}
	return org
}

// afterPeopleLeft runs after a committed remove, leave or revoke of a counted
// person. Best effort: it never fails what already happened.
func (s *Service) afterPeopleLeft(ctx context.Context, org *Organization) {
	if err := s.counters.DecrementPeopleCounter(ctx, org.ID); err != nil {
		observability.Warn(ctx, "plan limits: people counter decrement failed", err)
	}
	s.limits.LevelsChanged(ctx, org.OwnerUserID)
}

// expiryReportAt is one second past expiry: InvitationLive still counts the
// invitation at its exact expiry instant.
func expiryReportAt(inv *Invitation) time.Time { return inv.ExpiresAt.Add(time.Second) }

// SpaceUsage is the people page's counter. Owner only; organizations have none.
func (s *Service) SpaceUsage(ctx context.Context, orgID, actorUserID string) (SpaceUsage, error) {
	if err := s.require(ctx, orgID, actorUserID, RoleOwner); err != nil {
		return SpaceUsage{}, err
	}
	org, err := s.repo.Get(ctx, orgID)
	if err != nil {
		return SpaceUsage{}, err
	}
	if org.KindOf() != KindPersonal {
		return SpaceUsage{}, ErrNotASpace
	}
	p, err := s.peopleIn(ctx, org)
	if err != nil {
		return SpaceUsage{}, err
	}
	u := SpaceUsage{People: int64(len(p.members)), PendingInvitations: int64(len(p.invited)), Limit: Unlimited}
	if s.limits != nil {
		q, err := s.limits.Quotas(ctx, org.OwnerUserID)
		if err != nil {
			return SpaceUsage{}, err
		}
		u.Limit, u.Plan = q.PeoplePerSpace, q.Plan
	}
	return u, nil
}

// ReconcileSpaceCounter lowers (or raises) the spaces counter to the real
// count, conditionally on the value read. The worker calls it once the index
// has settled (decision P2).
func (s *Service) ReconcileSpaceCounter(ctx context.Context, ownerUserID string) error {
	if s.counters == nil {
		return nil
	}
	owned, err := s.ownedSpaces(ctx, ownerUserID)
	if err != nil {
		return err
	}
	seen, err := s.counters.SpaceCounter(ctx, ownerUserID)
	if err != nil {
		return err
	}
	real := int64(len(owned))
	if seen.Exists && seen.N == real {
		return nil
	}
	return s.counters.ReconcileSpaceCounter(ctx, ownerUserID, seen, real)
}
```

`service.go` changes:

`CreateOfKind` — replace the final write:

```go
	if kind == KindPersonal && s.limited() {
		if err := s.createGuarded(ctx, org, strings.TrimSpace(ownerName)); err != nil {
			return nil, err
		}
		s.limits.LevelsChanged(ctx, ownerUserID)
		return org, nil
	}
	if err := s.repo.CreateWithOwner(ctx, org, strings.TrimSpace(ownerName)); err != nil {
		return nil, err
	}
	return org, nil
```

`Remove` — both successful `return s.repo.RemoveMembership(...)` lines become `return s.removeMembership(ctx, orgID, targetUserID)`, with:

```go
// removeMembership is RemoveMembership plus the plan bookkeeping of a space.
func (s *Service) removeMembership(ctx context.Context, orgID, userID string) error {
	if err := s.repo.RemoveMembership(ctx, orgID, userID); err != nil {
		return err
	}
	if space := s.personalSpace(ctx, orgID); space != nil {
		s.afterPeopleLeft(ctx, space)
	}
	return nil
}
```

`Transfer` — replace the last line:

```go
	if kind == KindPersonal && s.limited() {
		return s.transferGuarded(ctx, orgID, actorUserID, toUserID, demoteTo)
	}
	return s.repo.TransferOwnership(ctx, orgID, actorUserID, toUserID, demoteTo, s.now().UTC())
```

`Invite` — replace the `s.repo.PutInvitation(ctx, &Invitation{…})` block with:

```go
	inv := &Invitation{
		OrganizationID: orgID,
		Email:          address,
		Role:           role,
		CompanyIDs:     companyIDs,
		TokenHash:      HashToken(token),
		InvitedBy:      actorUserID,
		CreatedAt:      now,
		ExpiresAt:      now.Add(invitationTTL),
	}
	if space := s.personalSpace(ctx, orgID); space != nil {
		if err := s.inviteGuarded(ctx, space, inv); err != nil {
			return "", err
		}
		s.limits.LevelsChanged(ctx, space.OwnerUserID)
		s.limits.ScheduleLevels(ctx, space.OwnerUserID, expiryReportAt(inv))
		return token, nil
	}
	if err := s.repo.PutInvitation(ctx, inv); err != nil {
		return "", err
	}
	return token, nil
```

`Accept` — right after `s.repo.AcceptInvitation` succeeds (before the granter loop):

```go
	// Never refused (it was counted when sent), but finance_people may change:
	// the address became a user who can already be in another of the owner's
	// spaces (decision P8).
	if space := s.personalSpace(ctx, inv.OrganizationID); space != nil {
		s.limits.LevelsChanged(ctx, space.OwnerUserID)
	}
```

`RevokeInvitation`:

```go
func (s *Service) RevokeInvitation(ctx context.Context, orgID, actorUserID, email string) error {
	if err := s.require(ctx, orgID, actorUserID, RoleAdmin); err != nil {
		return err
	}
	space := s.personalSpace(ctx, orgID)
	var counted bool
	if space != nil {
		existing, err := s.counters.GetInvitation(ctx, orgID, email)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		counted = existing != nil && InvitationLive(existing, s.now().UTC())
	}
	if err := s.repo.DeleteInvitation(ctx, orgID, email); err != nil {
		return err
	}
	if counted {
		s.afterPeopleLeft(ctx, space)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/domain/organization/ -v && go vet ./internal/domain/organization/`
Expected: PASS, every pre-existing organization test included (limits are off in `seedOrg`/`seedSpace`).

- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/organization/
git commit -m "feat(organization): enforce plan limits on creating, inviting into and transferring a space"
```

---

### Task 4: 402 / 503 problems, and the space usage route

**Files:**
- Modify: `api/internal/apierror/problem.go` (fields `Code`, `Limit`, `Used`, `Plan`, `Resource`; constructors)
- Modify: `api/internal/handler/organization.go` (`Register`, `organizationProblem`, new `planUsage`)
- Modify: `api/internal/handler/organization_test.go:212-237` (`newOrgTestApp` → `newOrgTestAppWith`)
- Test: `api/internal/handler/organization_plan_test.go`

**Interfaces:**
- Consumes: `organization.PlanLimitError`, `ErrPlanUnavailable`, `ErrPlanBusy`, `ErrNotASpace`, `(*Service).WithPlanLimits`, `(*Service).SpaceUsage`, `Quotas` (Tasks 1–3); `memOrgRepo` as `CounterRepository` (Task 2); `orgTestApp`, `registerUser`, `issueToken`, `problemOf` (`organization_test.go:263`), `decodeJSON`, `bodyString` (existing test helpers).
- Produces:
  - `apierror.PlanLimit(resource, detail, instance string) *Problem` (402, type `…/plan-limit`, `code: "plan_limit"`), `(*Problem).WithPlanUsage(limit, used int64, plan string) *Problem`, `apierror.PlanUnavailable(instance string) *Problem` (503, type `…/plan-unavailable`, `code: "plan_unavailable"`), constants `CodePlanLimit`, `CodePlanUnavailable`.
  - Route `GET /v1.0/organizations/:id/plan-usage` (owner floor) → `200 {"people","pending_invitations","limit","plan"}`; `404` on an organization; `503 plan_unavailable`.
  - `newOrgTestAppWith(t, func(*orgDomain.Service, *memOrgRepo))` test helper.

- [ ] **Step 1: Write the failing test** — `api/internal/handler/organization_plan_test.go`

```go
package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	orgDomain "gopkg.aoctech.app/account/api/internal/domain/organization"
)

type stubLimits struct {
	quotas map[string]orgDomain.Quotas
	err    error
}

func (s *stubLimits) Quotas(_ context.Context, owner string) (orgDomain.Quotas, error) {
	if s.err != nil {
		return orgDomain.Quotas{}, s.err
	}
	if q, ok := s.quotas[owner]; ok {
		return q, nil
	}
	return orgDomain.Quotas{Plan: "free", Spaces: 1, PeoplePerSpace: 1}, nil
}
func (s *stubLimits) LevelsChanged(context.Context, string)            {}
func (s *stubLimits) ScheduleLevels(context.Context, string, time.Time) {}

func newLimitedOrgTestApp(t *testing.T, limits *stubLimits) *orgTestApp {
	t.Helper()
	return newOrgTestAppWith(t, func(svc *orgDomain.Service, repo *memOrgRepo) {
		svc.WithPlanLimits(limits, repo)
	})
}

func decodeMap(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestASecondSpaceOnFreeIs402WithTheNumbers(t *testing.T) {
	a := newLimitedOrgTestApp(t, &stubLimits{})
	owner := a.registerUser(t, "plan-402@example.com", "Sup3rSecret!pass", "Dono")
	tok := a.issueToken(t, owner.ID())
	create := `{"display_name":"Casa","kind":"personal"}`
	if resp := a.do(t, http.MethodPost, "/v1.0/organizations", tok, create); resp.StatusCode != http.StatusCreated {
		t.Fatalf("first space: %d", resp.StatusCode)
	}
	resp := a.do(t, http.MethodPost, "/v1.0/organizations", tok, create)
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", resp.StatusCode)
	}
	body := decodeMap(t, resp)
	if body["code"] != "plan_limit" || body["resource"] != "spaces" || body["limit"] != float64(1) ||
		body["used"] != float64(1) || body["plan"] != "free" || body["type"] != "https://accounts.aoctech.app/problems/plan-limit" {
		t.Fatalf("body = %v", body)
	}
}

// A limit of 0 (malformed catalogue) must still say limit 0, not omit it.
func TestAZeroLimitIsStillInTheBody(t *testing.T) {
	a := newLimitedOrgTestApp(t, &stubLimits{quotas: map[string]orgDomain.Quotas{}})
	owner := a.registerUser(t, "plan-zero@example.com", "Sup3rSecret!pass", "Dono")
	a.svc.WithPlanLimits(&stubLimits{quotas: map[string]orgDomain.Quotas{owner.ID(): {Plan: "free"}}}, a.repo)
	resp := a.do(t, http.MethodPost, "/v1.0/organizations", a.issueToken(t, owner.ID()), `{"display_name":"Casa","kind":"personal"}`)
	body := decodeMap(t, resp)
	if v, ok := body["limit"]; !ok || v != float64(0) {
		t.Fatalf("limit = %v (present %v)", v, ok)
	}
}

// Spec test 5 at the route / Review Focus 3.
func TestOrganizationsIgnoreBillingEntirely(t *testing.T) {
	a := newLimitedOrgTestApp(t, &stubLimits{err: fmt.Errorf("%w: down", orgDomain.ErrPlanUnavailable)})
	owner := a.registerUser(t, "plan-org@example.com", "Sup3rSecret!pass", "Dono")
	tok := a.issueToken(t, owner.ID())

	resp := a.do(t, http.MethodPost, "/v1.0/organizations", tok, `{"display_name":"Casa","kind":"personal"}`)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("space with billing down: %d", resp.StatusCode)
	}
	if body := decodeMap(t, resp); body["code"] != "plan_unavailable" {
		t.Fatalf("body = %v", body)
	}
	if len(a.repo.orgs) != 0 {
		t.Fatal("a space was written with billing down")
	}

	resp = a.do(t, http.MethodPost, "/v1.0/organizations", tok, `{"display_name":"CTech"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("organization with billing down: %d (%s)", resp.StatusCode, bodyString(resp))
	}
	var org struct{ ID string `json:"id"` }
	_ = json.NewDecoder(resp.Body).Decode(&org)
	if resp := a.do(t, http.MethodPost, "/v1.0/organizations/"+org.ID+"/invitations", tok, `{"email":"x@example.com","role":"member"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("organization invite with billing down: %d", resp.StatusCode)
	}
}

// Review Focus 4.
func TestATransferRefusalRevealsNothingAboutTheOtherPlan(t *testing.T) {
	limits := &stubLimits{quotas: map[string]orgDomain.Quotas{}}
	a := newLimitedOrgTestApp(t, limits)
	owner := a.registerUser(t, "plan-from@example.com", "Sup3rSecret!pass", "Dono")
	to := a.registerUser(t, "plan-to@example.com", "Sup3rSecret!pass", "Bia")
	limits.quotas[owner.ID()] = orgDomain.Quotas{Plan: "pro", Spaces: 10, PeoplePerSpace: 10}
	ctx := context.Background()
	space, err := a.svc.CreateOfKind(ctx, orgDomain.KindPersonal, owner.ID(), "Dono", "Casa")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.svc.CreateOfKind(ctx, orgDomain.KindPersonal, to.ID(), "Bia", "Dela"); err != nil {
		t.Fatal(err)
	}
	if err := a.repo.PutMembership(ctx, &orgDomain.Membership{OrganizationID: space.ID, UserID: to.ID(), Role: orgDomain.RoleMember, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	resp := a.do(t, http.MethodPost, "/v1.0/organizations/"+space.ID+"/transfer", a.issueToken(t, owner.ID()), `{"user_id":"`+to.ID()+`"}`)
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decodeMap(t, resp)
	if body["code"] != "plan_limit" || body["resource"] != "spaces" {
		t.Fatalf("body = %v", body)
	}
	for _, key := range []string{"plan", "limit", "used"} {
		if _, present := body[key]; present {
			t.Fatalf("the refusal carries the other person's %s: %v", key, body)
		}
	}
}

func TestPlanUsage(t *testing.T) {
	limits := &stubLimits{quotas: map[string]orgDomain.Quotas{}}
	a := newLimitedOrgTestApp(t, limits)
	owner := a.registerUser(t, "plan-usage@example.com", "Sup3rSecret!pass", "Dono")
	limits.quotas[owner.ID()] = orgDomain.Quotas{Plan: "basic", Spaces: 3, PeoplePerSpace: 5}
	tok := a.issueToken(t, owner.ID())
	ctx := context.Background()
	space, _ := a.svc.CreateOfKind(ctx, orgDomain.KindPersonal, owner.ID(), "Dono", "Casa")
	_, _ = a.svc.Invite(ctx, space.ID, owner.ID(), "b@example.com", orgDomain.RoleMember, nil)

	resp := a.do(t, http.MethodGet, "/v1.0/organizations/"+space.ID+"/plan-usage", tok, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%s)", resp.StatusCode, bodyString(resp))
	}
	body := decodeMap(t, resp)
	if body["people"] != float64(0) || body["pending_invitations"] != float64(1) || body["limit"] != float64(5) || body["plan"] != "basic" {
		t.Fatalf("body = %v", body)
	}

	org, _ := a.svc.Create(ctx, owner.ID(), "Dono", "CTech")
	if resp := a.do(t, http.MethodGet, "/v1.0/organizations/"+org.ID+"/plan-usage", tok, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("organization: %d", resp.StatusCode)
	}
	limits.err = fmt.Errorf("%w: down", orgDomain.ErrPlanUnavailable)
	if resp := a.do(t, http.MethodGet, "/v1.0/organizations/"+space.ID+"/plan-usage", tok, ""); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("billing down: %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/handler/ -run 'TestASecondSpaceOnFree|TestAZeroLimit|TestOrganizationsIgnoreBilling|TestATransferRefusal|TestPlanUsage' -v`
Expected: FAIL — `undefined: newOrgTestAppWith`.

- [ ] **Step 3: Write minimal implementation**

`organization_test.go` — rename the body of `newOrgTestApp` into `newOrgTestAppWith` and call `configure(svc, repo)` right after `svc := orgDomain.NewService(repo, time.Now)` when non-nil:

```go
func newOrgTestApp(t *testing.T) *orgTestApp { return newOrgTestAppWith(t, nil) }

func newOrgTestAppWith(t *testing.T, configure func(*orgDomain.Service, *memOrgRepo)) *orgTestApp {
	t.Helper()
	base := newTestApp(t)
	repo := newMemOrgRepo()
	svc := orgDomain.NewService(repo, time.Now)
	if configure != nil {
		configure(svc, repo)
	}
	// …the rest of the former newOrgTestApp, unchanged…
}
```

`apierror/problem.go` — add to `Problem` after `RequiredScope`:

```go
	// Code is a stable machine-readable reason for problems a client branches
	// on (the UI tells a plan limit from an unreachable plan by it).
	Code string `json:"code,omitempty"`
	// Plan-limit extension members (docs/specs/2026-10-10-space-plan-limits.md
	// § 3.1). Pointers, because a limit of 0 is a real answer.
	Limit    *int64 `json:"limit,omitempty"`
	Used     *int64 `json:"used,omitempty"`
	Plan     string `json:"plan,omitempty"`
	Resource string `json:"resource,omitempty"`
```

and at the end of the file:

```go
const (
	CodePlanLimit       = "plan_limit"
	CodePlanUnavailable = "plan_unavailable"
)

// PlanLimit → 402: the caller's plan does not allow one more space or person.
func PlanLimit(resource, detail, instance string) *Problem {
	p := newProblem("plan-limit", "Plan Limit Reached", http.StatusPaymentRequired, detail, instance)
	p.Code, p.Resource = CodePlanLimit, resource
	return p
}

// WithPlanUsage adds the numbers. Left off when the plan is somebody else's.
func (p *Problem) WithPlanUsage(limit, used int64, plan string) *Problem {
	p.Limit, p.Used, p.Plan = &limit, &used, plan
	return p
}

// PlanUnavailable → 503: billing could not be asked, so nothing was written.
func PlanUnavailable(instance string) *Problem {
	p := newProblem("plan-unavailable", "Plan Unavailable", http.StatusServiceUnavailable,
		"The plan could not be checked right now. Try again shortly.", instance)
	p.Code = CodePlanUnavailable
	return p
}
```

`handler/organization.go` — in `Register`, after the transfer route:

```go
	// Owner only, spaces only: the people page's "people + pending of limit".
	orgs.Get("/:id/plan-usage", scoped(organization.RoleOwner), h.planUsage)
```

handler:

```go
func (h *OrganizationHandler) planUsage(c fiber.Ctx) error {
	u, err := h.svc.SpaceUsage(c.Context(), middleware.GetOrgID(c), middleware.GetUserID(c))
	if err != nil {
		return organizationProblem(c, err)
	}
	return c.JSON(fiber.Map{
		"people": u.People, "pending_invitations": u.PendingInvitations, "limit": u.Limit, "plan": u.Plan,
	})
}
```

`organizationProblem` — first lines of the function, before the `switch`:

```go
	if le, ok := errors.AsType[*organization.PlanLimitError](err); ok {
		return planLimitProblem(c, le).Send(c)
	}
```

new cases in the `switch` (before `default`):

```go
	case errors.Is(err, organization.ErrPlanUnavailable):
		return apierror.PlanUnavailable(c.Path()).WithCause(err).Send(c)
	case errors.Is(err, organization.ErrPlanBusy):
		return apierror.Conflict("Somebody changed this at the same time. Try again.", c.Path()).Send(c)
	case errors.Is(err, organization.ErrNotASpace):
		return apierror.NotFound("space", c.Path()).Send(c)
```

and:

```go
// planLimitProblem says what was refused. A transfer's refusal is about the
// other person's plan, so it carries none of its numbers (decision P7).
func planLimitProblem(c fiber.Ctx, le *organization.PlanLimitError) *apierror.Problem {
	if le.Hidden {
		return apierror.PlanLimit(le.Resource, "That person is already at their plan's space limit.", c.Path())
	}
	detail := fmt.Sprintf("Your plan allows %d spaces and you already have %d.", le.Limit, le.Used)
	if le.Resource == organization.ResourcePeople {
		detail = fmt.Sprintf("This space already has %d of %d people on your plan.", le.Used, le.Limit)
	}
	return apierror.PlanLimit(le.Resource, detail, c.Path()).WithPlanUsage(le.Limit, le.Used, le.Plan)
}
```

(add `"fmt"` to the imports).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/handler/ ./internal/apierror/ && go vet ./internal/handler/ ./internal/apierror/`
Expected: `ok` for both.

- [ ] **Step 5: Commit**

```bash
git add api/internal/apierror/problem.go api/internal/handler/organization.go api/internal/handler/organization_test.go api/internal/handler/organization_plan_test.go
git commit -m "feat(organization): 402 plan_limit and 503 plan_unavailable problems, and the space usage route"
```

---

### Task 5: The billing client — entitlements and level reports

**Files:**
- Create: `api/internal/billingclient/client.go`
- Test: `api/internal/billingclient/client_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `type TokenSource interface { Token(ctx context.Context) (string, error) }`.
  - `func New(baseURL string, tokens TokenSource) *Client`.
  - `(*Client).Entitlements(ctx, customerRef, ownerKey string) (*Entitlements, error)` — errors wrap `ErrUnavailable`.
  - `(*Client).ReportLevel(ctx, LevelReport) error` — 2xx and 409 are success.
  - Types `Entitlements{Entitled bool; Subscriptions []EntitlementSubscription; Default *EntitlementDefault}`, `EntitlementSubscription{ID, Status, Plan string; Entitled bool; Items []EntitlementItem}`, `EntitlementItem{PriceID string; Metadata map[string]string}`, `EntitlementDefault{PriceID, Plan string; Metadata map[string]string}`, `LevelReport{CustomerRef, Meter string; Value int64; OccurredAt time.Time; IdempotencyKey string}`.
  - Constants `EntitlementsTimeout = 2 * time.Second`, `OwnerKeyFinance = "finance"`, `MeterSpaces = "finance_spaces"`, `MeterPeople = "finance_people"`; `func CustomerRef(userID string) string`; `func LevelKey(ownerUserID, meter string, at time.Time) string`.

- [ ] **Step 1: Write the failing test** — `api/internal/billingclient/client_test.go`

```go
package billingclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type staticToken string

func (s staticToken) Token(context.Context) (string, error) { return string(s), nil }

func TestEntitlementsAsksForTheFinanceOwner(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.0/entitlements" || r.URL.Query().Get("customer_ref") != "USER_u1" ||
			r.URL.Query().Get("owner_key") != "finance" || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("request = %s %s auth=%q", r.Method, r.URL, r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"entitled":false,"subscriptions":[],"default":{"price_id":"p_free","plan":"free","metadata":{"quota_spaces":"1","quota_people_per_space":"1"}}}`))
	}))
	defer srv.Close()

	e, err := New(srv.URL+"/", staticToken("tok")).Entitlements(context.Background(), CustomerRef("u1"), OwnerKeyFinance)
	if err != nil {
		t.Fatal(err)
	}
	if e.Default == nil || e.Default.Plan != "free" || e.Default.Metadata["quota_spaces"] != "1" {
		t.Fatalf("entitlements = %+v", e)
	}
}

func TestEntitlementsRetriesOnceOnA5xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"entitled":true,"subscriptions":[]}`))
	}))
	defer srv.Close()
	if _, err := New(srv.URL, staticToken("t")).Entitlements(context.Background(), "USER_u", OwnerKeyFinance); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

// A 4xx is billing answering, wrongly for us: not retried, still unavailable.
func TestEntitlementsDoesNotRetryA4xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	_, err := New(srv.URL, staticToken("t")).Entitlements(context.Background(), "USER_u", OwnerKeyFinance)
	if !errors.Is(err, ErrUnavailable) || calls.Load() != 1 {
		t.Fatalf("err = %v, calls = %d", err, calls.Load())
	}
}

func TestEntitlementsGivesUpWithinTwoSeconds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()
	start := time.Now()
	_, err := New(srv.URL, staticToken("t")).Entitlements(context.Background(), "USER_u", OwnerKeyFinance)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if elapsed := time.Since(start); elapsed > EntitlementsTimeout+500*time.Millisecond {
		t.Fatalf("took %v, budget %v", elapsed, EntitlementsTimeout)
	}
}

func TestReportLevel(t *testing.T) {
	var got map[string]any
	var key string
	status := http.StatusCreated
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1.0/usage/levels" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		key = r.Header.Get("Idempotency-Key")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(status)
	}))
	defer srv.Close()
	c := New(srv.URL, staticToken("t"))
	at := time.Date(2026, 10, 10, 14, 3, 0, 0, time.UTC)
	report := LevelReport{CustomerRef: "USER_u1", Meter: MeterSpaces, Value: 4, OccurredAt: at, IdempotencyKey: LevelKey("u1", MeterSpaces, at)}

	if err := c.ReportLevel(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	if got["customer_ref"] != "USER_u1" || got["meter"] != "finance_spaces" || got["value"] != float64(4) ||
		got["occurred_at"] != "2026-10-10T14:03:00Z" || got["idempotency_key"] != "lvl:u1:finance_spaces:1791640980000" {
		t.Fatalf("body = %v", got)
	}
	if key != "lvl:u1:finance_spaces:1791640980000" {
		t.Fatalf("Idempotency-Key = %q", key)
	}
	status = http.StatusConflict // same key already recorded
	if err := c.ReportLevel(context.Background(), report); err != nil {
		t.Fatalf("409 is delivered: %v", err)
	}
	status = http.StatusInternalServerError
	if err := c.ReportLevel(context.Background(), report); err == nil {
		t.Fatal("a 500 was taken as delivered")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/billingclient/ -v`
Expected: FAIL — package does not exist / `undefined: New`.

- [ ] **Step 3: Write minimal implementation** — `api/internal/billingclient/client.go`

```go
// Package billingclient is ctech-account's client for ctech-billing: the
// entitlement read behind plan limits and the level reports behind on-demand
// billing (docs/specs/2026-10-10-space-plan-limits.md §§ 1, 4).
//
// api-commons has no retrying HTTP client (only oauth2client, which fetches a
// token), so the one retry the read needs lives here (decision P5). ctech-dfe
// has its own billingclient; a shared one belongs in ctech-go-common later.
package billingclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// EntitlementsTimeout is the whole budget of one entitlement read,
	// retries included: a person is waiting on the create or invite.
	EntitlementsTimeout = 2 * time.Second
	// reportTimeout bounds one level report.
	reportTimeout       = 2 * time.Second
	entitlementAttempts = 2
	maxBody             = 1 << 20

	entitlementsPath = "/v1.0/entitlements"
	levelsPath       = "/v1.0/usage/levels"

	OwnerKeyFinance = "finance"
	MeterSpaces     = "finance_spaces"
	MeterPeople     = "finance_people"

	headerIdempotencyKey = "Idempotency-Key"
)

// ErrUnavailable is billing unreachable, slow, or answering anything but a
// usable 2xx. Callers turn it into 503 plan_unavailable.
var ErrUnavailable = errors.New("billing is unavailable")

// TokenSource yields the bearer token for billing (token.go).
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

type Client struct {
	http    *http.Client
	baseURL string
	tokens  TokenSource
}

func New(baseURL string, tokens TokenSource) *Client {
	return &Client{
		http:    &http.Client{Timeout: EntitlementsTimeout},
		baseURL: strings.TrimSuffix(baseURL, "/"),
		tokens:  tokens,
	}
}

// CustomerRef is billing's external reference for a person.
func CustomerRef(userID string) string { return "USER_" + userID }

// LevelKey is the report's idempotency key (spec § 4).
func LevelKey(ownerUserID, meter string, at time.Time) string {
	return fmt.Sprintf("lvl:%s:%s:%d", ownerUserID, meter, at.UnixMilli())
}

// Wire types: only the fields this service reads (billing dto.go:243-292).
type EntitlementItem struct {
	PriceID  string            `json:"price_id"`
	Metadata map[string]string `json:"metadata"`
}

type EntitlementSubscription struct {
	ID       string            `json:"id"`
	Status   string            `json:"status"`
	Entitled bool              `json:"entitled"`
	Plan     string            `json:"plan"`
	Items    []EntitlementItem `json:"items"`
}

type EntitlementDefault struct {
	PriceID  string            `json:"price_id"`
	Plan     string            `json:"plan"`
	Metadata map[string]string `json:"metadata"`
}

type Entitlements struct {
	Entitled      bool                      `json:"entitled"`
	Subscriptions []EntitlementSubscription `json:"subscriptions"`
	Default       *EntitlementDefault       `json:"default"`
}

type LevelReport struct {
	CustomerRef    string
	Meter          string
	Value          int64
	OccurredAt     time.Time
	IdempotencyKey string
}

// Entitlements reads a person's standing for one owner's products. One retry
// on a transport error or 5xx, inside EntitlementsTimeout; a 4xx is final.
func (c *Client) Entitlements(ctx context.Context, customerRef, ownerKey string) (*Entitlements, error) {
	ctx, cancel := context.WithTimeout(ctx, EntitlementsTimeout)
	defer cancel()
	q := url.Values{"customer_ref": {customerRef}, "owner_key": {ownerKey}}
	var lastErr error
	for attempt := 0; attempt < entitlementAttempts; attempt++ {
		var out Entitlements
		status, err := c.do(ctx, http.MethodGet, entitlementsPath+"?"+q.Encode(), "", nil, &out)
		if err == nil {
			return &out, nil
		}
		lastErr = err
		if (status != 0 && status < 500) || ctx.Err() != nil {
			break
		}
	}
	return nil, fmt.Errorf("%w: %v", ErrUnavailable, lastErr)
}

// ReportLevel sends one level. 409 (same key, already recorded with another
// body) is treated as delivered: the next report carries the whole level.
func (c *Client) ReportLevel(ctx context.Context, r LevelReport) error {
	ctx, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()
	body, err := json.Marshal(map[string]any{
		"customer_ref":    r.CustomerRef,
		"meter":           r.Meter,
		"value":           r.Value,
		"occurred_at":     r.OccurredAt.UTC().Format(time.RFC3339Nano),
		"idempotency_key": r.IdempotencyKey,
	})
	if err != nil {
		return err
	}
	status, err := c.do(ctx, http.MethodPost, levelsPath, r.IdempotencyKey, body, nil)
	if status == http.StatusConflict {
		return nil
	}
	return err
}

// do performs one call. status is 0 when no response arrived.
func (c *Client) do(ctx context.Context, method, path, idempotencyKey string, body []byte, out any) (int, error) {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return 0, fmt.Errorf("billing token: %w", err)
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set(headerIdempotencyKey, idempotencyKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Billing's detail is for the log, never for a person on our screens.
		return resp.StatusCode, fmt.Errorf("billing %s %s: status %d: %.200s", method, path, resp.StatusCode, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("decoding billing %s: %w", path, err)
		}
	}
	return resp.StatusCode, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/billingclient/ -v && go vet ./internal/billingclient/`
Expected: PASS (5 tests; the timeout test takes ~2 s).

- [ ] **Step 5: Commit**

```bash
git add api/internal/billingclient/
git commit -m "feat(billingclient): entitlements read with a 2 s budget and level reports"
```

---

### Task 6: The `account-billing` token, signed in process

**Files:**
- Create: `api/internal/billingclient/token.go`
- Test: `api/internal/billingclient/token_test.go`

**Interfaces:**
- Consumes: `oauthclient.OAuthClient` (`internal/domain/oauth/client/model.go`: `IsPublic`, `FirstParty`, `FilterScopes`), the signature of `crypto.JWTService.SignAccessToken` and `AccessTokenTTLSeconds` (`internal/crypto/jwt.go:83,266`), `scopes.CatalogService.AudiencesFor` (`internal/scopes/service.go:130`), `oauthclient.Repository.GetByID`.
- Produces:
  - `const ClientID = "account-billing"`; `var Scopes = []string{"billing:entitlements:read", "billing:usage:write"}`; `ErrClientNotUsable`.
  - `type Signer interface { SignAccessToken(...) (string, error); AccessTokenTTLSeconds() int }`, `type ClientLookup interface { GetByID(ctx, clientID string) (*oauthclient.OAuthClient, error) }`, `type AudienceResolver interface { AudiencesFor(ctx, scopes []string) ([]string, error) }`.
  - `func NewSelfSigned(signer Signer, clients ClientLookup, audiences AudienceResolver, issuer, selfAudience string, now func() time.Time) *SelfSigned`; `(*SelfSigned).Token(ctx) (string, error)` — implements `TokenSource`.

The token is the one `clientCredentials` would issue (`handler/token.go:160-210`): `sub = azp = account-billing`, empty `sid`, scopes filtered by the registered client, audience `[cfg.Audience] + AudiencesFor(scopes)`. The client row is re-read on every mint (every ~14 minutes), so deleting or narrowing `account-billing` here is the revocation, as for any other client.

- [ ] **Step 1: Write the failing test** — `api/internal/billingclient/token_test.go`

```go
package billingclient

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	oauthclient "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
)

type fakeSigner struct {
	calls    int
	sub, azp string
	scopes   []string
	aud      []string
}

func (f *fakeSigner) SignAccessToken(userID, sessionID, clientID string, scopes []string, issuer string, audience []string, _, _ int64, _ []string, _ string) (string, error) {
	f.calls++
	f.sub, f.azp, f.scopes, f.aud = userID, clientID, scopes, audience
	if sessionID != "" || issuer != "https://accounts.example" {
		return "", errors.New("wrong sid or issuer")
	}
	return "tok", nil
}
func (f *fakeSigner) AccessTokenTTLSeconds() int { return 900 }

type fakeClients struct{ client *oauthclient.OAuthClient }

func (f fakeClients) GetByID(context.Context, string) (*oauthclient.OAuthClient, error) {
	if f.client == nil {
		return nil, oauthclient.ErrNotFound
	}
	return f.client, nil
}

type fakeAudiences struct{}

func (fakeAudiences) AudiencesFor(context.Context, []string) ([]string, error) {
	return []string{"https://billing.aoctech.app"}, nil
}

func registered() *oauthclient.OAuthClient {
	return &oauthclient.OAuthClient{
		PK: oauthclient.BuildPK(ClientID), ClientType: "confidential", FirstParty: true,
		AllowedScopes: []string{"billing:entitlements:read", "billing:usage:write"},
	}
}

func TestTheTokenIsCachedUntilAMinuteBeforeItExpires(t *testing.T) {
	signer := &fakeSigner{}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	src := NewSelfSigned(signer, fakeClients{registered()}, fakeAudiences{}, "https://accounts.example", "https://accounts-api.example", func() time.Time { return now })
	ctx := context.Background()

	if tok, err := src.Token(ctx); err != nil || tok != "tok" {
		t.Fatalf("Token = %q, %v", tok, err)
	}
	if signer.sub != ClientID || signer.azp != ClientID || !slices.Equal(signer.scopes, Scopes) {
		t.Fatalf("claims sub=%s azp=%s scopes=%v", signer.sub, signer.azp, signer.scopes)
	}
	if !slices.Equal(signer.aud, []string{"https://accounts-api.example", "https://billing.aoctech.app"}) {
		t.Fatalf("aud = %v", signer.aud)
	}
	now = now.Add(13*time.Minute + 59*time.Second)
	_, _ = src.Token(ctx)
	if signer.calls != 1 {
		t.Fatalf("re-signed inside the window: %d calls", signer.calls)
	}
	now = now.Add(2 * time.Second) // past exp − 1 min
	_, _ = src.Token(ctx)
	if signer.calls != 2 {
		t.Fatalf("not re-signed a minute before expiry: %d calls", signer.calls)
	}
}

// Review Focus 5.
func TestAnUnusableClientIsRefused(t *testing.T) {
	ctx := context.Background()
	narrow := registered()
	narrow.AllowedScopes = []string{"billing:entitlements:read"}
	thirdParty := registered()
	thirdParty.FirstParty = false
	for name, client := range map[string]*oauthclient.OAuthClient{"missing": nil, "narrow": narrow, "third-party": thirdParty} {
		src := NewSelfSigned(&fakeSigner{}, fakeClients{client}, fakeAudiences{}, "https://accounts.example", "a", time.Now)
		if _, err := src.Token(ctx); err == nil {
			t.Fatalf("%s: a token was signed", name)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/billingclient/ -run 'TestTheTokenIsCached|TestAnUnusableClient' -v`
Expected: FAIL — `undefined: NewSelfSigned`.

- [ ] **Step 3: Write minimal implementation** — `api/internal/billingclient/token.go`

```go
package billingclient

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	oauthclient "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
)

// ClientID is the OAuth client this service acts as towards billing
// (spec § 5). Registered here with cmd/createclient; on billing's side it is a
// credential of tenant ctech, scoped to owner finance.
const ClientID = "account-billing"

// Scopes is everything plan limits need, and no more.
var Scopes = []string{"billing:entitlements:read", "billing:usage:write"}

// tokenRefreshMargin: the cached token is replaced a minute before it expires.
const tokenRefreshMargin = time.Minute

// ErrClientNotUsable is account-billing missing, public, third-party or short
// of a scope — a deployment mistake, surfaced as 503 and at startup.
var ErrClientNotUsable = errors.New("account-billing client is not usable")

type Signer interface {
	SignAccessToken(userID, sessionID, clientID string, scopes []string, issuer string, audience []string, authTime, lastMFAAt int64, amr []string, kycLevel string) (string, error)
	AccessTokenTTLSeconds() int
}

type ClientLookup interface {
	GetByID(ctx context.Context, clientID string) (*oauthclient.OAuthClient, error)
}

type AudienceResolver interface {
	AudiencesFor(ctx context.Context, scopes []string) ([]string, error)
}

// SelfSigned is the issuer signing its own client-credentials token: no secret
// leaves this process and none is stored for it (spec § 5).
type SelfSigned struct {
	signer       Signer
	clients      ClientLookup
	audiences    AudienceResolver
	issuer       string
	selfAudience string
	now          func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

func NewSelfSigned(signer Signer, clients ClientLookup, audiences AudienceResolver, issuer, selfAudience string, now func() time.Time) *SelfSigned {
	if now == nil {
		now = time.Now
	}
	return &SelfSigned{signer: signer, clients: clients, audiences: audiences, issuer: issuer, selfAudience: selfAudience, now: now}
}

func (s *SelfSigned) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.token != "" && now.Before(s.expires.Add(-tokenRefreshMargin)) {
		return s.token, nil
	}
	client, err := s.clients.GetByID(ctx, ClientID)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrClientNotUsable, err)
	}
	if client.IsPublic() || !client.FirstParty {
		return "", fmt.Errorf("%w: must be a confidential first-party client", ErrClientNotUsable)
	}
	scp := client.FilterScopes(Scopes)
	if len(scp) != len(Scopes) {
		return "", fmt.Errorf("%w: needs scopes %v, has %v", ErrClientNotUsable, Scopes, scp)
	}
	services, err := s.audiences.AudiencesFor(ctx, scp)
	if err != nil {
		return "", fmt.Errorf("resolving billing audience: %w", err)
	}
	audience := append([]string{s.selfAudience}, services...)
	// Same claims as the client_credentials grant (handler/token.go): sub and
	// azp are the client, no session, no step-up, no kyc_level.
	token, err := s.signer.SignAccessToken(ClientID, "", ClientID, scp, s.issuer, audience, 0, 0, nil, "")
	if err != nil {
		return "", fmt.Errorf("signing billing token: %w", err)
	}
	s.token = token
	s.expires = now.Add(time.Duration(s.signer.AccessTokenTTLSeconds()) * time.Second)
	return token, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/billingclient/ -v && go vet ./internal/billingclient/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api/internal/billingclient/token.go api/internal/billingclient/token_test.go
git commit -m "feat(billingclient): sign the account-billing token in process, cached until a minute before expiry"
```

---

### Task 7: `planlimit` — quotas, the `LEVEL_DIRTY` queue, inline reports and the worker

**Files:**
- Create: `api/internal/domain/planlimit/quotas.go`
- Create: `api/internal/domain/planlimit/queue.go`
- Create: `api/internal/domain/planlimit/service.go`
- Test: `api/internal/domain/planlimit/quotas_test.go`
- Test: `api/internal/domain/planlimit/service_test.go`
- Test: `api/internal/domain/planlimit/queue_test.go`

**Interfaces:**
- Consumes: `billingclient.Entitlements`, `LevelReport`, `CustomerRef`, `LevelKey`, `OwnerKeyFinance`, `MeterSpaces`, `MeterPeople` (Task 5); `organization.Quotas`, `Levels`, `Unlimited`, `ErrPlanUnavailable`, `(*organization.Service).Levels`, `.ReconcileSpaceCounter` (Tasks 1, 3); `database.NewBase`, `TableName`, `IsConditionFailed`, `Base.PutItem/QueryRaw/DeleteItemRaw`.
- Produces:
  - `func QuotasFrom(ctx, *billingclient.Entitlements) (organization.Quotas, error)`.
  - `type Billing interface { Entitlements(ctx, customerRef, ownerKey string) (*billingclient.Entitlements, error); ReportLevel(ctx, billingclient.LevelReport) error }`.
  - `type LevelCounter interface { Levels(ctx, ownerUserID string) (organization.Levels, error); ReconcileSpaceCounter(ctx, ownerUserID string) error }`.
  - `type DirtyRow struct { SK, OwnerUserID string; DueAt time.Time; Scheduled bool }`; `type Queue interface { MarkNow(ctx, owner string, at time.Time) error; Schedule(ctx, owner string, at time.Time) error; Due(ctx, now time.Time, limit int) ([]DirtyRow, error); Done(ctx, row DirtyRow) (bool, error) }`; `func NewQueue(db *dynamodb.Client, tablePrefix string) Queue`; `nowSK`, `atSK`.
  - `func NewService(b Billing, q Queue, now func() time.Time) *Service`; `(*Service).WithCounter(LevelCounter) *Service`; methods `Quotas`, `LevelsChanged`, `ScheduleLevels` (satisfy `organization.PlanLimits`), `ProcessDue(ctx)`, `CheckBilling(ctx) error`; `func RunWorker(ctx, *Service, tryLock func(context.Context) (bool, error), interval time.Duration)`; `const WorkerInterval = time.Minute`.

Rows (decision P1), in `account_organizations`: `pk=LEVEL_DIRTY`, `sk=NOW#{owner}` (put on every change; collapses; deleted conditionally on `due_at` = the change's instant, so a newer change survives an older success) and `sk=AT#{unix:012d}#{owner}` (scheduled, e.g. an invitation's expiry + 1 s). Attributes `owner_user_id` (S), `due_at` (N, unix ms). The worker reads `NOW#` with `begins_with` and `AT#` with `BETWEEN "AT#" AND "AT#{now+1:012d}"`, both `ConsistentRead`, up to 100 rows per tick, recounts at send time, reports both meters, then deletes. It reconciles the spaces counter for scheduled rows and rows at least `reconcileSettle` (30 s) old.

- [ ] **Step 1: Write the failing tests**

`api/internal/domain/planlimit/quotas_test.go`:

```go
package planlimit

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"gopkg.aoctech.app/account/api/internal/billingclient"
	"gopkg.aoctech.app/account/api/internal/domain/organization"
)

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func sub(entitled bool, plan string, meta map[string]string) billingclient.EntitlementSubscription {
	return billingclient.EntitlementSubscription{Entitled: entitled, Plan: plan, Items: []billingclient.EntitlementItem{{PriceID: "p", Metadata: meta}}}
}

func TestTheEntitledSubscriptionWins(t *testing.T) {
	q, err := QuotasFrom(context.Background(), &billingclient.Entitlements{
		Subscriptions: []billingclient.EntitlementSubscription{
			sub(false, "pro", map[string]string{"quota_spaces": "10", "quota_people_per_space": "10"}),
			sub(true, "basic", map[string]string{"quota_spaces": "3", "quota_people_per_space": "5"}),
		},
		Default: &billingclient.EntitlementDefault{Plan: "free", Metadata: map[string]string{"quota_spaces": "1", "quota_people_per_space": "1"}},
	})
	if err != nil || q != (organization.Quotas{Plan: "basic", Spaces: 3, PeoplePerSpace: 5}) {
		t.Fatalf("q = %+v, %v", q, err)
	}
}

func TestNoEntitledSubscriptionReadsTheDefault(t *testing.T) {
	q, err := QuotasFrom(context.Background(), &billingclient.Entitlements{
		Default: &billingclient.EntitlementDefault{Plan: "free", Metadata: map[string]string{"quota_spaces": "1", "quota_people_per_space": "1"}},
	})
	if err != nil || q != (organization.Quotas{Plan: "free", Spaces: 1, PeoplePerSpace: 1}) {
		t.Fatalf("q = %+v, %v", q, err)
	}
}

// Sob demanda bills two prices; both carry the -1/-1 quotas.
func TestOnDemandIsUnlimited(t *testing.T) {
	unlimited := map[string]string{"quota_spaces": "-1", "quota_people_per_space": "-1"}
	q, _ := QuotasFrom(context.Background(), &billingclient.Entitlements{Subscriptions: []billingclient.EntitlementSubscription{{
		Entitled: true, Plan: "ondemand",
		Items: []billingclient.EntitlementItem{{PriceID: "price_finance_ondemand_spaces", Metadata: unlimited}, {PriceID: "price_finance_ondemand_people", Metadata: unlimited}},
	}}})
	if q.Spaces != organization.Unlimited || q.PeoplePerSpace != organization.Unlimited {
		t.Fatalf("q = %+v", q)
	}
}

func TestTwoEntitledSubscriptionsTakeTheMoreGenerous(t *testing.T) {
	q, _ := QuotasFrom(context.Background(), &billingclient.Entitlements{Subscriptions: []billingclient.EntitlementSubscription{
		sub(true, "basic", map[string]string{"quota_spaces": "3", "quota_people_per_space": "5"}),
		sub(true, "pro", map[string]string{"quota_spaces": "10", "quota_people_per_space": "10"}),
	}})
	if q.Plan != "pro" {
		t.Fatalf("q = %+v", q)
	}
}

// Spec test 10: a catalogue mistake refuses growth, never grants unlimited.
func TestMalformedQuotaIsZeroAndLogged(t *testing.T) {
	logs := captureLogs(t)
	q, err := QuotasFrom(context.Background(), &billingclient.Entitlements{Subscriptions: []billingclient.EntitlementSubscription{
		sub(true, "basic", map[string]string{"quota_spaces": "three", "quota_people_per_space": "-7"}),
	}})
	if err != nil || q.Spaces != 0 || q.PeoplePerSpace != 0 {
		t.Fatalf("q = %+v, %v", q, err)
	}
	if !strings.Contains(logs.String(), "quota_spaces") || !strings.Contains(logs.String(), "quota_people_per_space") {
		t.Fatalf("not logged: %s", logs)
	}
	q, _ = QuotasFrom(context.Background(), &billingclient.Entitlements{Subscriptions: []billingclient.EntitlementSubscription{sub(true, "basic", nil)}})
	if q.Spaces != 0 || q.PeoplePerSpace != 0 {
		t.Fatalf("missing metadata: %+v", q)
	}
}

func TestNoEntitlementAndNoDefaultIsUnavailable(t *testing.T) {
	if _, err := QuotasFrom(context.Background(), &billingclient.Entitlements{}); !errors.Is(err, organization.ErrPlanUnavailable) {
		t.Fatalf("err = %v", err)
	}
}
```

`api/internal/domain/planlimit/service_test.go`:

```go
package planlimit

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/account/api/internal/billingclient"
	"gopkg.aoctech.app/account/api/internal/domain/organization"
)

type fakeBilling struct {
	ent       *billingclient.Entitlements
	entErr    error
	reports   []billingclient.LevelReport
	reportErr error
	onReport  func()
}

func (f *fakeBilling) Entitlements(context.Context, string, string) (*billingclient.Entitlements, error) {
	return f.ent, f.entErr
}

func (f *fakeBilling) ReportLevel(_ context.Context, r billingclient.LevelReport) error {
	if f.onReport != nil {
		hook := f.onReport
		f.onReport = nil
		hook()
	}
	if f.reportErr != nil {
		return f.reportErr
	}
	f.reports = append(f.reports, r)
	return nil
}

type fakeCounter struct {
	levels     map[string]organization.Levels
	reconciled []string
}

func (f *fakeCounter) Levels(_ context.Context, owner string) (organization.Levels, error) {
	return f.levels[owner], nil
}
func (f *fakeCounter) ReconcileSpaceCounter(_ context.Context, owner string) error {
	f.reconciled = append(f.reconciled, owner)
	return nil
}

type memQueue struct{ rows map[string]DirtyRow }

func (q *memQueue) MarkNow(_ context.Context, owner string, at time.Time) error {
	q.rows[nowSK(owner)] = DirtyRow{SK: nowSK(owner), OwnerUserID: owner, DueAt: at}
	return nil
}
func (q *memQueue) Schedule(_ context.Context, owner string, at time.Time) error {
	q.rows[atSK(at, owner)] = DirtyRow{SK: atSK(at, owner), OwnerUserID: owner, DueAt: at, Scheduled: true}
	return nil
}
func (q *memQueue) Due(_ context.Context, now time.Time, limit int) ([]DirtyRow, error) {
	out := []DirtyRow{}
	for _, r := range q.rows {
		if !r.Scheduled || !r.DueAt.After(now) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SK < out[j].SK })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (q *memQueue) Done(_ context.Context, row DirtyRow) (bool, error) {
	cur, ok := q.rows[row.SK]
	if !ok || (!row.Scheduled && !cur.DueAt.Equal(row.DueAt)) {
		return false, nil
	}
	delete(q.rows, row.SK)
	return true, nil
}

type harness struct {
	svc     *Service
	billing *fakeBilling
	counter *fakeCounter
	queue   *memQueue
	now     *time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	now := time.Date(2026, 10, 10, 14, 3, 0, 0, time.UTC)
	h := &harness{
		billing: &fakeBilling{},
		counter: &fakeCounter{levels: map[string]organization.Levels{"u1": {Spaces: 2, People: 3}}},
		queue:   &memQueue{rows: map[string]DirtyRow{}},
		now:     &now,
	}
	h.svc = NewService(h.billing, h.queue, func() time.Time { return *h.now }).WithCounter(h.counter)
	return h
}

var _ organization.PlanLimits = (*Service)(nil)

func TestAChangeIsReportedInlineAndTheRowCleared(t *testing.T) {
	h := newHarness(t)
	h.svc.LevelsChanged(context.Background(), "u1")
	if len(h.billing.reports) != 2 {
		t.Fatalf("reports = %+v", h.billing.reports)
	}
	spaces, people := h.billing.reports[0], h.billing.reports[1]
	if spaces.Meter != billingclient.MeterSpaces || spaces.Value != 2 || spaces.CustomerRef != "USER_u1" ||
		people.Meter != billingclient.MeterPeople || people.Value != 3 ||
		spaces.IdempotencyKey != billingclient.LevelKey("u1", billingclient.MeterSpaces, *h.now) {
		t.Fatalf("reports = %+v", h.billing.reports)
	}
	if len(h.queue.rows) != 0 {
		t.Fatalf("rows left: %v", h.queue.rows)
	}
}

// Free included: billing must already know the level the day the person moves
// to Sob demanda (spec § 4). The service never looks at the plan to report.
func TestAReportNeverAsksForThePlan(t *testing.T) {
	h := newHarness(t)
	h.billing.entErr = errors.New("must not be called")
	h.svc.LevelsChanged(context.Background(), "u1")
	if len(h.billing.reports) != 2 {
		t.Fatal("the report depended on the entitlement")
	}
}

// Spec test 8: a failed inline report is delivered by the worker.
func TestAFailedInlineReportIsDeliveredByTheWorker(t *testing.T) {
	h := newHarness(t)
	h.billing.reportErr = errors.New("billing down")
	h.svc.LevelsChanged(context.Background(), "u1")
	if len(h.queue.rows) != 1 {
		t.Fatalf("rows = %v, want the NOW row kept", h.queue.rows)
	}
	h.billing.reportErr = nil
	h.counter.levels["u1"] = organization.Levels{Spaces: 3, People: 3} // changed since: recount at send time
	*h.now = h.now.Add(time.Minute)
	h.svc.ProcessDue(context.Background())
	if len(h.billing.reports) != 2 || h.billing.reports[0].Value != 3 {
		t.Fatalf("reports = %+v", h.billing.reports)
	}
	if len(h.queue.rows) != 0 {
		t.Fatalf("rows left: %v", h.queue.rows)
	}
	if len(h.counter.reconciled) != 1 {
		t.Fatalf("a settled row was not reconciled: %v", h.counter.reconciled)
	}
}

// Spec test 8: a newer change is not deleted by an older success.
func TestANewerChangeIsNotDeletedByAnOlderSuccess(t *testing.T) {
	h := newHarness(t)
	newer := h.now.Add(time.Second)
	h.billing.onReport = func() { _ = h.queue.MarkNow(context.Background(), "u1", newer) }
	h.svc.LevelsChanged(context.Background(), "u1")
	row, ok := h.queue.rows[nowSK("u1")]
	if !ok || !row.DueAt.Equal(newer) {
		t.Fatalf("the newer change was lost: %v", h.queue.rows)
	}
}

// Spec test 8: an invitation's expiry produces a report at expires_at.
func TestAnInvitationExpiryIsReportedWhenDue(t *testing.T) {
	h := newHarness(t)
	at := h.now.Add(7 * 24 * time.Hour)
	h.svc.ScheduleLevels(context.Background(), "u1", at)
	h.svc.ProcessDue(context.Background())
	if len(h.billing.reports) != 0 {
		t.Fatal("reported before the expiry")
	}
	*h.now = at
	h.svc.ProcessDue(context.Background())
	if len(h.billing.reports) != 2 || len(h.queue.rows) != 0 {
		t.Fatalf("reports=%d rows=%v", len(h.billing.reports), h.queue.rows)
	}
}

func TestQuotasWrapBillingFailures(t *testing.T) {
	h := newHarness(t)
	h.billing.entErr = billingclient.ErrUnavailable
	if _, err := h.svc.Quotas(context.Background(), "u1"); !errors.Is(err, organization.ErrPlanUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

// Review Focus 5.
func TestTheStartupCheckLogsWithoutFailing(t *testing.T) {
	logs := captureLogs(t)
	h := newHarness(t)
	h.billing.entErr = billingclient.ErrUnavailable
	if err := h.svc.CheckBilling(context.Background()); err == nil {
		t.Fatal("no error returned")
	}
	if !strings.Contains(logs.String(), "owner_key") {
		t.Fatalf("not logged loudly: %s", logs)
	}
	h.billing.entErr = nil
	h.billing.ent = &billingclient.Entitlements{Default: &billingclient.EntitlementDefault{Plan: "free"}}
	if err := h.svc.CheckBilling(context.Background()); err != nil {
		t.Fatalf("a ready billing failed the check: %v", err)
	}
}
```

`api/internal/domain/planlimit/queue_test.go`:

```go
package planlimit

import (
	"testing"
	"time"
)

func TestQueueKeys(t *testing.T) {
	if nowSK("u1") != "NOW#u1" {
		t.Fatalf("nowSK = %q", nowSK("u1"))
	}
	at := time.Unix(1760104980, 0)
	if got := atSK(at, "u1"); got != "AT#001760104980#u1" {
		t.Fatalf("atSK = %q", got)
	}
	// Due's upper bound includes every row due at or before now, and none after.
	hi := atUpperBound(at)
	if !(atSK(at, "zzzz") <= hi) || !(atSK(at.Add(time.Second), "a") > hi) {
		t.Fatalf("bound %q is wrong", hi)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/domain/planlimit/ -v`
Expected: FAIL — package has no non-test files / `undefined: QuotasFrom`.

- [ ] **Step 3: Write minimal implementation**

`quotas.go`:

```go
// Package planlimit connects the organization domain to ctech-billing: it turns
// entitlements into quotas, and keeps billing told of every owner's levels
// through a small durable queue (docs/specs/2026-10-10-space-plan-limits.md).
package planlimit

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"gopkg.aoctech.app/api-commons/observability"

	"gopkg.aoctech.app/account/api/internal/billingclient"
	"gopkg.aoctech.app/account/api/internal/domain/organization"
)

const (
	metaQuotaSpaces = "quota_spaces"
	metaQuotaPeople = "quota_people_per_space"
)

// QuotasFrom reads the plan from an entitlement answer (decision P4): the
// entitled subscription's item metadata, the most generous if several, else
// billing's default (Free). Neither → unavailable: that is a billing without
// owner_key support, and treating it as 0 would refuse everyone silently.
func QuotasFrom(ctx context.Context, e *billingclient.Entitlements) (organization.Quotas, error) {
	var chosen *organization.Quotas
	for _, sub := range e.Subscriptions {
		if !sub.Entitled {
			continue
		}
		meta := metadataOf(sub.Items)
		q := organization.Quotas{
			Plan:           sub.Plan,
			Spaces:         quota(ctx, meta, metaQuotaSpaces, sub.Plan),
			PeoplePerSpace: quota(ctx, meta, metaQuotaPeople, sub.Plan),
		}
		if chosen == nil || moreGenerous(q, *chosen) {
			chosen = &q
		}
	}
	if chosen != nil {
		return *chosen, nil
	}
	if e.Default == nil {
		return organization.Quotas{}, fmt.Errorf("%w: no entitled subscription and no default plan", organization.ErrPlanUnavailable)
	}
	return organization.Quotas{
		Plan:           e.Default.Plan,
		Spaces:         quota(ctx, e.Default.Metadata, metaQuotaSpaces, e.Default.Plan),
		PeoplePerSpace: quota(ctx, e.Default.Metadata, metaQuotaPeople, e.Default.Plan),
	}, nil
}

func metadataOf(items []billingclient.EntitlementItem) map[string]string {
	for _, it := range items {
		if _, ok := it.Metadata[metaQuotaSpaces]; ok {
			return it.Metadata
		}
	}
	if len(items) > 0 {
		return items[0].Metadata
	}
	return nil
}

// quota parses one value. Missing, not an integer, or below -1 → 0, logged: a
// catalogue mistake must not turn into unlimited spaces (spec § 1).
func quota(ctx context.Context, meta map[string]string, key, plan string) int64 {
	raw, ok := meta[key]
	v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if !ok || err != nil || v < organization.Unlimited {
		observability.Error(ctx, "plan limits: malformed quota in billing metadata, treating as 0", err,
			"key", key, "plan", plan, "value", raw)
		return 0
	}
	return v
}

func rank(limit int64) int64 {
	if limit == organization.Unlimited {
		return math.MaxInt64
	}
	return limit
}

func moreGenerous(a, b organization.Quotas) bool {
	if rank(a.Spaces) != rank(b.Spaces) {
		return rank(a.Spaces) > rank(b.Spaces)
	}
	return rank(a.PeoplePerSpace) > rank(b.PeoplePerSpace)
}
```

`queue.go`:

```go
package planlimit

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/account/api/internal/database"
)

// The queue lives in account_organizations, beside the counters (decision P1).
const (
	queueTable  = "account_organizations"
	dirtyPK     = "LEVEL_DIRTY"
	nowSKPrefix = "NOW#"
	atSKPrefix  = "AT#"
	attrOwner   = "owner_user_id"
	attrDueAt   = "due_at"
)

func nowSK(owner string) string { return nowSKPrefix + owner }

func atSK(at time.Time, owner string) string {
	return fmt.Sprintf("%s%012d#%s", atSKPrefix, at.Unix(), owner)
}

// atUpperBound sorts after every AT# key due at or before now and before any
// due later: keys always continue with "#owner" after the twelve digits.
func atUpperBound(now time.Time) string { return fmt.Sprintf("%s%012d", atSKPrefix, now.Unix()+1) }

type DirtyRow struct {
	SK          string
	OwnerUserID string
	DueAt       time.Time
	Scheduled   bool
}

type Queue interface {
	MarkNow(ctx context.Context, owner string, at time.Time) error
	Schedule(ctx context.Context, owner string, at time.Time) error
	Due(ctx context.Context, now time.Time, limit int) ([]DirtyRow, error)
	// Done deletes a delivered row. A NOW row only if due_at is still the one
	// delivered — false, nil when a newer change replaced it.
	Done(ctx context.Context, row DirtyRow) (bool, error)
}

type dynamoQueue struct {
	base database.Base
	name string
}

func NewQueue(db *dynamodb.Client, tablePrefix string) Queue {
	return &dynamoQueue{
		base: database.NewBase(db, tablePrefix, queueTable),
		name: database.TableName(tablePrefix, queueTable),
	}
}

func s(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }
func n(v int64) types.AttributeValue  { return &types.AttributeValueMemberN{Value: strconv.FormatInt(v, 10)} }

func (q *dynamoQueue) put(ctx context.Context, sk, owner string, due time.Time) error {
	return q.base.PutItem(ctx, map[string]types.AttributeValue{
		"pk": s(dirtyPK), "sk": s(sk), attrOwner: s(owner), attrDueAt: n(due.UnixMilli()),
	})
}

func (q *dynamoQueue) MarkNow(ctx context.Context, owner string, at time.Time) error {
	return q.put(ctx, nowSK(owner), owner, at)
}

func (q *dynamoQueue) Schedule(ctx context.Context, owner string, at time.Time) error {
	return q.put(ctx, atSK(at, owner), owner, at)
}

func (q *dynamoQueue) query(ctx context.Context, cond string, values map[string]types.AttributeValue, limit int) ([]DirtyRow, error) {
	values[":pk"] = s(dirtyPK)
	out, err := q.base.QueryRaw(ctx, &dynamodb.QueryInput{
		TableName:                 aws.String(q.name),
		KeyConditionExpression:    aws.String("pk = :pk AND " + cond),
		ExpressionAttributeValues: values,
		ConsistentRead:            aws.Bool(true),
		Limit:                     aws.Int32(int32(limit)),
	})
	if err != nil {
		return nil, fmt.Errorf("reading level queue: %w", err)
	}
	rows := make([]DirtyRow, 0, len(out.Items))
	for _, item := range out.Items {
		sk, _ := item["sk"].(*types.AttributeValueMemberS)
		owner, _ := item[attrOwner].(*types.AttributeValueMemberS)
		due, _ := item[attrDueAt].(*types.AttributeValueMemberN)
		if sk == nil || owner == nil || due == nil {
			continue
		}
		ms, _ := strconv.ParseInt(due.Value, 10, 64)
		rows = append(rows, DirtyRow{
			SK: sk.Value, OwnerUserID: owner.Value, DueAt: time.UnixMilli(ms).UTC(),
			Scheduled: strings.HasPrefix(sk.Value, atSKPrefix),
		})
	}
	return rows, nil
}

func (q *dynamoQueue) Due(ctx context.Context, now time.Time, limit int) ([]DirtyRow, error) {
	rows, err := q.query(ctx, "begins_with(sk, :p)", map[string]types.AttributeValue{":p": s(nowSKPrefix)}, limit)
	if err != nil || len(rows) >= limit {
		return rows, err
	}
	scheduled, err := q.query(ctx, "sk BETWEEN :lo AND :hi",
		map[string]types.AttributeValue{":lo": s(atSKPrefix), ":hi": s(atUpperBound(now))}, limit-len(rows))
	if err != nil {
		return nil, err
	}
	return append(rows, scheduled...), nil
}

func (q *dynamoQueue) Done(ctx context.Context, row DirtyRow) (bool, error) {
	input := &dynamodb.DeleteItemInput{
		TableName: aws.String(q.name),
		Key:       map[string]types.AttributeValue{"pk": s(dirtyPK), "sk": s(row.SK)},
	}
	if !row.Scheduled {
		input.ConditionExpression = aws.String("#d = :due")
		input.ExpressionAttributeNames = map[string]string{"#d": attrDueAt}
		input.ExpressionAttributeValues = map[string]types.AttributeValue{":due": n(row.DueAt.UnixMilli())}
	}
	_, err := q.base.DeleteItemRaw(ctx, input)
	if database.IsConditionFailed(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("clearing level queue row: %w", err)
	}
	return true, nil
}
```

`service.go`:

```go
package planlimit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gopkg.aoctech.app/api-commons/observability"

	"gopkg.aoctech.app/account/api/internal/billingclient"
	"gopkg.aoctech.app/account/api/internal/domain/organization"
)

const (
	WorkerInterval = time.Minute
	// drainBatch keeps one tick (two reports of ≤2 s each per row, usually
	// far less) inside the minute the Valkey lock covers.
	drainBatch = 100
	// inlineBudget bounds the report a request makes after its commit.
	inlineBudget = 2 * time.Second
	// reconcileSettle: rows at least this old have a settled membership index.
	reconcileSettle = 30 * time.Second
	startupTimeout  = 10 * time.Second
	// startupProbeRef is a customer billing never has: with owner_key support
	// the answer is 200 with a default; without it, 404.
	startupProbeRef = "USER_ctech-account-startup-probe"
)

type Billing interface {
	Entitlements(ctx context.Context, customerRef, ownerKey string) (*billingclient.Entitlements, error)
	ReportLevel(ctx context.Context, r billingclient.LevelReport) error
}

type LevelCounter interface {
	Levels(ctx context.Context, ownerUserID string) (organization.Levels, error)
	ReconcileSpaceCounter(ctx context.Context, ownerUserID string) error
}

type Service struct {
	billing Billing
	queue   Queue
	counter LevelCounter
	now     func() time.Time
}

func NewService(b Billing, q Queue, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{billing: b, queue: q, now: now}
}

// WithCounter closes the loop with the organization service, which is built
// first and handed this service as its PlanLimits.
func (s *Service) WithCounter(c LevelCounter) *Service {
	s.counter = c
	return s
}

// Quotas reads the owner's plan live — no cache, so an upgrade lifts the
// limit at once (spec § 3.1).
func (s *Service) Quotas(ctx context.Context, ownerUserID string) (organization.Quotas, error) {
	e, err := s.billing.Entitlements(ctx, billingclient.CustomerRef(ownerUserID), billingclient.OwnerKeyFinance)
	if err != nil {
		return organization.Quotas{}, fmt.Errorf("%w: %v", organization.ErrPlanUnavailable, err)
	}
	return QuotasFrom(ctx, e)
}

// LevelsChanged marks the owner dirty, then tries to report right away. It
// never fails the caller: the write it follows already committed (spec § 4).
func (s *Service) LevelsChanged(ctx context.Context, ownerUserID string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), inlineBudget)
	defer cancel()
	at := s.now().UTC()
	if err := s.queue.MarkNow(ctx, ownerUserID, at); err != nil {
		observability.Error(ctx, "plan levels: marking the owner dirty failed", err, "owner", ownerUserID)
	}
	if err := s.report(ctx, ownerUserID); err != nil {
		observability.Warn(ctx, "plan levels: inline report failed; the worker will retry", err, "owner", ownerUserID)
		return
	}
	if _, err := s.queue.Done(ctx, DirtyRow{SK: nowSK(ownerUserID), OwnerUserID: ownerUserID, DueAt: at}); err != nil {
		observability.Warn(ctx, "plan levels: clearing the reported row failed", err, "owner", ownerUserID)
	}
}

func (s *Service) ScheduleLevels(ctx context.Context, ownerUserID string, at time.Time) {
	if err := s.queue.Schedule(ctx, ownerUserID, at.UTC()); err != nil {
		observability.Error(ctx, "plan levels: scheduling a report failed", err, "owner", ownerUserID)
	}
}

// report recounts from the source rows and sends both meters, whatever the
// plan. occurred_at is the instant of the count.
func (s *Service) report(ctx context.Context, ownerUserID string) error {
	lv, err := s.counter.Levels(ctx, ownerUserID)
	if err != nil {
		return err
	}
	at := s.now().UTC()
	for _, m := range []struct {
		meter string
		value int64
	}{{billingclient.MeterSpaces, lv.Spaces}, {billingclient.MeterPeople, lv.People}} {
		if err := s.billing.ReportLevel(ctx, billingclient.LevelReport{
			CustomerRef:    billingclient.CustomerRef(ownerUserID),
			Meter:          m.meter,
			Value:          m.value,
			OccurredAt:     at,
			IdempotencyKey: billingclient.LevelKey(ownerUserID, m.meter, at),
		}); err != nil {
			return err
		}
	}
	return nil
}

// ProcessDue drains due rows: report, reconcile a settled spaces counter,
// clear. A row whose report fails stays for the next tick.
func (s *Service) ProcessDue(ctx context.Context) {
	now := s.now().UTC()
	rows, err := s.queue.Due(ctx, now, drainBatch)
	if err != nil {
		observability.Error(ctx, "plan levels: reading the queue failed", err)
		return
	}
	for _, row := range rows {
		if err := s.report(ctx, row.OwnerUserID); err != nil {
			observability.Warn(ctx, "plan levels: report failed; kept for the next tick", err, "owner", row.OwnerUserID)
			continue
		}
		if row.Scheduled || now.Sub(row.DueAt) >= reconcileSettle {
			if err := s.counter.ReconcileSpaceCounter(ctx, row.OwnerUserID); err != nil {
				observability.Warn(ctx, "plan levels: reconciling the spaces counter failed", err, "owner", row.OwnerUserID)
			}
		}
		if _, err := s.queue.Done(ctx, row); err != nil {
			observability.Warn(ctx, "plan levels: clearing a delivered row failed", err, "owner", row.OwnerUserID)
		}
	}
}

var errNoOwnerKeySupport = errors.New("billing answered without a default plan")

// CheckBilling is the startup check of deploy order step 1: logs loudly and
// returns an error, never stops the process — this service is the platform's
// auth backbone, and billing being early or late must not take sign-in down.
func (s *Service) CheckBilling(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	e, err := s.billing.Entitlements(ctx, startupProbeRef, billingclient.OwnerKeyFinance)
	if err == nil && e.Default == nil {
		err = errNoOwnerKeySupport
	}
	if err != nil {
		observability.Error(ctx, "PLAN LIMITS NOT READY: ctech-billing does not answer entitlements with owner_key=finance "+
			"and a default plan (or the account-billing client is not usable). Every create, invite and transfer of a "+
			"personal space will answer 503 until ctech-billing ships its plans step "+
			"(docs/specs/2026-10-10-space-plan-limits.md, Deploy order).", err)
		return err
	}
	return nil
}

// RunWorker drains the queue on whichever instance wins tryLock for the tick
// (deletion.RunWorker's pattern).
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
				observability.Error(ctx, "plan levels worker: lock failed", err)
				continue
			}
			if ok {
				svc.ProcessDue(ctx)
			}
		}
	}
}
```

Add a compile-time check that the organization service satisfies the counter, in `service.go` of `planlimit`:

```go
var _ LevelCounter = (*organization.Service)(nil)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/domain/planlimit/ -v && go vet ./internal/domain/planlimit/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/planlimit/
git commit -m "feat(planlimit): quotas from entitlements, the LEVEL_DIRTY queue, inline reports and the drain worker"
```

---

### Task 8: Counts on the internal user-organizations route

**Files:**
- Modify: `api/internal/handler/organization_internal.go:62-72`
- Test: `api/internal/handler/organization_internal_counts_test.go`

**Interfaces:**
- Consumes: `(*organization.Service).SpaceCounts` (Task 1); `newInternalMembershipApp`, `issueServiceToken`, `registerUser`, `decodeJSON`, `bodyString` (existing); `scopes.InternalAccountUserOrganizations`.
- Produces: each entry of `GET /internal/users/:user_id/organizations` that is a `personal` workspace **owned** by `:user_id` carries `"people"` (members, owner excluded) and `"pending_invitations"` (unexpired); no other entry carries either key.

This costs two `Query`s per owned space (members and invitations), not the one the spec estimates — `ListWorkspaces` reads no member list today. Bounded by `quota_spaces`.

- [ ] **Step 1: Write the failing test** — `api/internal/handler/organization_internal_counts_test.go`

```go
package handler_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	orgDomain "gopkg.aoctech.app/account/api/internal/domain/organization"
	"gopkg.aoctech.app/account/api/internal/scopes"
)

// Spec test 11.
func TestCountsOnlyOnOwnedPersonalSpaces(t *testing.T) {
	a := newInternalMembershipApp(t)
	ctx := context.Background()
	owner := a.registerUser(t, "counts-owner@example.com", "Sup3rSecret!pass", "Dono")
	other := a.registerUser(t, "counts-other@example.com", "Sup3rSecret!pass", "Outra")
	mine, _ := a.orgSvc.CreateOfKind(ctx, orgDomain.KindPersonal, owner.ID(), "Dono", "Casa")
	theirs, _ := a.orgSvc.CreateOfKind(ctx, orgDomain.KindPersonal, other.ID(), "Outra", "Dela")
	org, _ := a.orgSvc.Create(ctx, owner.ID(), "Dono", "CTech")
	for _, m := range []*orgDomain.Membership{
		{OrganizationID: mine.ID, UserID: "usr_a", Role: orgDomain.RoleMember},
		{OrganizationID: mine.ID, UserID: "usr_b", Role: orgDomain.RoleViewer},
		{OrganizationID: theirs.ID, UserID: owner.ID(), Role: orgDomain.RoleMember},
	} {
		m.CreatedAt = time.Now()
		if err := a.orgRepo.PutMembership(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	_ = a.orgRepo.PutInvitation(ctx, &orgDomain.Invitation{OrganizationID: mine.ID, Email: "c@example.com", Role: orgDomain.RoleMember, TokenHash: "h1", ExpiresAt: time.Now().Add(time.Hour)})
	_ = a.orgRepo.PutInvitation(ctx, &orgDomain.Invitation{OrganizationID: mine.ID, Email: "old@example.com", Role: orgDomain.RoleMember, TokenHash: "h2", ExpiresAt: time.Now().Add(-time.Hour)})

	internal := a.issueServiceToken(t, []string{scopes.InternalAccountUserOrganizations})
	resp := a.do(t, http.MethodGet, "/v1.0/internal/users/"+owner.ID()+"/organizations", internal, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%s)", resp.StatusCode, bodyString(resp))
	}
	var body struct {
		Organizations []map[string]any `json:"organizations"`
	}
	decodeJSON(t, resp, &body)
	byID := map[string]map[string]any{}
	for _, w := range body.Organizations {
		byID[w["id"].(string)] = w
	}
	if w := byID[mine.ID]; w["people"] != float64(2) || w["pending_invitations"] != float64(1) {
		t.Fatalf("owned space = %v, want people 2, pending 1", w)
	}
	for _, id := range []string{theirs.ID, org.ID} {
		if _, present := byID[id]["people"]; present {
			t.Fatalf("%s carries counts: %v", id, byID[id])
		}
		if _, present := byID[id]["pending_invitations"]; present {
			t.Fatalf("%s carries counts: %v", id, byID[id])
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/handler/ -run TestCountsOnlyOnOwnedPersonalSpaces -v`
Expected: FAIL — `owned space = map[…] , want people 2, pending 1`.

- [ ] **Step 3: Write minimal implementation** — replace the loop in `internalUserOrganizations`:

```go
	userID := c.Params("user_id")
	workspaces, err := h.svc.ListWorkspaces(c.Context(), userID)
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err).Send(c)
	}
	out := make([]fiber.Map, 0, len(workspaces))
	for _, w := range workspaces {
		item := fiber.Map{"id": w.ID, "display_name": w.DisplayName, "kind": w.Kind, "role": w.Role}
		// Counts for billing's plan screen (spec § 6): only on the person's own
		// spaces — a member gets no counts for somebody else's.
		if w.Kind == organization.KindPersonal && w.Role == organization.RoleOwner && w.OwnerUserID == userID {
			people, pending, err := h.svc.SpaceCounts(c.Context(), w.ID)
			if err != nil {
				return apierror.ServerError(c.Path()).WithCause(err).Send(c)
			}
			item["people"], item["pending_invitations"] = people, pending
		}
		out = append(out, item)
	}
	return c.JSON(fiber.Map{"organizations": out})
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/handler/ -v -run 'Internal|Counts|Kind'`
Expected: PASS, the existing internal-route tests included.

- [ ] **Step 5: Commit**

```bash
git add api/internal/handler/organization_internal.go api/internal/handler/organization_internal_counts_test.go
git commit -m "feat(organization): people and pending counts on owned spaces in the internal user list"
```

---

### Task 9: Wiring, startup check, CDK, and documentation

**Files:**
- Modify: `api/internal/config/config.go` (field + `Load`), `api/internal/config/config_test.go`
- Modify: `api/cmd/api/main.go` (after `oauthClientOperator`, ~line 213)
- Modify: `cdk/lib/api-stack.ts` (`ApiStackProps.billingApiUrl`, static env), `cdk/bin/ctech-account.ts` (pass it), `cdk/test/compute-stack.test.ts`
- Modify: `README.md` (env table near line 610; routes table near lines 131–132; a "Plan limits on personal spaces" section), `PLAN.md` (new checklist section)

**Interfaces:**
- Consumes: `billingclient.New`, `NewSelfSigned` (Tasks 5–6), `planlimit.NewService`, `NewQueue`, `RunWorker`, `WorkerInterval`, `(*Service).CheckBilling` (Task 7), `orgDomain.NewCounterRepository`, `(*Service).WithPlanLimits`, `.WithEmailOwner` (Tasks 2–3), `userSvc.GetByEmail`, `userDomain.ErrNotFound`, `valkeyClient.SetNX/Enabled` (existing).
- Produces: `config.Config.BillingAPIURL` (`BILLING_API_URL`); the worker lock key `plan_levels_worker_lock:{env}`; CDK env `BILLING_API_URL=https://billing-api[-env].aoctech.app`.

- [ ] **Step 1: Write the failing tests**

`config_test.go`:

```go
func TestLoadReadsTheBillingAPIURL(t *testing.T) {
	setWebAuthnTestEnv(t)
	t.Setenv("BILLING_API_URL", "https://billing-api.example")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BillingAPIURL != "https://billing-api.example" {
		t.Fatalf("BillingAPIURL = %q", cfg.BillingAPIURL)
	}
}
```

`cdk/test/compute-stack.test.ts` — add `billingApiUrl: 'https://billing-api.aoctech.app',` to the `ApiStack` props in `synth()`, and:

```ts
test('the API knows where billing is (plan limits)', () => {
  expect(userDataText()).toContain('BILLING_API_URL=https://billing-api.aoctech.app')
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd api && go test ./internal/config/ -run TestLoadReadsTheBillingAPIURL -v` → FAIL (`cfg.BillingAPIURL undefined`).
Run: `cd cdk && npx jest test/compute-stack.test.ts` → FAIL (TypeScript: `billingApiUrl` not in `ApiStackProps`).

- [ ] **Step 3: Implement**

`config.go` — field, after `ErasureServices`:

```go
	// BillingAPIURL is ctech-billing's API (plan limits on personal spaces,
	// docs/specs/2026-10-10-space-plan-limits.md). Empty disables plan limits
	// and level reports entirely — today's behaviour.
	BillingAPIURL string
```

and in `Load`'s literal: `BillingAPIURL: strings.TrimSuffix(os.Getenv("BILLING_API_URL"), "/"),`.

`cmd/api/main.go` — after `oauthClientOperator := …` (imports: `"gopkg.aoctech.app/account/api/internal/billingclient"`, `"gopkg.aoctech.app/account/api/internal/domain/planlimit"`):

```go
	// Plan limits on personal spaces. Dark until BILLING_API_URL is set, which
	// is deploy step 2 (docs/specs/2026-10-10-space-plan-limits.md).
	if cfg.BillingAPIURL != "" {
		billingTokens := billingclient.NewSelfSigned(jwtSvc, oauthClientRepo, scopesCatalogSvc, cfg.AppURL, cfg.Audience, time.Now)
		planSvc := planlimit.NewService(billingclient.New(cfg.BillingAPIURL, billingTokens),
			planlimit.NewQueue(db, cfg.TablePrefix), time.Now).WithCounter(orgSvc)
		orgSvc = orgSvc.
			WithPlanLimits(planSvc, orgDomain.NewCounterRepository(db, cfg.TablePrefix)).
			WithEmailOwner(func(ctx context.Context, email string) (string, error) {
				u, err := userSvc.GetByEmail(ctx, email)
				if errors.Is(err, userDomain.ErrNotFound) {
					return "", nil
				}
				if err != nil {
					return "", err
				}
				return u.ID(), nil
			})
		go func() { _ = planSvc.CheckBilling(ctx) }()
		planLockKey := "plan_levels_worker_lock:" + cfg.Environment
		go planlimit.RunWorker(ctx, planSvc, func(ctx context.Context) (bool, error) {
			if !valkeyClient.Enabled() {
				return true, nil // dev: single instance
			}
			return valkeyClient.SetNX(ctx, planLockKey, "1", planlimit.WorkerInterval-5*time.Second)
		}, planlimit.WorkerInterval)
	} else {
		log.Println("BILLING_API_URL not set — plan limits on personal spaces disabled")
	}
```

(`errors` is added to the imports if absent.)

`cdk/lib/api-stack.ts` — in `ApiStackProps`:

```ts
  // ctech-billing's API. Setting it switches plan limits on personal spaces on
  // (deploy step 2 of docs/specs/2026-10-10-space-plan-limits.md); absent keeps
  // them off.
  billingApiUrl?: string;
```

destructure `billingApiUrl` from props, and in the `app-static.env` heredoc, after `TRUSTED_PROXIES=127.0.0.1`:

```ts
      ...(billingApiUrl ? [`BILLING_API_URL=${billingApiUrl}`] : []),
```

`cdk/bin/ctech-account.ts` — in the `ApiStack` props: `billingApiUrl: \`https://${domainForEnv(ENVIRONMENT, 'billing-api')}\`,`.

`README.md`:
- env table: `| BILLING_API_URL | No | ctech-billing's API (e.g. https://billing-api.aoctech.app). Set → plan limits on personal spaces and level reports to billing; the account signs its own token as OAuth client account-billing (register it with cmd/createclient). Unset → no limits (dev). |`
- routes table: `GET /v1.0/organizations/:id/plan-usage` (first-party, owner of a space) → `{"people","pending_invitations","limit","plan"}` (`limit` −1 = unlimited; 404 on an organization; 503 `plan_unavailable`); amend the `GET /v1.0/internal/users/:user_id/organizations` row: owned `personal` entries also carry `people` and `pending_invitations`.
- a short "Plan limits on personal spaces" section: 402 `plan_limit` / 503 `plan_unavailable` on create, invite and transfer of a space; `LEVEL_DIRTY` queue rows `NOW#{owner}` and `AT#{unix}#{owner}` in `account_organizations`; worker lock `plan_levels_worker_lock:{env}`; the ops step to register the client:
  `AWS_REGION=us-east-1 TABLE_PREFIX=prod go run ./cmd/createclient -client-id account-billing -name "ctech-account plan limits" -scopes billing:entitlements:read,billing:usage:write` — the printed secret is not used (the token is signed in process) and can be discarded.

`PLAN.md` — new section after "Account Deletion (LGPD)":

```markdown
## Plan limits on personal spaces

Spec: `docs/specs/2026-10-10-space-plan-limits.md`. Plan: `docs/plans/2026-10-10-space-plan-limits.md`.

- [x] Live entitlement read (2 s), quotas from billing metadata, malformed → 0
- [x] Guarded create / invite / transfer of a space (402 `plan_limit`, 503 `plan_unavailable`)
- [x] `LEVEL_DIRTY` queue, inline reports, minute worker
- [x] Counts on the internal user-organizations route
- [ ] Deploy: `account-billing` client registered; `BILLING_API_URL` after ctech-billing step 1
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api && go build ./... && go vet ./... && go test ./...`
Expected: every package `ok`.
Run: `cd cdk && npx jest test/compute-stack.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api/internal/config/ api/cmd/api/main.go cdk/lib/api-stack.ts cdk/bin/ctech-account.ts cdk/test/compute-stack.test.ts README.md PLAN.md
git commit -m "feat: wire plan limits behind BILLING_API_URL, with a startup check and the level worker"
```

---

## UI tasks (`ui/`)

All commands run from `ui/`. **Load the `/impeccable` skill before starting each UI task** (user rule) and let it review the states this task adds (refusal, unavailable, counter) for hierarchy, copy and touch targets. Vitest is judged by its exit code: `npx vitest run --maxWorkers=2 <files>; echo "exit=$?"`.

### Task 10: Plan problems, the billing link, the usage query and the copy

**Files:**
- Modify: `ui/src/lib/env.ts`, `ui/src/lib/constants.ts`, `ui/src/lib/types.ts`, `ui/src/lib/queries.ts`, `ui/src/lib/mock.ts` (route list at line 346)
- Create: `ui/src/lib/plan-problem.ts`, `ui/src/lib/plan-problem.test.ts`
- Modify: `ui/src/locales/pt-BR.json`, `ui/src/locales/en.json` (`spaces.plan`)
- Modify: `.github/workflows/frontend.yml` (build env per environment)

**Interfaces:**
- Consumes: `isAxiosError` (`lib/axios.ts:110`), `api` (`lib/axios.ts`).
- Produces:
  - `BILLING_URL` (`lib/env.ts`, from `NEXT_PUBLIC_BILLING_URL`); `BILLING_PLAN_PATH = '/finance/plano'` (`lib/constants.ts`).
  - `type PlanProblem = {kind: 'limit'; resource: 'spaces' | 'people'; limit?: number; used?: number; plan?: string} | {kind: 'unavailable'}`; `planProblemOf(error: unknown): PlanProblem | null`; `planURL(): string | null`.
  - `interface SpaceUsage {people: number; pending_invitations: number; limit: number; plan?: string}`; `fetchSpaceUsage(id: string): Promise<SpaceUsage>`.
  - i18n keys `spaces.plan.spacesLimit_one|_other`, `peopleLimit`, `unavailable`, `transferLimit`, `seePlans`, `back`, `usage`, `usageUnlimited`.

- [ ] **Step 1: Write the failing test** — `ui/src/lib/plan-problem.test.ts`

```ts
import {AxiosError, AxiosHeaders} from 'axios'
import {describe, expect, it, vi} from 'vitest'
import {planProblemOf, planURL} from './plan-problem'

vi.mock('@/lib/env', async (orig) => ({...(await orig<object>()), BILLING_URL: 'https://billing.example'}))

function problem(status: number, data: unknown) {
  return new AxiosError('x', String(status), undefined, undefined, {
    status, data, statusText: '', headers: {}, config: {headers: new AxiosHeaders()},
  })
}

describe('planProblemOf', () => {
  it('reads a plan limit with its numbers', () => {
    expect(planProblemOf(problem(402, {code: 'plan_limit', resource: 'spaces', limit: 1, used: 1, plan: 'free'})))
      .toEqual({kind: 'limit', resource: 'spaces', limit: 1, used: 1, plan: 'free'})
  })

  it('reads a transfer refusal that carries no numbers', () => {
    expect(planProblemOf(problem(402, {code: 'plan_limit', resource: 'spaces'})))
      .toEqual({kind: 'limit', resource: 'spaces', limit: undefined, used: undefined, plan: undefined})
  })

  it('reads an unavailable plan', () => {
    expect(planProblemOf(problem(503, {code: 'plan_unavailable'}))).toEqual({kind: 'unavailable'})
  })

  it('ignores every other failure', () => {
    expect(planProblemOf(problem(503, {code: 'service_unavailable'}))).toBeNull()
    expect(planProblemOf(problem(403, {}))).toBeNull()
    expect(planProblemOf(new Error('x'))).toBeNull()
  })

  it('builds the plans link on the billing app', () => {
    expect(planURL()).toBe('https://billing.example/finance/plano')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run --maxWorkers=2 src/lib/plan-problem.test.ts; echo "exit=$?"`
Expected: `exit=1` — cannot resolve `./plan-problem`.

- [ ] **Step 3: Implement**

`lib/env.ts`:

```ts
// The billing app (not its API): where "Ver planos" sends somebody. Empty
// hides the link rather than pointing it at nowhere.
export const BILLING_URL = (process.env.NEXT_PUBLIC_BILLING_URL ?? '').replace(/\/$/, '')
```

`lib/constants.ts`: `export const BILLING_PLAN_PATH = '/finance/plano'`.

`lib/plan-problem.ts`:

```ts
import {isAxiosError} from '@/lib/axios'
import {BILLING_URL} from '@/lib/env'
import {BILLING_PLAN_PATH} from '@/lib/constants'

/** A write refused by the Finanças plan, or a plan that could not be read. */
export type PlanProblem =
  | {kind: 'limit'; resource: 'spaces' | 'people'; limit?: number; used?: number; plan?: string}
  | {kind: 'unavailable'}

interface PlanProblemBody {
  code?: string
  resource?: string
  limit?: number
  used?: number
  plan?: string
}

/** By the problem's `code`, never its prose: the detail is English and for logs. */
export function planProblemOf(error: unknown): PlanProblem | null {
  if (!isAxiosError(error)) return null
  const status = error.response?.status
  const data = error.response?.data as PlanProblemBody | undefined
  if (status === 402 && data?.code === 'plan_limit') {
    return {
      kind: 'limit',
      resource: data.resource === 'people' ? 'people' : 'spaces',
      limit: data.limit,
      used: data.used,
      plan: data.plan,
    }
  }
  if (status === 503 && data?.code === 'plan_unavailable') return {kind: 'unavailable'}
  return null
}

/** The Finanças plan page, or null when the build does not know the billing app. */
export function planURL(): string | null {
  return BILLING_URL ? `${BILLING_URL}${BILLING_PLAN_PATH}` : null
}
```

`lib/types.ts`:

```ts
/** The people page's counter: people + pending of limit. `limit` −1 is unlimited. */
export interface SpaceUsage {
  people: number
  pending_invitations: number
  limit: number
  plan?: string
}
```

`lib/queries.ts` (add `SpaceUsage` to the type import):

```ts
export async function fetchSpaceUsage(id: string): Promise<SpaceUsage> {
  const { data } = await api.get<SpaceUsage>(`/v1.0/organizations/${encodeURIComponent(id)}/plan-usage`)
  return data
}
```

`lib/mock.ts` — in `routes`, before the invitations GET:

```ts
  {
    method: 'get',
    pattern: /^\/v1\.0\/organizations\/([^/]+)\/plan-usage$/,
    handle: (m) => ({ people: 0, pending_invitations: (state.invitations[m[1]] ?? []).length, limit: -1, plan: 'mock' }),
  },
```

`locales/pt-BR.json` — inside `spaces`:

```json
"plan": {
  "spacesLimit_one": "Seu plano permite {{count}} espaço e você já tem {{used}}.",
  "spacesLimit_other": "Seu plano permite {{count}} espaços e você já tem {{used}}.",
  "peopleLimit": "Este espaço já tem {{used}} de {{limit}} pessoas do seu plano.",
  "unavailable": "Não foi possível verificar seu plano agora. Tente em instantes.",
  "transferLimit": "{{name}} já está no limite de espaços do plano.",
  "seePlans": "Ver planos",
  "back": "Voltar",
  "usage": "{{people}} + {{pending}} de {{limit}} pessoas",
  "usageUnlimited": "{{people}} + {{pending}} pessoas"
}
```

`locales/en.json` — inside `spaces`:

```json
"plan": {
  "spacesLimit_one": "Your plan allows {{count}} space and you already have {{used}}.",
  "spacesLimit_other": "Your plan allows {{count}} spaces and you already have {{used}}.",
  "peopleLimit": "This space already has {{used}} of {{limit}} people on your plan.",
  "unavailable": "We couldn't check your plan right now. Try again in a moment.",
  "transferLimit": "{{name}} is already at their plan's space limit.",
  "seePlans": "See plans",
  "back": "Back",
  "usage": "{{people}} + {{pending}} of {{limit}} people",
  "usageUnlimited": "{{people}} + {{pending}} people"
}
```

`.github/workflows/frontend.yml` — add to each `build-env-*` block: dev `NEXT_PUBLIC_BILLING_URL=https://billing-dev.aoctech.app`, stage `NEXT_PUBLIC_BILLING_URL=https://billing-stage.aoctech.app`, prod `NEXT_PUBLIC_BILLING_URL=https://billing.aoctech.app` (a link, not a fetch: no `connect-src` change).

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run --maxWorkers=2 src/lib/plan-problem.test.ts; echo "exit=$?"` → `exit=0`.
Run: `npx tsc --noEmit && npm run lint` → no errors.

- [ ] **Step 5: Commit**

```bash
git add ui/src/lib/ ui/src/locales/ .github/workflows/frontend.yml
git commit -m "feat(ui): plan problems, the plans link and the space usage query"
```

---

### Task 11: `/account/spaces/new` refused by the plan

**Files:**
- Modify: `ui/src/app/account/spaces/new/page.tsx`
- Test: `ui/src/app/account/spaces/new/page.test.tsx`

**Interfaces:**
- Consumes: `planProblemOf`, `planURL` (Task 10); the page's existing `leave('cancelled')`, `handoff`, `isHandoff`.
- Produces: on 402 the form is replaced by the limit message with **Ver planos** (`planURL()`, hidden when null) and **Voltar** (`leave('cancelled')` in a handoff — `return_to?cancelled=1&state=…` — else a link to `/account/spaces`); on 503 the form stays with the unavailable message in its alert.

- [ ] **Step 1: Write the failing test** — add to `page.test.tsx` (imports `AxiosError, AxiosHeaders` from `axios`; mock `@/lib/env`):

```tsx
vi.mock('@/lib/env', async (orig) => ({...(await orig<object>()), BILLING_URL: 'https://billing.example'}))

function refusal(status: number, data: unknown) {
  return new AxiosError('x', String(status), undefined, undefined, {
    status, data, statusText: '', headers: {}, config: {headers: new AxiosHeaders()},
  })
}

  it('replaces the form with the plan limit and a way to the plans', async () => {
    vi.mocked(createOrganizationAPI).mockRejectedValue(
      refusal(402, {code: 'plan_limit', resource: 'spaces', limit: 3, used: 3, plan: 'basic'}))
    const user = userEvent.setup()
    renderPage('client_id=billing&return_to=https://billing.example/x&state=abc123')
    await user.type(await screen.findByLabelText(/space name/i), 'Casa')
    await user.click(screen.getByRole('button', {name: /create space/i}))

    expect(await screen.findByText('Your plan allows 3 spaces and you already have 3.')).toBeInTheDocument()
    expect(screen.queryByLabelText(/space name/i)).toBeNull()
    expect(screen.getByRole('link', {name: /see plans/i})).toHaveAttribute('href', 'https://billing.example/finance/plano')

    await user.click(screen.getByRole('button', {name: /^back$/i}))
    await waitFor(() => expect(replaced).not.toBeNull())
    const url = new URL(replaced!)
    expect(url.searchParams.get('cancelled')).toBe('1')
    expect(url.searchParams.get('state')).toBe('abc123')
  })

  it('says one space in the singular', async () => {
    vi.mocked(createOrganizationAPI).mockRejectedValue(
      refusal(402, {code: 'plan_limit', resource: 'spaces', limit: 1, used: 1, plan: 'free'}))
    const user = userEvent.setup()
    renderPage('')
    await user.type(await screen.findByLabelText(/space name/i), 'Casa')
    await user.click(screen.getByRole('button', {name: /create space/i}))
    expect(await screen.findByText('Your plan allows 1 space and you already have 1.')).toBeInTheDocument()
    expect(screen.getByRole('link', {name: /^back$/i})).toHaveAttribute('href', '/account/spaces')
  })

  it('keeps the form when the plan cannot be read', async () => {
    vi.mocked(createOrganizationAPI).mockRejectedValue(refusal(503, {code: 'plan_unavailable'}))
    const user = userEvent.setup()
    renderPage('')
    await user.type(await screen.findByLabelText(/space name/i), 'Casa')
    await user.click(screen.getByRole('button', {name: /create space/i}))
    expect(await screen.findByText(/couldn't check your plan right now/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/space name/i)).toBeInTheDocument()
  })
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run --maxWorkers=2 src/app/account/spaces/new/page.test.tsx; echo "exit=$?"`
Expected: `exit=1` — the limit text is not found.

- [ ] **Step 3: Implement** — in `page.tsx` (imports `planProblemOf`, `planURL` from `@/lib/plan-problem`):

replace the `errorMsg` computation with:

```tsx
  const plan = planProblemOf(error)
  const errorMsg = plan?.kind === 'unavailable'
    ? t('spaces.plan.unavailable')
    : isAxiosError(error)
      // Never the server's detail: it is written for organizations.
      ? t('spaces.new.failed')
      : (error?.message ?? null)
```

and, before `function handleSubmit`, the refused state:

```tsx
  // Over the plan: the form has nothing left to offer. Say why, offer the
  // plans, and give the product its way back (spec § 7).
  if (plan?.kind === 'limit') {
    const plansHref = planURL()
    return (
      <div className="mx-auto max-w-md space-y-4 py-8">
        <Alert>
          <AlertDescription>
            {t('spaces.plan.spacesLimit', {count: plan.limit ?? 0, used: plan.used ?? 0})}
          </AlertDescription>
        </Alert>
        <div className="flex flex-wrap items-center gap-2">
          {plansHref && (
            <a href={plansHref} className={cn(buttonVariants(), 'max-sm:min-h-11')}>
              {t('spaces.plan.seePlans')}
            </a>
          )}
          {isHandoff ? (
            <Button type="button" variant="ghost" onClick={() => leave('cancelled')} className="max-sm:min-h-11">
              {t('spaces.plan.back')}
            </Button>
          ) : (
            <Link href="/account/spaces" className={cn(buttonVariants({variant: 'ghost'}), 'max-sm:min-h-11')}>
              {t('spaces.plan.back')}
            </Link>
          )}
        </div>
      </div>
    )
  }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run --maxWorkers=2 src/app/account/spaces/new/page.test.tsx; echo "exit=$?"` → `exit=0`.
Run: `npx tsc --noEmit && npm run lint` → clean.

- [ ] **Step 5: Commit**

```bash
git add ui/src/app/account/spaces/new/
git commit -m "feat(ui): a space refused by the plan offers the plans and the way back"
```

---

### Task 12: The people page — invite refusal, the counter, transfer refusal

**Files:**
- Modify: `ui/src/app/account/organizations/detail/invitations-tab.tsx`
- Modify: `ui/src/app/account/organizations/detail/settings-tab.tsx` (`TransferSection`)
- Test: `ui/src/app/account/organizations/detail/invitations-tab.test.tsx`, `ui/src/app/account/organizations/detail/settings-tab.test.tsx`

**Interfaces:**
- Consumes: `planProblemOf`, `planURL`, `fetchSpaceUsage`, `SpaceUsage`, i18n `spaces.plan.*` (Task 10); `isPersonal`, `useWorkspaceT`, `workspaceDetail` (existing).
- Produces: in a space owned by the caller, `people + pending of limit` beside the invite button (query key `['space-usage', id]`, hidden on error, `usageUnlimited` when `limit` is −1, invalidated on invite and revoke); the invite dialog shows *"Este espaço já tem N de N pessoas do seu plano."* + **Ver planos** inline on 402, the unavailable sentence on 503, and no toast for either; transfer 402 → toast *"{Name} já está no limite de espaços do plano."*, 503 → the unavailable sentence. The roster and every other tab keep working when billing is down.

- [ ] **Step 1: Write the failing tests**

`invitations-tab.test.tsx` — extend the mocks (`fetchSpaceUsage: vi.fn()` in the `@/lib/queries` mock; `vi.mock('@/lib/env', …BILLING_URL: 'https://billing.example')`), add the `refusal` helper from Task 11, and:

```tsx
function space(): Organization {
  return {...organization('owner'), id: 'spc_1', display_name: 'Casa', kind: 'personal'}
}

  it('shows people + pending of the plan beside the invite button', async () => {
    vi.mocked(fetchSpaceUsage).mockResolvedValue({people: 3, pending_invitations: 1, limit: 5, plan: 'basic'})
    renderTab(space())
    expect(await screen.findByText('3 + 1 of 5 people')).toBeInTheDocument()
  })

  it('hides the counter when the plan cannot be read, and keeps the list', async () => {
    vi.mocked(fetchSpaceUsage).mockRejectedValue(refusal(503, {code: 'plan_unavailable'}))
    vi.mocked(fetchOrganizationInvitations).mockResolvedValue([
      {email: 'a@example.com', role: 'member', invited_by: 'usr_owner', expires_at: new Date().toISOString()},
    ])
    renderTab(space())
    expect(await screen.findByText('a@example.com')).toBeInTheDocument()
    expect(screen.queryByText(/people$/)).toBeNull()
  })

  it('explains a refused invitation inline, with the plans', async () => {
    vi.mocked(fetchSpaceUsage).mockResolvedValue({people: 4, pending_invitations: 1, limit: 5, plan: 'basic'})
    vi.mocked(inviteMemberAPI).mockRejectedValue(
      refusal(402, {code: 'plan_limit', resource: 'people', limit: 5, used: 5, plan: 'basic'}))
    const user = userEvent.setup()
    renderTab(space())
    await user.click(await screen.findByRole('button', {name: /invite/i}))
    await user.type(screen.getByLabelText(/e-?mail/i), 'f@example.com')
    await user.click(within(screen.getByRole('dialog')).getByRole('button', {name: /create invitation|criar convite/i}))

    const dialog = within(screen.getByRole('dialog'))
    expect(await dialog.findByText('This space already has 5 of 5 people on your plan.')).toBeInTheDocument()
    expect(dialog.getByRole('link', {name: /see plans/i})).toHaveAttribute('href', 'https://billing.example/finance/plano')
  })
```

(In the existing `beforeEach`, add `vi.mocked(fetchSpaceUsage).mockResolvedValue({people: 0, pending_invitations: 0, limit: -1})`; the organization tests never call it — assert `expect(fetchSpaceUsage).not.toHaveBeenCalled()` in the existing "sends only the companies that were ticked" test.)

`settings-tab.test.tsx` — with `transferOwnershipAPI` and `fetchOrganizationMembers` already mocked there, add (and the `refusal` helper; `toast` from `sonner` mocked as `vi.mock('sonner', () => ({toast: {success: vi.fn(), error: vi.fn()}}))` if the file does not mock it yet):

```tsx
  it('names the person a transfer could not reach, without their plan', async () => {
    vi.mocked(fetchOrganizationMembers).mockResolvedValue([
      {organization_id: 'spc_1', user_id: 'usr_owner', name: 'Dono', role: 'owner', created_at: ''},
      {organization_id: 'spc_1', user_id: 'usr_bia', name: 'Bia', role: 'member', created_at: ''},
    ])
    vi.mocked(transferOwnershipAPI).mockRejectedValue(refusal(402, {code: 'plan_limit', resource: 'spaces'}))
    const user = userEvent.setup()
    renderSettings({id: 'spc_1', display_name: 'Casa', owner_user_id: 'usr_owner', role: 'owner', joined_at: '', kind: 'personal'})
    await user.click(await screen.findByLabelText(/transfer/i))
    await user.click(await screen.findByRole('option', {name: 'Bia'}))
    await user.click(screen.getByRole('button', {name: /transfer/i}))
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', {name: /transfer|confirm/i}))
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Bia is already at their plan's space limit."))
  })
```

(`renderSettings` is the file's existing render helper; if it is named differently, use that name — it renders `<SettingsTab organization={…}/>` inside a `QueryClientProvider`.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `npx vitest run --maxWorkers=2 src/app/account/organizations/detail/invitations-tab.test.tsx src/app/account/organizations/detail/settings-tab.test.tsx; echo "exit=$?"`
Expected: `exit=1`.

- [ ] **Step 3: Implement**

`invitations-tab.tsx` (imports `fetchSpaceUsage`, `planProblemOf`, `planURL`, `useTranslation`):

In `InvitationsTab`, after the invitations query:

```tsx
  const personal = isPersonal(organization)
  const ownsSpace = personal && organization.role === 'owner'
  // The counter reads the same entitlement the invite is checked against. A
  // plan that cannot be read hides it; nothing else on the page depends on it.
  const { data: usage } = useQuery({
    queryKey: ['space-usage', organization.id],
    queryFn: () => fetchSpaceUsage(organization.id),
    enabled: ownsSpace,
    retry: false,
  })
```

in `revokeMutation.onSuccess` add `queryClient.invalidateQueries({ queryKey: ['space-usage', organization.id] })`; the header row becomes:

```tsx
      <div className="flex flex-wrap items-center justify-end gap-3">
        {usage && <SpaceUsageCount usage={usage} />}
        <InviteDialog organization={organization} />
      </div>
```

with:

```tsx
function SpaceUsageCount({ usage }: { usage: SpaceUsage }) {
  const { t } = useTranslation()
  const values = { people: usage.people, pending: usage.pending_invitations, limit: usage.limit }
  return (
    <span className="text-sm tabular-nums text-muted-foreground">
      {usage.limit < 0 ? t('spaces.plan.usageUnlimited', values) : t('spaces.plan.usage', values)}
    </span>
  )
}
```

In `InviteDialog`: in the mutation's `onSuccess` add `queryClient.invalidateQueries({ queryKey: ['space-usage', organization.id] })`; `onError` becomes

```tsx
    onError: (err) => {
      // A plan refusal is explained inside the dialog, next to the field.
      if (planProblemOf(err)) return
      if (isAxiosError(err)) toast.error(workspaceDetail(err.response?.data?.detail, organization.kind) ?? t('toast.inviteFailed'))
    },
```

and the message:

```tsx
  const { t: tPlain } = useTranslation()
  const plan = planProblemOf(error)
  const plansHref = planURL()
  const errorMsg = plan?.kind === 'limit'
    ? tPlain('spaces.plan.peopleLimit', { used: plan.used ?? 0, limit: plan.limit ?? 0 })
    : plan?.kind === 'unavailable'
      ? tPlain('spaces.plan.unavailable')
      : isAxiosError(error)
        ? (workspaceDetail(error.response?.data?.detail, organization.kind) ?? t('toast.inviteFailed'))
        : null
```

and where the dialog renders `errorMsg` in its destructive `Alert`, append the link for a limit:

```tsx
            {errorMsg && (
              <Alert variant="destructive">
                <AlertDescription>
                  {errorMsg}
                  {plan?.kind === 'limit' && plansHref && (
                    <>
                      {' '}
                      <a href={plansHref} className="font-medium underline underline-offset-4">
                        {tPlain('spaces.plan.seePlans')}
                      </a>
                    </>
                  )}
                </AlertDescription>
              </Alert>
            )}
```

`settings-tab.tsx` — `TransferSection`'s mutation `onError`:

```tsx
    onError: (err, userId) => {
      const plan = planProblemOf(err)
      if (plan?.kind === 'limit') {
        // Their plan's name and numbers are theirs: the server sends none (spec § 7).
        const name = candidates.find((m) => m.user_id === userId)?.name || userId
        toast.error(tPlain('spaces.plan.transferLimit', {name}))
        return
      }
      if (plan?.kind === 'unavailable') {
        toast.error(tPlain('spaces.plan.unavailable'))
        return
      }
      if (isAxiosError(err)) toast.error(workspaceDetail(err.response?.data?.detail, organization.kind) ?? t('toast.transferFailed'))
    },
```

with `const {t: tPlain} = useTranslation()` at the top of `TransferSection` and the imports `planProblemOf` and `useTranslation`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `npx vitest run --maxWorkers=2 src/app/account/organizations/detail/ src/app/account/spaces/; echo "exit=$?"` → `exit=0`.
Run: `npx tsc --noEmit && npm run lint` → clean.

- [ ] **Step 5: Commit**

```bash
git add ui/src/app/account/organizations/detail/
git commit -m "feat(ui): the people page shows the plan's room and explains a refused invite or transfer"
```

---

### Task 13: Mark the spec implemented and verify the whole branch

**Files:**
- Modify: `docs/specs/2026-10-10-space-plan-limits.md` (status line; an "Amendment, implementation" section only for departures from P1–P11)

**Interfaces:**
- Consumes: everything above.
- Produces: a spec that says what was built; ctech-billing reads the internal counts and the queue/key decisions from it.

- [ ] **Step 1: Mark the spec implemented** — the decisions P1–P11 are already in the spec's "Amendment, planning (2026-10-10)" section (written with this plan). Change the status line to `**Implemented** · <date>` and, only if the implementation departed from P1–P11, append an "Amendment, implementation (<date>)" section listing each departure. No departure → status line only.

- [ ] **Step 2: Run everything the way CI does**

Run: `cd api && gofmt -l internal cmd | grep -v -e 'mfa/totp/model.go' -e 'session/model.go' -e 'session/service_test.go' -e 'geoupdater/updater_test.go'; go vet ./... && go test ./...`
Expected: no gofmt output; every package `ok`.
Run: `cd ui && npx vitest run --maxWorkers=2; echo "exit=$?"` → `exit=0`; `npx tsc --noEmit && npm run lint` → clean.
Run: `cd cdk && npx jest` → PASS.

- [ ] **Step 3: Commit**

```bash
git add docs/specs/2026-10-10-space-plan-limits.md
git commit -m "docs: plan limits on personal spaces — mark the spec implemented"
```

## Deploy order (outside this plan)

1. **ctech-billing** step 1 (its § 10): catalogue, `owner_key`/`default` on entitlements, `usage/levels`, the `account-billing` credential (tenant `ctech`, owner `finance`).
2. **This repository**: merge (dark — `BILLING_API_URL` unset everywhere). Register the client in each environment: `go run ./cmd/createclient -client-id account-billing -name "ctech-account plan limits" -scopes billing:entitlements:read,billing:usage:write` (needs billing's scopes in the catalog; the printed secret is not used). Then deploy the CDK change, which sets `BILLING_API_URL` and switches enforcement on; watch the boot log for `PLAN LIMITS NOT READY` — if it appears, step 1 is not live and every create/invite/transfer of a space answers 503.
3. **ctech-billing** step 3: the plan screen, reading `people`/`pending_invitations` from `GET /internal/users/:user_id/organizations`.
