# Personal workspaces (ctech-account) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Workspaces of `kind: personal` ("Espaços") — created and managed here, with no company and a three-rung ladder — plus the `kind` every consumer needs: on the internal membership and user-organizations routes (ctech-billing) and on the company routes (ctech-dfe's defence in depth).

**Architecture:** One optional attribute on the existing organization row (`kind`, absent = organization, so nothing is migrated). The domain applies the space rules (no admin, transfer only to full access, no companies); owner-only management falls out of the existing admin floor. The public list stays organizations-only and spaces are asked for with `?kind=personal`. The UI reuses the organization tabs with kind-aware copy, adds `/account/spaces`, `/account/spaces/new` (handoff) and `/account/spaces/people`, and reuses the existing handoff validation endpoint.

**Tech Stack:** Go 1.27 + Fiber v3 + DynamoDB (`api/`), standard `testing`; Next.js static export + vitest + TypeScript (`ui/`).

**Spec:** [`docs/specs/2026-10-09-personal-workspaces.md`](../specs/2026-10-09-personal-workspaces.md) (with its 2026-10-09 amendment). Consumers: `ctech-billing/docs/specs/2026-10-09-shared-spaces-design.md` (ADR 0027 there), `ctech-dfe/docs/specs/2026-10-09-personal-workspaces-in-dfe.md`.

## Global Constraints

- An absent `kind` is an organization. No backfill, no migration, no behaviour change for existing workspaces — every pre-existing test must still pass untouched (except the `TransferOwnership` fakes in Task 5, whose signature changes).
- A space never holds a company; never has an `admin`; is managed (invite, role, remove, rename) by its owner only; is transferred only to a `member`.
- Refusals stay indistinguishable: no `kind` on `{member:false}` or `{may_act:false}`.
- Interface copy for a space never says "organização", "empresa" or "CNPJ" (Task 6 has a test for it).
- Handoff rules are unchanged: first-party client, `return_to` on a registered origin, no token on the redirect. Spaces reuse `GET /v1.0/organizations/handoff`.
- Static export: no dynamic route segments (`/account/spaces/people?id=`, not `/{id}/people`).
- Go: `gofmt` clean on touched files, `go vet ./...` clean. Four files are already not gofmt-clean on `main` (`internal/domain/mfa/totp/model.go`, `internal/domain/session/model.go`, `internal/domain/session/service_test.go`, `internal/geoupdater/updater_test.go`) — leave them alone.
- UI: `npx vitest run`, `npx tsc --noEmit`, `npm run lint` green after each UI task.
- Commit messages carry no attribution trailer of any kind.

## Review Focus

1. **A workspace written before this change** (no `kind` attribute) must behave exactly as an organization everywhere — list, roles, transfer, companies. Task 1 `TestCreateOfKindStoresTheKind` (absent → organization), Task 2 `TestTheListIsSplitByKind`.
2. **A non-owner in a space** must not be able to invite, change a role, remove someone or rename — but must still be able to leave. Task 1 `TestOnlyTheOwnerManagesASpace` (the last assertion is leaving).
3. **Transferring a space** must neither go to read-only access nor leave an `admin` behind. Task 1 `TestASpaceIsTransferredOnlyToFullAccess`, Task 5 `TestTransferringASpaceLeavesTheFormerOwnerWithFullAccess`.
4. **A prober** must not learn a workspace's kind from a refusal. Task 3 `TestARefusalCarriesNoKind`, Task 4 `TestAReachRefusalCarriesNoKind`.
5. **A company in a space** must be refused at the route, and reported as being in a space if it ever got there anyway. Task 4 `TestASpaceRefusesACompany`, `TestACompanyInASpaceIsReportedAsSuch`.

---

## API tasks (`api/`)

All Go commands run from `api/`.

### Task 1: Workspace kind in the domain: creation, reads and the space rules

**Files:**
- Create: `api/internal/domain/organization/kind.go`
- Modify: `api/internal/domain/organization/model.go`
- Modify: `api/internal/domain/organization/service.go`
- Test: `api/internal/domain/organization/kind_test.go`

**Interfaces:**
- Consumes: `Organization`, `Service`, `Repository`, `IsGrantableRole`, `ErrNotGrantable`, `ErrForbidden`, `ErrNotAMember`, `ErrNotFound` (existing, `api/internal/domain/organization`); test helpers `newFakeRepo`, `fixedClock`, `seedOrg`, `join` (existing, `service_test.go`).
- Produces:
- `KindOrganization = "organization"`, `KindPersonal = "personal"`; `NormalizeKind(string) (string, error)` (`""` → organization; unknown → `ErrInvalidKind`).
- `Organization.Kind string` (`dynamodbav:"kind,omitempty"`), `(*Organization).KindOf() string` (normalized; a garbled stored value reads as personal).
- `IsGrantableRoleIn(kind, role string) bool` — no `admin` in a space.
- `(*Service).CreateOfKind(ctx, kind, ownerUserID, ownerName, displayName) (*Organization, error)`; `Create` delegates with `KindOrganization`.
- `(*Service).KindOf(ctx, orgID) (string, error)`; `(*Service).MembershipOf(ctx, orgID, userID) (role, kind string, err error)`.
- Errors `ErrInvalidKind`, `ErrTransferNeedsFullAccess`, `ErrNoCompaniesInASpace`.
- `Workspace.Kind string`.
- Behaviour: `SetRole`/`Invite` refuse `admin` in a space (`ErrNotGrantable`); `Invite` to a space with company ids → `ErrNoCompaniesInASpace`; `Transfer` in a space only to a `member` (`ErrTransferNeedsFullAccess`). Owner-only management in a space falls out of the existing admin floor, since a space has no admin — pinned by `TestOnlyTheOwnerManagesASpace`.

No migration: an absent `kind` reads as an organization (`NormalizeKind("")`), which is what every existing row is.

- [ ] **Step 1: Write the failing test** — `api/internal/domain/organization/kind_test.go`

```go
package organization

import (
	"context"
	"errors"
	"testing"
)

func TestNormalizeKind(t *testing.T) {
	for in, want := range map[string]string{"": KindOrganization, "organization": KindOrganization, "personal": KindPersonal} {
		got, err := NormalizeKind(in)
		if err != nil || got != want {
			t.Fatalf("NormalizeKind(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := NormalizeKind("household"); !errors.Is(err, ErrInvalidKind) {
		t.Fatalf("an unknown kind: err = %v", err)
	}
}

// A row with an unknown kind reads as a space: the kind that grants less.
func TestAnUnknownStoredKindReadsAsPersonal(t *testing.T) {
	if got := (&Organization{Kind: "garbled"}).KindOf(); got != KindPersonal {
		t.Fatalf("KindOf = %q, want personal", got)
	}
	if got := (&Organization{}).KindOf(); got != KindOrganization {
		t.Fatalf("an absent kind = %q, want organization", got)
	}
}

func TestASpaceHasNoAdminRung(t *testing.T) {
	if IsGrantableRoleIn(KindPersonal, RoleAdmin) {
		t.Fatal("admin is grantable in a space")
	}
	for _, role := range []string{RoleMember, RoleViewer} {
		if !IsGrantableRoleIn(KindPersonal, role) {
			t.Fatalf("%s is not grantable in a space", role)
		}
	}
	if !IsGrantableRoleIn(KindOrganization, RoleAdmin) || IsGrantableRoleIn(KindPersonal, RoleOwner) {
		t.Fatal("the organization ladder changed, or owner became grantable")
	}
}

func seedSpace(t *testing.T, ownerID string) (*Service, *Organization) {
	t.Helper()
	svc := NewService(newFakeRepo(), fixedClock)
	space, err := svc.CreateOfKind(context.Background(), KindPersonal, ownerID, "Pessoa", "Casa")
	if err != nil {
		t.Fatalf("seeding a space: %v", err)
	}
	return svc, space
}

func TestCreateOfKindStoresTheKind(t *testing.T) {
	svc, space := seedSpace(t, "usr_owner")
	ctx := context.Background()
	if space.Kind != KindPersonal {
		t.Fatalf("Kind = %q", space.Kind)
	}
	kind, err := svc.KindOf(ctx, space.ID)
	if err != nil || kind != KindPersonal {
		t.Fatalf("KindOf = %q, %v", kind, err)
	}
	org, _ := svc.Create(ctx, "usr_owner", "Pessoa", "CTech")
	if kind, _ := svc.KindOf(ctx, org.ID); kind != KindOrganization {
		t.Fatalf("Create made a %q", kind)
	}
	if _, err := svc.CreateOfKind(ctx, "household", "usr_owner", "Pessoa", "X"); !errors.Is(err, ErrInvalidKind) {
		t.Fatalf("an unknown kind was created: %v", err)
	}
}

func TestMembershipOfCarriesTheKind(t *testing.T) {
	svc, space := seedSpace(t, "usr_owner")
	role, kind, err := svc.MembershipOf(context.Background(), space.ID, "usr_owner")
	if err != nil || role != RoleOwner || kind != KindPersonal {
		t.Fatalf("MembershipOf = %q, %q, %v", role, kind, err)
	}
	if _, _, err := svc.MembershipOf(context.Background(), space.ID, "usr_stranger"); !errors.Is(err, ErrNotAMember) {
		t.Fatalf("a stranger: err = %v", err)
	}
}

func TestASpaceRefusesAdmin(t *testing.T) {
	svc, space := seedSpace(t, "usr_owner")
	ctx := context.Background()
	join(t, svc, space.ID, "usr_2", RoleViewer)
	if err := svc.SetRole(ctx, space.ID, "usr_owner", "usr_2", RoleAdmin); !errors.Is(err, ErrNotGrantable) {
		t.Fatalf("SetRole admin in a space: err = %v", err)
	}
	if _, err := svc.Invite(ctx, space.ID, "usr_owner", "a@example.com", RoleAdmin, nil); !errors.Is(err, ErrNotGrantable) {
		t.Fatalf("Invite admin to a space: err = %v", err)
	}
	if err := svc.SetRole(ctx, space.ID, "usr_owner", "usr_2", RoleMember); err != nil {
		t.Fatalf("SetRole member in a space: %v", err)
	}
}

// With no admin rung, only the owner clears the floor every management route
// already requires. This pins that consequence.
func TestOnlyTheOwnerManagesASpace(t *testing.T) {
	svc, space := seedSpace(t, "usr_owner")
	ctx := context.Background()
	join(t, svc, space.ID, "usr_full", RoleMember)
	join(t, svc, space.ID, "usr_read", RoleViewer)
	if _, err := svc.Invite(ctx, space.ID, "usr_full", "b@example.com", RoleViewer, nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a member invited: err = %v", err)
	}
	if err := svc.SetRole(ctx, space.ID, "usr_full", "usr_read", RoleMember); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a member changed a role: err = %v", err)
	}
	if err := svc.Remove(ctx, space.ID, "usr_full", "usr_read"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a member removed somebody: err = %v", err)
	}
	if err := svc.Rename(ctx, space.ID, "usr_full", "Outro"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a member renamed the space: err = %v", err)
	}
	if err := svc.Remove(ctx, space.ID, "usr_read", "usr_read"); err != nil {
		t.Fatalf("leaving a space: %v", err)
	}
}

func TestASpaceIsTransferredOnlyToFullAccess(t *testing.T) {
	svc, space := seedSpace(t, "usr_owner")
	ctx := context.Background()
	join(t, svc, space.ID, "usr_read", RoleViewer)
	join(t, svc, space.ID, "usr_full", RoleMember)
	if err := svc.Transfer(ctx, space.ID, "usr_owner", "usr_read"); !errors.Is(err, ErrTransferNeedsFullAccess) {
		t.Fatalf("transfer to a viewer: err = %v", err)
	}
	if err := svc.Transfer(ctx, space.ID, "usr_owner", "usr_full"); err != nil {
		t.Fatalf("transfer to a member: %v", err)
	}
}

// An organization keeps its rules: transfer to any member, admin grantable.
func TestAnOrganizationIsUnchanged(t *testing.T) {
	svc, org := seedOrg(t, "usr_owner")
	ctx := context.Background()
	join(t, svc, org.ID, "usr_read", RoleViewer)
	if err := svc.SetRole(ctx, org.ID, "usr_owner", "usr_read", RoleAdmin); err != nil {
		t.Fatalf("admin in an organization: %v", err)
	}
	join(t, svc, org.ID, "usr_v", RoleViewer)
	if err := svc.Transfer(ctx, org.ID, "usr_owner", "usr_v"); err != nil {
		t.Fatalf("transfer to a viewer in an organization: %v", err)
	}
}

func TestASpaceInvitationNamesNoCompanies(t *testing.T) {
	svc, space := seedSpace(t, "usr_owner")
	if _, err := svc.Invite(context.Background(), space.ID, "usr_owner", "c@example.com", RoleMember, []string{"cmp_1"}); !errors.Is(err, ErrNoCompaniesInASpace) {
		t.Fatalf("err = %v", err)
	}
}

func TestListWorkspacesCarriesTheKind(t *testing.T) {
	svc, space := seedSpace(t, "usr_owner")
	org, _ := svc.Create(context.Background(), "usr_owner", "Pessoa", "CTech")
	got, err := svc.ListWorkspaces(context.Background(), "usr_owner")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, w := range got {
		kinds[w.ID] = w.Kind
	}
	if kinds[space.ID] != KindPersonal || kinds[org.ID] != KindOrganization {
		t.Fatalf("kinds = %v", kinds)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/domain/organization/ -v`
Expected: FAIL — build fails: `o.Kind undefined`, `svc.CreateOfKind undefined`.

- [ ] **Step 3: Write the implementation** — `api/internal/domain/organization/kind.go`

```go
package organization

import "errors"

// The kinds of workspace. An organization is what every workspace was before
// kinds existed; a personal workspace is a space — a household, a shared
// budget — with no company and a three-rung ladder
// (docs/specs/2026-10-09-personal-workspaces.md).
//
// The kind is set at creation and never changes. Turning a space into an
// organization would be a new decision, not a field update.
const (
	KindOrganization = "organization"
	KindPersonal     = "personal"
)

var (
	// ErrInvalidKind is a kind that is neither of the two.
	ErrInvalidKind = errors.New("unknown workspace kind")
	// ErrTransferNeedsFullAccess is handing a space to somebody with read
	// access only. They are promoted first, deliberately: ownership is the
	// widest grant there is, and it should not skip the one below it.
	ErrTransferNeedsFullAccess = errors.New("a space can only be transferred to somebody with full access")
	// ErrNoCompaniesInASpace is an invitation to a space naming companies. A
	// space holds none, so the grant could only ever fail after the person
	// joined.
	ErrNoCompaniesInASpace = errors.New("a space holds no companies")
)

// NormalizeKind reads a stored or requested kind. Empty is an organization:
// every row written before kinds existed has no kind attribute, and reading it
// as anything else would change what an existing workspace is.
func NormalizeKind(kind string) (string, error) {
	switch kind {
	case "", KindOrganization:
		return KindOrganization, nil
	case KindPersonal:
		return KindPersonal, nil
	}
	return "", ErrInvalidKind
}

// KindOf returns the organization's kind, normalized.
func (o *Organization) KindOf() string {
	k, err := NormalizeKind(o.Kind)
	if err != nil {
		// A value that reached the table by a path nobody planned. Read as a
		// space, the kind that grants less: no admin rung, no companies.
		return KindPersonal
	}
	return k
}

// IsGrantableRoleIn reports whether member management may assign role in a
// workspace of this kind. A space has no admin: two levels — full access and
// read — and an owner, and the ladder must not quietly grow a third.
func IsGrantableRoleIn(kind, role string) bool {
	if !IsGrantableRole(role) {
		return false
	}
	return kind != KindPersonal || role != RoleAdmin
}
```

- [ ] **Step 4: Apply the implementation** — save as `task1.patch` and run `git apply task1.patch` from the repository root

```diff
diff --git a/api/internal/domain/organization/model.go b/api/internal/domain/organization/model.go
index 4c078e1..ec49b56 100644
--- a/api/internal/domain/organization/model.go
+++ b/api/internal/domain/organization/model.go
@@ -76,6 +76,10 @@ type Organization struct {
 	ID          string `dynamodbav:"-"`
 	DisplayName string `dynamodbav:"display_name"`
 	OwnerUserID string `dynamodbav:"owner_user_id"`
+	// Kind is KindOrganization or KindPersonal (kind.go). Absent on every row
+	// written before kinds existed, which NormalizeKind reads as an
+	// organization — so nothing is migrated.
+	Kind string `dynamodbav:"kind,omitempty"`
 	// SourceSystem/SourceRef record where an imported organization came from —
 	// set only by a migration, empty for anything created through the product.
 	// They exist so an import can be re-run without writing the row twice, and
diff --git a/api/internal/domain/organization/service.go b/api/internal/domain/organization/service.go
index 249202f..02edb09 100644
--- a/api/internal/domain/organization/service.go
+++ b/api/internal/domain/organization/service.go
@@ -77,6 +77,17 @@ func NewService(repo Repository, now func() time.Time) *Service {
 // separate act with evidence behind it (phase 3), and conflating the two would
 // mean the first person to type a name owns it.
 func (s *Service) Create(ctx context.Context, ownerUserID, ownerName, displayName string) (*Organization, error) {
+	return s.CreateOfKind(ctx, KindOrganization, ownerUserID, ownerName, displayName)
+}
+
+// CreateOfKind is Create for either kind of workspace. A space (KindPersonal)
+// is created exactly like an organization — the caller as its owner, in one
+// transaction — and differs only in the rules applied to it afterwards.
+func (s *Service) CreateOfKind(ctx context.Context, kind, ownerUserID, ownerName, displayName string) (*Organization, error) {
+	kind, err := NormalizeKind(kind)
+	if err != nil {
+		return nil, err
+	}
 	name := strings.TrimSpace(displayName)
 	if name == "" || len(name) > maxDisplayName {
 		return nil, ErrInvalidName
@@ -89,6 +100,7 @@ func (s *Service) Create(ctx context.Context, ownerUserID, ownerName, displayNam
 		ID:          uuid.NewV7().String(),
 		DisplayName: name,
 		OwnerUserID: ownerUserID,
+		Kind:        kind,
 		CreatedAt:   now,
 		UpdatedAt:   now,
 	}
@@ -149,6 +161,35 @@ func (s *Service) RoleOf(ctx context.Context, orgID, userID string) (string, err
 	return m.Role, nil
 }
 
+// KindOf returns a workspace's kind. ErrNotFound when it does not exist.
+func (s *Service) KindOf(ctx context.Context, orgID string) (string, error) {
+	org, err := s.repo.Get(ctx, orgID)
+	if err != nil {
+		return "", err
+	}
+	return org.KindOf(), nil
+}
+
+// MembershipOf is RoleOf plus the workspace's kind: what a product needs to
+// decide what a role means there (ctech-billing ADR 0027). A role alone is not
+// enough — `member` grants different things in a space and in an organization.
+func (s *Service) MembershipOf(ctx context.Context, orgID, userID string) (role, kind string, err error) {
+	role, err = s.RoleOf(ctx, orgID, userID)
+	if err != nil {
+		return "", "", err
+	}
+	kind, err = s.KindOf(ctx, orgID)
+	if errors.Is(err, ErrNotFound) {
+		// A membership left pointing at a workspace that is gone answers as no
+		// membership, the way ListWorkspaces skips it.
+		return "", "", ErrNotAMember
+	}
+	if err != nil {
+		return "", "", err
+	}
+	return role, kind, nil
+}
+
 // require is the one place a floor is enforced, so "who may do this" is a
 // single line at the top of each use case rather than a comparison each of them
 // spells out slightly differently.
@@ -178,6 +219,9 @@ func (s *Service) SetRole(ctx context.Context, orgID, actorUserID, targetUserID,
 	if !IsGrantableRole(role) {
 		return ErrNotGrantable
 	}
+	if err := s.requireGrantableIn(ctx, orgID, role); err != nil {
+		return err
+	}
 	if actorUserID == targetUserID {
 		return ErrOwnRole
 	}
@@ -253,12 +297,37 @@ func (s *Service) Transfer(ctx context.Context, orgID, actorUserID, toUserID str
 	if actorUserID == toUserID {
 		return ErrForbidden
 	}
-	if _, err := s.RoleOf(ctx, orgID, toUserID); err != nil {
+	toRole, err := s.RoleOf(ctx, orgID, toUserID)
+	if err != nil {
 		return err
 	}
+	kind, err := s.KindOf(ctx, orgID)
+	if err != nil {
+		return err
+	}
+	if kind == KindPersonal && toRole != RoleMember {
+		return ErrTransferNeedsFullAccess
+	}
 	return s.repo.TransferOwnership(ctx, orgID, actorUserID, toUserID, s.now().UTC())
 }
 
+// requireGrantableIn refuses a role the workspace's kind does not have. An
+// unknown workspace passes here and is refused by the membership check that
+// follows, so the two refusals stay indistinguishable to a stranger.
+func (s *Service) requireGrantableIn(ctx context.Context, orgID, role string) error {
+	kind, err := s.KindOf(ctx, orgID)
+	if errors.Is(err, ErrNotFound) {
+		return nil
+	}
+	if err != nil {
+		return err
+	}
+	if !IsGrantableRoleIn(kind, role) {
+		return ErrNotGrantable
+	}
+	return nil
+}
+
 // invitationTTL bounds how long an offer stands. Seven days is long enough for
 // somebody to read their e-mail on holiday and short enough that a link found
 // in an old inbox is no longer a way in.
@@ -286,6 +355,14 @@ func (s *Service) Invite(ctx context.Context, orgID, actorUserID, email, role st
 	if address == "" || !strings.Contains(address, "@") {
 		return "", ErrInvalidName
 	}
+	if err := s.requireGrantableIn(ctx, orgID, role); err != nil {
+		return "", err
+	}
+	if len(companyIDs) > 0 {
+		if kind, err := s.KindOf(ctx, orgID); err == nil && kind == KindPersonal {
+			return "", ErrNoCompaniesInASpace
+		}
+	}
 	actorRole, err := s.RoleOf(ctx, orgID, actorUserID)
 	if err != nil {
 		return "", err
@@ -424,6 +501,7 @@ type Workspace struct {
 	ID          string
 	DisplayName string
 	OwnerUserID string
+	Kind        string
 	Role        string
 	JoinedAt    time.Time
 }
@@ -465,6 +543,7 @@ func (s *Service) ListWorkspaces(ctx context.Context, userID string) ([]Workspac
 			ID:          org.ID,
 			DisplayName: org.DisplayName,
 			OwnerUserID: org.OwnerUserID,
+			Kind:        org.KindOf(),
 			Role:        m.Role,
 			JoinedAt:    m.CreatedAt,
 		})
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/domain/organization/ -v && go vet ./... && go test ./...`
Expected: the listed tests PASS; `vet` silent; every package `ok`.

- [ ] **Step 6: Commit**

```bash
git add api/internal/domain/organization/kind.go api/internal/domain/organization/model.go api/internal/domain/organization/service.go api/internal/domain/organization/kind_test.go
git commit -m "feat(organization): workspace kind (organization or personal space) and the space rules"
```

### Task 2: Public routes: create with a kind, list by kind, kind on every DTO

**Files:**
- Modify: `api/internal/handler/organization.go`
- Test: `api/internal/handler/organization_kind_test.go`

**Interfaces:**
- Consumes: Task 1: `CreateOfKind`, `KindOf`, `NormalizeKind`, `(*Organization).KindOf`, `Workspace.Kind`, `ErrInvalidKind`, `ErrTransferNeedsFullAccess`, `ErrNoCompaniesInASpace`. Test helpers `newOrgTestApp`, `bodyString`, `problemOf` (existing).
- Produces:
- `POST /v1.0/organizations` takes `{display_name, kind?}` (`kind` ∈ organization|personal, default organization; anything else 422).
- `GET /v1.0/organizations` returns `kind = organization` only; `?kind=personal` returns the person's spaces; an unknown value is 422. Same `{organizations: [...]}` envelope.
- `kind` on the create, list and get DTOs.
- 422 problems for `ErrInvalidKind`, `ErrTransferNeedsFullAccess`, `ErrNoCompaniesInASpace`.
- Test helpers `decodeJSON(t, resp, dst)` and `listedWorkspace` (reused by Tasks 3–4).

The organizations list keeps its meaning — organizations — so nothing that reads it today (this UI, any product) starts seeing spaces.

- [ ] **Step 1: Write the failing test** — `api/internal/handler/organization_kind_test.go`

```go
package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	orgDomain "gopkg.aoctech.app/account/api/internal/domain/organization"
)

func decodeJSON(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decoding: %v", err)
	}
}

type listedWorkspace struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Role string `json:"role"`
}

func TestCreatingASpaceStoresItsKind(t *testing.T) {
	a := newOrgTestApp(t)
	owner := a.registerUser(t, "space-create@example.com", "Sup3rSecret!pass", "Dono")
	token := a.issueToken(t, owner.ID())

	resp := a.do(t, http.MethodPost, "/v1.0/organizations", token, `{"display_name":"Casa","kind":"personal"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d (%s)", resp.StatusCode, bodyString(resp))
	}
	var created struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	decodeJSON(t, resp, &created)
	if created.Kind != orgDomain.KindPersonal {
		t.Fatalf("kind = %q, want personal", created.Kind)
	}
	if kind, _ := a.svc.KindOf(context.Background(), created.ID); kind != orgDomain.KindPersonal {
		t.Fatalf("stored kind = %q", kind)
	}

	resp = a.do(t, http.MethodPost, "/v1.0/organizations", token, `{"display_name":"CTech"}`)
	decodeJSON(t, resp, &created)
	if created.Kind != orgDomain.KindOrganization {
		t.Fatalf("no kind created a %q, want organization", created.Kind)
	}

	if resp := a.do(t, http.MethodPost, "/v1.0/organizations", token, `{"display_name":"X","kind":"household"}`); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown kind: status %d, want 422", resp.StatusCode)
	}
}

// The organizations list shows organizations only; spaces are asked for by
// kind. A space showing up in the organizations screen, or in the DF-e, would
// teach the second noun the spec keeps out of a household's sight.
func TestTheListIsSplitByKind(t *testing.T) {
	a := newOrgTestApp(t)
	owner := a.registerUser(t, "space-list@example.com", "Sup3rSecret!pass", "Dono")
	token := a.issueToken(t, owner.ID())
	ctx := context.Background()
	org, _ := a.svc.Create(ctx, owner.ID(), "Dono", "CTech")
	space, _ := a.svc.CreateOfKind(ctx, orgDomain.KindPersonal, owner.ID(), "Dono", "Casa")

	list := func(query string) []listedWorkspace {
		t.Helper()
		resp := a.do(t, http.MethodGet, "/v1.0/organizations"+query, token, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d (%s)", query, resp.StatusCode, bodyString(resp))
		}
		var body struct {
			Organizations []listedWorkspace `json:"organizations"`
		}
		decodeJSON(t, resp, &body)
		return body.Organizations
	}

	if got := list(""); len(got) != 1 || got[0].ID != org.ID || got[0].Kind != orgDomain.KindOrganization {
		t.Fatalf("default list = %+v, want the organization only", got)
	}
	if got := list("?kind=personal"); len(got) != 1 || got[0].ID != space.ID || got[0].Kind != orgDomain.KindPersonal {
		t.Fatalf("spaces = %+v, want the space only", got)
	}
	if resp := a.do(t, http.MethodGet, "/v1.0/organizations?kind=household", token, ""); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown kind filter: status %d, want 422", resp.StatusCode)
	}
}

func TestASpaceReadsAsASpace(t *testing.T) {
	a := newOrgTestApp(t)
	owner := a.registerUser(t, "space-get@example.com", "Sup3rSecret!pass", "Dono")
	space, _ := a.svc.CreateOfKind(context.Background(), orgDomain.KindPersonal, owner.ID(), "Dono", "Casa")

	resp := a.do(t, http.MethodGet, "/v1.0/organizations/"+space.ID, a.issueToken(t, owner.ID()), "")
	var got struct {
		Kind string `json:"kind"`
	}
	decodeJSON(t, resp, &got)
	if got.Kind != orgDomain.KindPersonal {
		t.Fatalf("kind = %q", got.Kind)
	}
}

func TestASpaceRefusesAdminAndATransferToReadAccess(t *testing.T) {
	a := newOrgTestApp(t)
	owner := a.registerUser(t, "space-rules@example.com", "Sup3rSecret!pass", "Dono")
	reader := a.registerUser(t, "space-reader@example.com", "Sup3rSecret!pass", "Leitor")
	ctx := context.Background()
	space, _ := a.svc.CreateOfKind(ctx, orgDomain.KindPersonal, owner.ID(), "Dono", "Casa")
	if err := a.repo.PutMembership(ctx, &orgDomain.Membership{OrganizationID: space.ID, UserID: reader.ID(), Role: orgDomain.RoleViewer}); err != nil {
		t.Fatal(err)
	}
	token := a.issueToken(t, owner.ID())

	resp := a.do(t, http.MethodPatch, "/v1.0/organizations/"+space.ID+"/members/"+reader.ID(), token, `{"role":"admin"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("admin in a space: status %d, want 422", resp.StatusCode)
	}
	resp = a.do(t, http.MethodPost, "/v1.0/organizations/"+space.ID+"/invitations", token, `{"email":"x@example.com","role":"admin"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("inviting an admin to a space: status %d, want 422", resp.StatusCode)
	}
	resp = a.do(t, http.MethodPost, "/v1.0/organizations/"+space.ID+"/transfer", token, `{"user_id":"`+reader.ID()+`"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("transfer to a viewer: status %d, want 422", resp.StatusCode)
	}
	if got := problemOf(t, resp).Detail; got != "A space can only be transferred to somebody with full access. Give them full access first." {
		t.Fatalf("detail = %q", got)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/handler/ -run 'TestCreatingASpace|TestTheListIsSplit|TestASpaceReads|TestASpaceRefusesAdmin' -v`
Expected: FAIL — the created kind is empty, the default list includes the space, and transfer to a viewer answers 500.

- [ ] **Step 3: Apply the implementation** — save as `task2.patch` and run `git apply task2.patch` from the repository root

```diff
diff --git a/api/internal/handler/organization.go b/api/internal/handler/organization.go
index b6e57cd..fa60849 100644
--- a/api/internal/handler/organization.go
+++ b/api/internal/handler/organization.go
@@ -54,6 +54,13 @@ type organizationRequest struct {
 	DisplayName string `json:"display_name" validate:"required,max=120"`
 }
 
+// createOrganizationRequest is organizationRequest plus the kind, which only
+// creation takes: a workspace's kind never changes afterwards.
+type createOrganizationRequest struct {
+	DisplayName string `json:"display_name" validate:"required,max=120"`
+	Kind        string `json:"kind" validate:"omitempty,oneof=organization personal"`
+}
+
 type inviteRequest struct {
 	Email string `json:"email" validate:"required,email"`
 	Role  string `json:"role" validate:"required,oneof=admin member viewer"`
@@ -79,6 +86,7 @@ type organizationDTO struct {
 	ID          string `json:"id"`
 	DisplayName string `json:"display_name"`
 	OwnerUserID string `json:"owner_user_id"`
+	Kind        string `json:"kind"`
 	Role        string `json:"role,omitempty"`
 	CreatedAt   string `json:"created_at"`
 }
@@ -88,6 +96,7 @@ type workspaceDTO struct {
 	ID          string `json:"id"`
 	DisplayName string `json:"display_name"`
 	OwnerUserID string `json:"owner_user_id"`
+	Kind        string `json:"kind"`
 	Role        string `json:"role"`
 	JoinedAt    string `json:"joined_at"`
 }
@@ -114,16 +123,16 @@ type invitationDTO struct {
 }
 
 func (h *OrganizationHandler) create(c fiber.Ctx) error {
-	var req organizationRequest
+	var req createOrganizationRequest
 	if err := parseBody(c, &req); err != nil {
 		return err
 	}
-	org, err := h.svc.Create(c.Context(), middleware.GetUserID(c), h.callerName(c), req.DisplayName)
+	org, err := h.svc.CreateOfKind(c.Context(), req.Kind, middleware.GetUserID(c), h.callerName(c), req.DisplayName)
 	if err != nil {
 		return organizationProblem(c, err)
 	}
 	return c.Status(http.StatusCreated).JSON(organizationDTO{
-		ID: org.ID, DisplayName: org.DisplayName, OwnerUserID: org.OwnerUserID,
+		ID: org.ID, DisplayName: org.DisplayName, OwnerUserID: org.OwnerUserID, Kind: org.KindOf(),
 		Role: organization.RoleOwner, CreatedAt: org.CreatedAt.Format(time.RFC3339),
 	})
 }
@@ -132,15 +141,26 @@ func (h *OrganizationHandler) create(c fiber.Ctx) error {
 // sign-in. It carries the name and the caller's role together: a list of ids
 // cannot be rendered, a list of names cannot decide what to offer, and
 // fetching the halves separately turns one sign-in into N requests.
+//
+// It lists one kind at a time — organizations unless ?kind=personal asks for
+// spaces — so a space never reaches a screen built for organizations, here or
+// in a product that reads this list.
 func (h *OrganizationHandler) listMine(c fiber.Ctx) error {
+	kind, err := organization.NormalizeKind(c.Query("kind"))
+	if err != nil {
+		return apierror.ValidationFailed("kind must be organization or personal.", c.Path()).Send(c)
+	}
 	workspaces, err := h.svc.ListWorkspaces(c.Context(), middleware.GetUserID(c))
 	if err != nil {
 		return organizationProblem(c, err)
 	}
 	out := make([]workspaceDTO, 0, len(workspaces))
 	for _, w := range workspaces {
+		if w.Kind != kind {
+			continue
+		}
 		out = append(out, workspaceDTO{
-			ID: w.ID, DisplayName: w.DisplayName, OwnerUserID: w.OwnerUserID,
+			ID: w.ID, DisplayName: w.DisplayName, OwnerUserID: w.OwnerUserID, Kind: w.Kind,
 			Role: w.Role, JoinedAt: w.JoinedAt.Format(time.RFC3339),
 		})
 	}
@@ -153,7 +173,7 @@ func (h *OrganizationHandler) get(c fiber.Ctx) error {
 		return organizationProblem(c, err)
 	}
 	return c.JSON(organizationDTO{
-		ID: org.ID, DisplayName: org.DisplayName, OwnerUserID: org.OwnerUserID,
+		ID: org.ID, DisplayName: org.DisplayName, OwnerUserID: org.OwnerUserID, Kind: org.KindOf(),
 		Role: middleware.GetOrgRole(c), CreatedAt: org.CreatedAt.Format(time.RFC3339),
 	})
 }
@@ -321,6 +341,12 @@ func organizationProblem(c fiber.Ctx, err error) error {
 		return apierror.ValidationFailed("You cannot change your own role. Ask somebody who outranks you.", c.Path()).Send(c)
 	case errors.Is(err, organization.ErrOutranked):
 		return apierror.ValidationFailed("You can only manage people below your own role.", c.Path()).Send(c)
+	case errors.Is(err, organization.ErrInvalidKind):
+		return apierror.ValidationFailed("kind must be organization or personal.", c.Path()).Send(c)
+	case errors.Is(err, organization.ErrTransferNeedsFullAccess):
+		return apierror.ValidationFailed("A space can only be transferred to somebody with full access. Give them full access first.", c.Path()).Send(c)
+	case errors.Is(err, organization.ErrNoCompaniesInASpace):
+		return apierror.ValidationFailed("A space holds no companies, so an invitation to one cannot name any.", c.Path()).Send(c)
 	case errors.Is(err, organization.ErrNotGrantable):
 		return apierror.ValidationFailed("That role cannot be assigned. Ownership moves through transfer.", c.Path()).Send(c)
 	case errors.Is(err, organization.ErrNotAMember), errors.Is(err, organization.ErrForbidden), errors.Is(err, organization.ErrNotFound):
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/handler/ -run 'TestCreatingASpace|TestTheListIsSplit|TestASpaceReads|TestASpaceRefusesAdmin' -v && go vet ./... && go test ./...`
Expected: the listed tests PASS; `vet` silent; every package `ok`.

- [ ] **Step 5: Commit**

```bash
git add api/internal/handler/organization.go api/internal/handler/organization_kind_test.go
git commit -m "feat(organization): create spaces and list workspaces by kind"
```

### Task 3: Internal routes answer the kind

**Files:**
- Modify: `api/internal/handler/organization_internal.go`
- Test: `api/internal/handler/organization_internal_kind_test.go`

**Interfaces:**
- Consumes: Task 1: `MembershipOf`, `Workspace.Kind`. Task 2: `decodeJSON`, `listedWorkspace`. Test helper `newInternalMembershipApp` (existing).
- Produces:
- `GET /internal/organizations/:organization_id/members/:user_id` → `{member: true, role, kind}`; a refusal stays `{member: false}` with **no** kind.
- `GET /internal/users/:user_id/organizations` → every workspace, of both kinds, each with `kind` (ctech-billing builds its switcher from it).

Scopes unchanged. The user-organizations route is NOT filtered by kind: it is the product's list, and ctech-billing needs both kinds.

- [ ] **Step 1: Write the failing test** — `api/internal/handler/organization_internal_kind_test.go`

```go
package handler_test

import (
	"context"
	"net/http"
	"testing"

	orgDomain "gopkg.aoctech.app/account/api/internal/domain/organization"
	"gopkg.aoctech.app/account/api/internal/scopes"
)

// A product needs the kind to know what a role means: `member` is full access
// in a space and something narrower in an organization (ctech-billing ADR 0027).
func TestTheMembershipAnswerCarriesTheKind(t *testing.T) {
	a := newInternalMembershipApp(t)
	owner := a.registerUser(t, "kind-member@example.com", "Sup3rSecret!pass", "Dono")
	ctx := context.Background()
	space, _ := a.orgSvc.CreateOfKind(ctx, orgDomain.KindPersonal, owner.ID(), "Dono", "Casa")
	org, _ := a.orgSvc.Create(ctx, owner.ID(), "Dono", "CTech")
	internal := a.issueServiceToken(t, []string{scopes.InternalAccountOrgMember})

	for id, want := range map[string]string{space.ID: orgDomain.KindPersonal, org.ID: orgDomain.KindOrganization} {
		resp := a.do(t, http.MethodGet, "/v1.0/internal/organizations/"+id+"/members/"+owner.ID(), internal, "")
		var body map[string]any
		decodeJSON(t, resp, &body)
		if body["member"] != true || body["role"] != "owner" || body["kind"] != want {
			t.Fatalf("%s: got %+v, want member, owner, %s", id, body, want)
		}
	}
}

// A refusal still says nothing — no kind on {member:false}, or the field would
// tell a prober which ids are spaces.
func TestARefusalCarriesNoKind(t *testing.T) {
	a := newInternalMembershipApp(t)
	owner := a.registerUser(t, "kind-refusal@example.com", "Sup3rSecret!pass", "Dono")
	space, _ := a.orgSvc.CreateOfKind(context.Background(), orgDomain.KindPersonal, owner.ID(), "Dono", "Casa")
	internal := a.issueServiceToken(t, []string{scopes.InternalAccountOrgMember})

	resp := a.do(t, http.MethodGet, "/v1.0/internal/organizations/"+space.ID+"/members/usr_stranger", internal, "")
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["member"] != false {
		t.Fatalf("got %+v", body)
	}
	if _, present := body["kind"]; present {
		t.Fatalf("a refusal carries the kind: %+v", body)
	}
}

func TestTheUserOrganizationsListCarriesEveryKind(t *testing.T) {
	a := newInternalMembershipApp(t)
	owner := a.registerUser(t, "kind-list@example.com", "Sup3rSecret!pass", "Dono")
	ctx := context.Background()
	space, _ := a.orgSvc.CreateOfKind(ctx, orgDomain.KindPersonal, owner.ID(), "Dono", "Casa")
	org, _ := a.orgSvc.Create(ctx, owner.ID(), "Dono", "CTech")
	internal := a.issueServiceToken(t, []string{scopes.InternalAccountUserOrganizations})

	resp := a.do(t, http.MethodGet, "/v1.0/internal/users/"+owner.ID()+"/organizations", internal, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%s)", resp.StatusCode, bodyString(resp))
	}
	var body struct {
		Organizations []listedWorkspace `json:"organizations"`
	}
	decodeJSON(t, resp, &body)
	kinds := map[string]string{}
	for _, w := range body.Organizations {
		kinds[w.ID] = w.Kind
	}
	if kinds[space.ID] != orgDomain.KindPersonal || kinds[org.ID] != orgDomain.KindOrganization {
		t.Fatalf("kinds = %v", kinds)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/handler/ -run 'TestTheMembershipAnswerCarriesTheKind|TestARefusalCarriesNoKind|TestTheUserOrganizationsListCarriesEveryKind' -v`
Expected: FAIL — the membership answer and the user-organizations list carry no `kind`.

- [ ] **Step 3: Apply the implementation** — save as `task3.patch` and run `git apply task3.patch` from the repository root

```diff
diff --git a/api/internal/handler/organization_internal.go b/api/internal/handler/organization_internal.go
index c3e9786..943abe1 100644
--- a/api/internal/handler/organization_internal.go
+++ b/api/internal/handler/organization_internal.go
@@ -36,14 +36,18 @@ func (h *OrganizationHandler) RegisterInternal(v1 fiber.Router, auth, memberScop
 // must not reveal which organizations exist, and "not a member" is an answer
 // where a 404 would invite the caller to read a refusal and an outage alike.
 func (h *OrganizationHandler) internalMember(c fiber.Ctx) error {
-	role, err := h.svc.RoleOf(c.Context(), c.Params("organization_id"), c.Params("user_id"))
+	role, kind, err := h.svc.MembershipOf(c.Context(), c.Params("organization_id"), c.Params("user_id"))
 	if errors.Is(err, organization.ErrNotAMember) {
+		// No kind on a refusal: it would tell a prober which ids are spaces.
 		return c.JSON(fiber.Map{"member": false})
 	}
 	if err != nil {
 		return apierror.ServerError(c.Path()).WithCause(err).Send(c)
 	}
-	return c.JSON(fiber.Map{"member": true, "role": role})
+	// The kind travels with the role because a role alone does not say what it
+	// grants: `member` is full access in a space and narrower in an
+	// organization (ctech-billing ADR 0027).
+	return c.JSON(fiber.Map{"member": true, "role": role, "kind": kind})
 }
 
 // internalUserOrganizations lists the organizations a person belongs to, with
@@ -62,7 +66,7 @@ func (h *OrganizationHandler) internalUserOrganizations(c fiber.Ctx) error {
 	}
 	out := make([]fiber.Map, 0, len(workspaces))
 	for _, w := range workspaces {
-		out = append(out, fiber.Map{"id": w.ID, "display_name": w.DisplayName, "role": w.Role})
+		out = append(out, fiber.Map{"id": w.ID, "display_name": w.DisplayName, "kind": w.Kind, "role": w.Role})
 	}
 	return c.JSON(fiber.Map{"organizations": out})
 }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/handler/ -run 'TestTheMembershipAnswerCarriesTheKind|TestARefusalCarriesNoKind|TestTheUserOrganizationsListCarriesEveryKind' -v && go vet ./... && go test ./...`
Expected: the listed tests PASS; `vet` silent; every package `ok`.

- [ ] **Step 5: Commit**

```bash
git add api/internal/handler/organization_internal.go api/internal/handler/organization_internal_kind_test.go
git commit -m "feat(internal): membership and user-organizations routes answer the workspace kind"
```

### Task 4: Companies: a space refuses one, and the company routes report the kind

**Files:**
- Modify: `api/internal/handler/company.go`
- Modify: `api/internal/handler/company_internal.go`
- Test: `api/internal/handler/company_kind_test.go`

**Interfaces:**
- Consumes: Task 1: `(*Service).KindOf`, `KindPersonal`, `ErrNotFound`. Task 2: `decodeJSON`. Test helpers `newCompanyTestApp`, `newInternalReachApp` (existing).
- Produces:
- `POST /v1.0/organizations/:id/companies` on a space → 409 ("A space holds no companies…"), nothing written.
- `GET /internal/companies/:company_id/actors/:user_id` → `{may_act: true, organization_id, organization_kind}`; a refusal carries no kind; an edge whose workspace is gone answers `{may_act: false}`.
- `GET /internal/organizations/:organization_id/companies/:company_id` gains `organization_kind`.

`organization_kind` is for the DF-e's defence in depth (`ctech-dfe/docs/specs/2026-10-09-personal-workspaces-in-dfe.md`): if the 409 above ever regressed, the DF-e still refuses. `TestACompanyInASpaceIsReportedAsSuch` writes a company through the service directly to simulate exactly that regression.

- [ ] **Step 1: Write the failing test** — `api/internal/handler/company_kind_test.go`

```go
package handler_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	companyDomain "gopkg.aoctech.app/account/api/internal/domain/company"
	orgDomain "gopkg.aoctech.app/account/api/internal/domain/organization"
	"gopkg.aoctech.app/account/api/internal/scopes"
)

// A space holds no companies. Refused here, at the only route that adds one,
// so nothing fiscal can ever be issued in a household's name.
func TestASpaceRefusesACompany(t *testing.T) {
	a := newCompanyTestApp(t)
	owner := a.registerUser(t, "space-company@example.com", "Sup3rSecret!pass", "Dono")
	space, err := a.orgSvc.CreateOfKind(context.Background(), orgDomain.KindPersonal, owner.ID(), "Dono", "Casa")
	if err != nil {
		t.Fatal(err)
	}
	resp := a.do(t, http.MethodPost, "/v1.0/organizations/"+space.ID+"/companies", a.issueToken(t, owner.ID()),
		`{"tax_id":"11222333000181","legal_name":"Acme LTDA"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", resp.StatusCode, bodyString(resp))
	}
	if got, _ := a.repo.List(context.Background(), space.ID); len(got) != 0 {
		t.Fatalf("a company was written into a space: %+v", got)
	}
}

// The DF-e checks the kind on its own (ctech-dfe spec 2026-10-09), in case the
// refusal above ever regresses. Both company routes carry it.
func TestTheCompanyRoutesCarryTheOrganizationKind(t *testing.T) {
	a := newInternalReachApp(t)
	orgID, ownerID, _ := a.seedOrg(t, "company-kind@example.com")
	internal := a.issueServiceToken(t, []string{scopes.InternalAccountCompanyActor})
	real, err := companyDomain.NewService(a.repo, time.Now).
		Register(context.Background(), orgID, ownerID, "Dono", "11222333000181", "Acme LTDA", "")
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"/v1.0/internal/companies/" + real.ID + "/actors/" + ownerID,
		"/v1.0/internal/organizations/" + orgID + "/companies/" + real.ID,
	} {
		resp := a.do(t, http.MethodGet, path, internal, "")
		var body map[string]any
		decodeJSON(t, resp, &body)
		if body["organization_kind"] != orgDomain.KindOrganization {
			t.Fatalf("%s: got %+v, want organization_kind organization", path, body)
		}
	}
}

// If a company ever did end up in a space, the routes must say so rather than
// read it as an organization.
func TestACompanyInASpaceIsReportedAsSuch(t *testing.T) {
	a := newInternalReachApp(t)
	owner := a.registerUser(t, "company-in-space@example.com", "Sup3rSecret!pass", "Dono")
	space, _ := a.orgSvc.CreateOfKind(context.Background(), orgDomain.KindPersonal, owner.ID(), "Dono", "Casa")
	// Written straight through the company service, bypassing the handler's
	// refusal — the regression this field exists to survive.
	smuggled, err := companyDomain.NewService(a.repo, time.Now).
		Register(context.Background(), space.ID, owner.ID(), "Dono", "11222333000181", "Acme LTDA", "")
	if err != nil {
		t.Fatal(err)
	}
	internal := a.issueServiceToken(t, []string{scopes.InternalAccountCompanyActor})
	resp := a.do(t, http.MethodGet, "/v1.0/internal/companies/"+smuggled.ID+"/actors/"+owner.ID(), internal, "")
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["organization_kind"] != orgDomain.KindPersonal {
		t.Fatalf("got %+v, want organization_kind personal", body)
	}
}

// A refusal still carries nothing.
func TestAReachRefusalCarriesNoKind(t *testing.T) {
	a := newInternalReachApp(t)
	internal := a.issueServiceToken(t, []string{scopes.InternalAccountCompanyActor})
	resp := a.do(t, http.MethodGet, "/v1.0/internal/companies/cmp_x/actors/usr_x", internal, "")
	var body map[string]any
	decodeJSON(t, resp, &body)
	if _, present := body["organization_kind"]; present || body["may_act"] != false {
		t.Fatalf("got %+v", body)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/handler/ -run 'TestASpaceRefusesACompany|TestTheCompanyRoutesCarry|TestACompanyInASpace|TestAReachRefusalCarriesNoKind' -v`
Expected: FAIL — registering a company in a space answers 201, and the reach/identity answers carry no `organization_kind`.

- [ ] **Step 3: Apply the implementation** — save as `task4.patch` and run `git apply task4.patch` from the repository root

```diff
diff --git a/api/internal/handler/company.go b/api/internal/handler/company.go
index e26e884..184ea93 100644
--- a/api/internal/handler/company.go
+++ b/api/internal/handler/company.go
@@ -107,6 +107,17 @@ func (h *CompanyHandler) register(c fiber.Ctx) error {
 	if err := parseBody(c, &req); err != nil {
 		return err
 	}
+	// A space holds no companies (personal workspaces spec § 1). This is the
+	// only route that adds one, so refusing here is refusing everywhere — and
+	// the internal routes still report the kind, so a product can refuse on its
+	// own if this ever regresses.
+	kind, err := h.orgs.KindOf(c.Context(), middleware.GetOrgID(c))
+	if err != nil {
+		return apierror.ServerError(c.Path()).WithCause(err).Send(c)
+	}
+	if kind == organization.KindPersonal {
+		return apierror.Conflict("A space holds no companies. Create an organization to register one.", c.Path()).Send(c)
+	}
 	created, err := h.svc.Register(c.Context(), middleware.GetOrgID(c), middleware.GetUserID(c),
 		h.callerName(c), req.TaxID, req.LegalName, req.TradeName)
 	if err != nil {
diff --git a/api/internal/handler/company_internal.go b/api/internal/handler/company_internal.go
index ffb65d0..49d4c41 100644
--- a/api/internal/handler/company_internal.go
+++ b/api/internal/handler/company_internal.go
@@ -1,8 +1,11 @@
 package handler
 
 import (
+	"errors"
+
 	"github.com/gofiber/fiber/v3"
 	"gopkg.aoctech.app/account/api/internal/apierror"
+	"gopkg.aoctech.app/account/api/internal/domain/organization"
 )
 
 // RegisterInternal mounts the one service-to-service route a product needs:
@@ -56,12 +59,17 @@ func (h *CompanyHandler) internalIdentity(c fiber.Ctx) error {
 	if err != nil {
 		return apierror.NotFound("company", c.Path()).Send(c)
 	}
+	kind, err := h.orgs.KindOf(c.Context(), orgID)
+	if err != nil {
+		return apierror.NotFound("company", c.Path()).Send(c)
+	}
 	return c.JSON(fiber.Map{
-		"organization_id": orgID,
-		"tax_id":          company.TaxID,
-		"tax_id_kind":     company.TaxIDKind,
-		"legal_name":      company.LegalName,
-		"trade_name":      company.TradeName,
+		"organization_id":   orgID,
+		"organization_kind": kind,
+		"tax_id":            company.TaxID,
+		"tax_id_kind":       company.TaxIDKind,
+		"legal_name":        company.LegalName,
+		"trade_name":        company.TradeName,
 	})
 }
 
@@ -85,8 +93,17 @@ func (h *CompanyHandler) internalReach(c fiber.Ctx) error {
 		// No organization on a refusal: naming one would say the company exists.
 		return c.JSON(fiber.Map{"may_act": false})
 	}
-	// Reach and the organization, and nothing else. A role or a permission list
-	// here would be the platform holding the product's vocabulary
-	// (ctech-billing ADR 0023).
-	return c.JSON(fiber.Map{"may_act": true, "organization_id": orgID})
+	kind, err := h.orgs.KindOf(c.Context(), orgID)
+	if errors.Is(err, organization.ErrNotFound) {
+		// An edge left pointing at a workspace that is gone reaches nothing.
+		return c.JSON(fiber.Map{"may_act": false})
+	}
+	if err != nil {
+		return apierror.ServerError(c.Path()).WithCause(err).Send(c)
+	}
+	// Reach, the organization and its kind, and nothing else. A role or a
+	// permission list here would be the platform holding the product's
+	// vocabulary (ctech-billing ADR 0023). The kind is there so the DF-e can
+	// refuse a company in a space on its own (ctech-dfe spec 2026-10-09).
+	return c.JSON(fiber.Map{"may_act": true, "organization_id": orgID, "organization_kind": kind})
 }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/handler/ -run 'TestASpaceRefusesACompany|TestTheCompanyRoutesCarry|TestACompanyInASpace|TestAReachRefusalCarriesNoKind' -v && go vet ./... && go test ./...`
Expected: the listed tests PASS; `vet` silent; every package `ok`.

- [ ] **Step 5: Commit**

```bash
git add api/internal/handler/company.go api/internal/handler/company_internal.go api/internal/handler/company_kind_test.go
git commit -m "feat(company): a space holds no companies; company routes report the workspace kind"
```

### Task 5: Transferring a space leaves the former owner with full access

**Files:**
- Modify: `api/internal/domain/organization/kind_test.go`
- Modify: `api/internal/domain/organization/repository.go`
- Modify: `api/internal/domain/organization/service.go`
- Modify: `api/internal/domain/organization/service_test.go`
- Modify: `api/internal/handler/organization_test.go`
- Modify: `api/internal/middleware/organization_test.go`
- Modify: `api/cmd/migrate-dfe-orgs/fake_test.go`

**Interfaces:**
- Consumes: Task 1: `KindOf`, `ErrTransferNeedsFullAccess`, `seedSpace`.
- Produces: `Repository.TransferOwnership(ctx, orgID, fromUserID, toUserID, demoteTo string, now time.Time) error` — the former owner is demoted to `demoteTo`: `admin` in an organization (today's behaviour), `member` in a space. Every fake implementing `Repository` changes signature with it.

Found while planning the UI: the DynamoDB transaction hard-coded `:admin`, which would leave an admin behind in a space — a rung a space does not have. The condition expressions are unchanged, so two racing transfers still cannot both commit.

- [ ] **Step 1: Write the failing test** — append to `api/internal/domain/organization/kind_test.go` (apply with `git apply`)

```diff
diff --git a/api/internal/domain/organization/kind_test.go b/api/internal/domain/organization/kind_test.go
index e45e745..1782351 100644
--- a/api/internal/domain/organization/kind_test.go
+++ b/api/internal/domain/organization/kind_test.go
@@ -170,3 +170,32 @@ func TestListWorkspacesCarriesTheKind(t *testing.T) {
 		t.Fatalf("kinds = %v", kinds)
 	}
 }
+
+// Handing a space over leaves the former owner with full access, not as an
+// admin — a rung a space does not have.
+func TestTransferringASpaceLeavesTheFormerOwnerWithFullAccess(t *testing.T) {
+	svc, space := seedSpace(t, "usr_owner")
+	ctx := context.Background()
+	join(t, svc, space.ID, "usr_full", RoleMember)
+	if err := svc.Transfer(ctx, space.ID, "usr_owner", "usr_full"); err != nil {
+		t.Fatal(err)
+	}
+	if role, _ := svc.RoleOf(ctx, space.ID, "usr_owner"); role != RoleMember {
+		t.Fatalf("former owner = %q, want member", role)
+	}
+	if role, _ := svc.RoleOf(ctx, space.ID, "usr_full"); role != RoleOwner {
+		t.Fatalf("new owner = %q, want owner", role)
+	}
+}
+
+func TestTransferringAnOrganizationStillLeavesAnAdmin(t *testing.T) {
+	svc, org := seedOrg(t, "usr_owner")
+	ctx := context.Background()
+	join(t, svc, org.ID, "usr_2", RoleMember)
+	if err := svc.Transfer(ctx, org.ID, "usr_owner", "usr_2"); err != nil {
+		t.Fatal(err)
+	}
+	if role, _ := svc.RoleOf(ctx, org.ID, "usr_owner"); role != RoleAdmin {
+		t.Fatalf("former owner = %q, want admin", role)
+	}
+}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/domain/organization/ -run 'TestTransferring' -v`
Expected: FAIL — `former owner = "admin", want member`.

- [ ] **Step 3: Apply the implementation** — save as `task5.patch` and run `git apply task5.patch` from the repository root

```diff
diff --git a/api/cmd/migrate-dfe-orgs/fake_test.go b/api/cmd/migrate-dfe-orgs/fake_test.go
index c1e364c..ef852d0 100644
--- a/api/cmd/migrate-dfe-orgs/fake_test.go
+++ b/api/cmd/migrate-dfe-orgs/fake_test.go
@@ -75,7 +75,7 @@ func (f *fakeRepo) ListForUser(context.Context, string) ([]*orgDomain.Membership
 }
 func (f *fakeRepo) SetRole(context.Context, string, string, string) error  { return nil }
 func (f *fakeRepo) RemoveMembership(context.Context, string, string) error { return nil }
-func (f *fakeRepo) TransferOwnership(context.Context, string, string, string, time.Time) error {
+func (f *fakeRepo) TransferOwnership(context.Context, string, string, string, string, time.Time) error {
 	return nil
 }
 func (f *fakeRepo) PutInvitation(context.Context, *orgDomain.Invitation) error { return nil }
diff --git a/api/internal/domain/organization/repository.go b/api/internal/domain/organization/repository.go
index f291c24..2349c62 100644
--- a/api/internal/domain/organization/repository.go
+++ b/api/internal/domain/organization/repository.go
@@ -78,7 +78,9 @@ type Repository interface {
 	SetRole(ctx context.Context, orgID, userID, role string) error
 	RemoveMembership(ctx context.Context, orgID, userID string) error
 	RenameMember(ctx context.Context, userID, name string) error
-	TransferOwnership(ctx context.Context, orgID, fromUserID, toUserID string, now time.Time) error
+	// TransferOwnership demotes the old owner to demoteTo — admin in an
+	// organization, member in a space, which has no admin rung.
+	TransferOwnership(ctx context.Context, orgID, fromUserID, toUserID, demoteTo string, now time.Time) error
 	PutInvitation(ctx context.Context, inv *Invitation) error
 	GetInvitationByToken(ctx context.Context, tokenHash string) (*Invitation, error)
 	ListInvitations(ctx context.Context, orgID string) ([]*Invitation, error)
@@ -369,20 +371,20 @@ func (r *repo) RenameMember(ctx context.Context, userID, name string) error {
 // finds a role it did not expect and the whole transaction is rejected. Doing
 // this as three sequential writes would have two failure windows, and both
 // leave an organization with either two owners or none.
-func (r *repo) TransferOwnership(ctx context.Context, orgID, fromUserID, toUserID string, now time.Time) error {
+func (r *repo) TransferOwnership(ctx context.Context, orgID, fromUserID, toUserID, demoteTo string, now time.Time) error {
 	ownerVal := map[string]types.AttributeValue{":owner": &types.AttributeValueMemberS{Value: RoleOwner}}
 	demote := map[string]types.AttributeValue{
-		":owner": &types.AttributeValueMemberS{Value: RoleOwner},
-		":admin": &types.AttributeValueMemberS{Value: RoleAdmin},
+		":owner":  &types.AttributeValueMemberS{Value: RoleOwner},
+		":demote": &types.AttributeValueMemberS{Value: demoteTo},
 	}
 	roleName := map[string]string{"#role": "role"}
 
 	err := r.memberships.TransactWrite(ctx, []types.TransactWriteItem{
-		// The outgoing owner becomes an admin, not a stranger: taking away the
-		// workspace they built as a side effect of handing it over is a
-		// surprise nobody asked for.
+		// The outgoing owner stays in, one rung down (admin, or member in a
+		// space), not a stranger: taking away the workspace they built as a
+		// side effect of handing it over is a surprise nobody asked for.
 		r.memberships.BuildRawUpdateTxItem(orgPK(orgID), aws.String(memberSK(fromUserID)),
-			"SET #role = :admin", "attribute_exists(pk) AND #role = :owner", roleName, demote),
+			"SET #role = :demote", "attribute_exists(pk) AND #role = :owner", roleName, demote),
 		r.memberships.BuildRawUpdateTxItem(orgPK(orgID), aws.String(memberSK(toUserID)),
 			"SET #role = :owner", "attribute_exists(pk) AND #role <> :owner", roleName, ownerVal),
 		r.orgs.BuildRawUpdateTxItem(orgPK(orgID), aws.String(metaSK),
diff --git a/api/internal/domain/organization/service.go b/api/internal/domain/organization/service.go
index 02edb09..e0c5ad4 100644
--- a/api/internal/domain/organization/service.go
+++ b/api/internal/domain/organization/service.go
@@ -305,10 +305,14 @@ func (s *Service) Transfer(ctx context.Context, orgID, actorUserID, toUserID str
 	if err != nil {
 		return err
 	}
-	if kind == KindPersonal && toRole != RoleMember {
-		return ErrTransferNeedsFullAccess
+	demoteTo := RoleAdmin
+	if kind == KindPersonal {
+		if toRole != RoleMember {
+			return ErrTransferNeedsFullAccess
+		}
+		demoteTo = RoleMember
 	}
-	return s.repo.TransferOwnership(ctx, orgID, actorUserID, toUserID, s.now().UTC())
+	return s.repo.TransferOwnership(ctx, orgID, actorUserID, toUserID, demoteTo, s.now().UTC())
 }
 
 // requireGrantableIn refuses a role the workspace's kind does not have. An
diff --git a/api/internal/domain/organization/service_test.go b/api/internal/domain/organization/service_test.go
index 57037db..16b8a93 100644
--- a/api/internal/domain/organization/service_test.go
+++ b/api/internal/domain/organization/service_test.go
@@ -143,13 +143,13 @@ func (f *fakeRepo) RemoveMembership(_ context.Context, orgID, userID string) err
 	return nil
 }
 
-func (f *fakeRepo) TransferOwnership(_ context.Context, orgID, fromUserID, toUserID string, now time.Time) error {
+func (f *fakeRepo) TransferOwnership(_ context.Context, orgID, fromUserID, toUserID, demoteTo string, now time.Time) error {
 	from, okFrom := f.memberships[orgID][fromUserID]
 	to, okTo := f.memberships[orgID][toUserID]
 	if !okFrom || !okTo || from.Role != RoleOwner || to.Role == RoleOwner {
 		return ErrNotFound
 	}
-	from.Role = RoleAdmin
+	from.Role = demoteTo
 	to.Role = RoleOwner
 	if org, ok := f.orgs[orgID]; ok {
 		org.OwnerUserID = toUserID
diff --git a/api/internal/handler/organization_test.go b/api/internal/handler/organization_test.go
index cca1d32..c7c252a 100644
--- a/api/internal/handler/organization_test.go
+++ b/api/internal/handler/organization_test.go
@@ -146,13 +146,13 @@ func (m *memOrgRepo) RemoveMembership(_ context.Context, orgID, userID string) e
 	return nil
 }
 
-func (m *memOrgRepo) TransferOwnership(_ context.Context, orgID, fromUserID, toUserID string, now time.Time) error {
+func (m *memOrgRepo) TransferOwnership(_ context.Context, orgID, fromUserID, toUserID, demoteTo string, now time.Time) error {
 	from, okFrom := m.memberships[orgID][fromUserID]
 	to, okTo := m.memberships[orgID][toUserID]
 	if !okFrom || !okTo || from.Role != orgDomain.RoleOwner || to.Role == orgDomain.RoleOwner {
 		return orgDomain.ErrNotFound
 	}
-	from.Role, to.Role = orgDomain.RoleAdmin, orgDomain.RoleOwner
+	from.Role, to.Role = demoteTo, orgDomain.RoleOwner
 	if org, ok := m.orgs[orgID]; ok {
 		org.OwnerUserID, org.UpdatedAt = toUserID, now
 	}
diff --git a/api/internal/middleware/organization_test.go b/api/internal/middleware/organization_test.go
index 28dade7..8f01bcf 100644
--- a/api/internal/middleware/organization_test.go
+++ b/api/internal/middleware/organization_test.go
@@ -46,7 +46,7 @@ func (orgRepoStub) ListForUser(context.Context, string) ([]*organization.Members
 func (orgRepoStub) PutMembership(context.Context, *organization.Membership) error { return nil }
 func (orgRepoStub) SetRole(context.Context, string, string, string) error         { return nil }
 func (orgRepoStub) RemoveMembership(context.Context, string, string) error        { return nil }
-func (orgRepoStub) TransferOwnership(context.Context, string, string, string, time.Time) error {
+func (orgRepoStub) TransferOwnership(context.Context, string, string, string, string, time.Time) error {
 	return nil
 }
 func (orgRepoStub) PutInvitation(context.Context, *organization.Invitation) error { return nil }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/domain/organization/ -run 'TestTransferring' -v && go vet ./... && go test ./...`
Expected: the listed tests PASS; `vet` silent; every package `ok`.

- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/organization/kind_test.go api/internal/domain/organization/repository.go api/internal/domain/organization/service.go api/internal/domain/organization/service_test.go api/internal/handler/organization_test.go api/internal/middleware/organization_test.go api/cmd/migrate-dfe-orgs/fake_test.go
git commit -m "fix(organization): a transferred space leaves the former owner with full access, not admin"
```

---

## UI tasks (`ui/`)

Tasks 6–9 build the account UI for personal workspaces ("Espaços") on top of the backend contract from Tasks 1–5. Each task was applied in order on a clean checkout of `origin/main` and verified there: every new test failed before its implementation and passed after it, and after each task `npx vitest run`, `npx tsc --noEmit` and `npm run lint` were green (the final run: 122 tests). `next build --webpack` exported `/account/spaces`, `/account/spaces/new` and `/account/spaces/people` as static pages.

**Route deviation from the spec.** Spec §3.2 gives `/account/spaces/{id}/people`. Production is a static export (`next.config.ts`: `output: 'export'`), which has no dynamic segments, so the route here is **`/account/spaces/people?id={id}[&client_id&return_to&state]`**. This matches `/account/organizations/detail?id=`. Products that link here (ctech-billing) must build this URL.

**Copy mechanism.** The member, invitation and settings tabs are reused for spaces. They are not duplicated. Each tab reads `organization.kind` through `useWorkspaceT(kind)`. On a space, a key `organizations.X` (or `toast.X`) renders `spaces.X` when that key exists and falls back to the shared key otherwise. A test checks that no string under `spaces` says organização/empresa/CNPJ (or organization/company in `en`).

All commands run from `ui/`.

### Task 6: Workspace kind in the API client, space copy, and the "Espaços" nav entry

**Files:**
- Create: `ui/src/lib/workspace-copy.ts`
- Test: `ui/src/lib/workspace-kind.test.ts`
- Modify: `ui/src/lib/types.ts`, `ui/src/lib/queries.ts`, `ui/src/lib/mutations.ts`
- Modify: `ui/src/components/organization-role-badge.tsx`, `ui/src/components/account-nav.tsx`, `ui/src/components/route-title.tsx`
- Modify: `ui/src/locales/pt-BR.json`, `ui/src/locales/en.json`
- Test: `ui/src/components/account-nav.test.tsx`

**Interfaces:**
- Consumes: `GET /v1.0/organizations?kind=personal` → `{organizations: Organization[]}` (same envelope as the unfiltered list); `POST /v1.0/organizations` `{display_name, kind?}`; `kind` on every organization DTO (Task 1–5 contract).
- Produces:
  - `type OrganizationKind = 'organization' | 'personal'`; `Organization.kind?: OrganizationKind` (absent = organization)
  - `SPACE_GRANTABLE_ROLES`, `isPersonal(ws)`, `assignableRoles(callerRole, kind = 'organization')`, `canTransferTo(kind, role)` in `@/lib/types`
  - `fetchSpaces(): Promise<Organization[]>` in `@/lib/queries`
  - `createOrganizationAPI(displayName, kind?)` in `@/lib/mutations` (sends `kind` only when given)
  - `workspaceKey(key, kind, exists)` and `useWorkspaceT(kind)` in `@/lib/workspace-copy`
  - `<OrganizationRoleBadge role kind? />`
  - i18n: `nav.spaces` and the whole `spaces.*` namespace (used by Tasks 7–9)
  - nav item `/account/spaces` next to Organizações; route title for `/account/spaces*`

- [ ] **Step 1: Write the failing tests**

`ui/src/lib/workspace-kind.test.ts` (new file — complete content)

```ts
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {api} from './axios'
import {fetchSpaces} from './queries'
import {createOrganizationAPI} from './mutations'
import {assignableRoles, canTransferTo, isPersonal} from './types'
import {workspaceKey} from './workspace-copy'
import i18n from './i18n'
import ptBR from '@/locales/pt-BR.json'
import en from '@/locales/en.json'

vi.mock('./axios', () => ({
  api: {get: vi.fn(), post: vi.fn()},
  cnpjaApi: {get: vi.fn()},
  isAxiosError: vi.fn(),
}))

describe('workspace kind', () => {
  beforeEach(() => vi.clearAllMocks())

  // An absent kind is an organization: every row written before spaces existed
  // has none, and nothing is migrated.
  it('reads an absent kind as an organization', () => {
    expect(isPersonal({kind: undefined})).toBe(false)
    expect(isPersonal({kind: 'organization'})).toBe(false)
    expect(isPersonal({kind: 'personal'})).toBe(true)
  })

  // A space has two levels and only its owner hands them out. Offering admin
  // would be offering a choice the server answers with 422.
  it('lets only the owner of a space grant, and never admin', () => {
    expect(assignableRoles('owner', 'personal')).toEqual(['member', 'viewer'])
    expect(assignableRoles('member', 'personal')).toEqual([])
    expect(assignableRoles('viewer', 'personal')).toEqual([])
  })

  it('leaves the organization ladder exactly as it was', () => {
    expect(assignableRoles('owner')).toEqual(['admin', 'member', 'viewer'])
    expect(assignableRoles('admin', 'organization')).toEqual(['member', 'viewer'])
    expect(assignableRoles('member')).toEqual([])
  })

  // A viewer is promoted first; handing a space to somebody who can only read
  // it is refused by the server.
  it('transfers a space only to somebody with full access', () => {
    expect(canTransferTo('personal', 'member')).toBe(true)
    expect(canTransferTo('personal', 'viewer')).toBe(false)
    expect(canTransferTo('personal', 'owner')).toBe(false)
    expect(canTransferTo(undefined, 'viewer')).toBe(true)
    expect(canTransferTo('organization', 'owner')).toBe(false)
  })

  it('lists spaces through the kind filter', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: {organizations: [{id: 'org_s', display_name: 'Casa', kind: 'personal'}]},
    })
    await expect(fetchSpaces()).resolves.toEqual([
      {id: 'org_s', display_name: 'Casa', kind: 'personal'},
    ])
    expect(api.get).toHaveBeenCalledWith('/v1.0/organizations', {params: {kind: 'personal'}})
  })

  it('creates a space with the personal kind', async () => {
    vi.mocked(api.post).mockResolvedValue({data: {id: 'org_s'}})
    await createOrganizationAPI('Casa', 'personal')
    expect(api.post).toHaveBeenCalledWith('/v1.0/organizations', {
      display_name: 'Casa',
      kind: 'personal',
    })
  })

  // The organization paths never send a kind: the server's default is the
  // organization, and the organization handoff must never create a space.
  it('sends no kind when creating an organization', async () => {
    vi.mocked(api.post).mockResolvedValue({data: {id: 'org_o'}})
    await createOrganizationAPI('CTech')
    expect(api.post).toHaveBeenCalledWith('/v1.0/organizations', {display_name: 'CTech'})
  })
})

describe('space copy', () => {
  const exists = (key: string) => i18n.exists(key)

  it('swaps in the space wording where there is one', () => {
    expect(workspaceKey('organizations.settings.transfer', 'personal', exists)).toBe(
      'spaces.settings.transfer',
    )
    expect(workspaceKey('toast.transferFailed', 'personal', exists)).toBe(
      'spaces.toast.transferFailed',
    )
  })

  it('falls back to the shared wording where nothing differs', () => {
    expect(workspaceKey('organizations.members.user', 'personal', exists)).toBe(
      'organizations.members.user',
    )
  })

  it('never touches an organization', () => {
    expect(workspaceKey('organizations.settings.transfer', 'organization', exists)).toBe(
      'organizations.settings.transfer',
    )
    expect(workspaceKey('organizations.settings.transfer', undefined, exists)).toBe(
      'organizations.settings.transfer',
    )
  })

  // A space has none of an organization's vocabulary. A string that slips one
  // in tells a couple sharing a grocery budget they founded a company.
  it('never says organization or company in the space wording', () => {
    const strings = (node: unknown): string[] =>
      typeof node === 'string'
        ? [node]
        : Object.values(node as Record<string, unknown>).flatMap(strings)
    for (const text of strings(ptBR.spaces)) {
      expect(text).not.toMatch(/organiza|empresa|cnpj/i)
    }
    for (const text of strings(en.spaces)) {
      expect(text).not.toMatch(/organi[sz]ation|company|cnpj/i)
    }
  })
})
```

`ui/src/components/account-nav.test.tsx` — apply these exact replacements, in order:

1. Replace:

```tsx
    expect(screen.queryByRole('link', { name: /admin/i })).toBeNull()
  })
})
```

   With:

```tsx
    expect(screen.queryByRole('link', { name: /admin/i })).toBeNull()
  })

  // Spaces sit beside organizations, not inside them: a household budget is
  // not a company, and the list that says "organizations" never shows one.
  it('offers spaces as their own section', async () => {
    signedInAs('')
    renderNav()
    expect(await screen.findByRole('link', { name: /^spaces$/i })).toHaveAttribute(
      'href',
      '/account/spaces',
    )
  })
})
```


- [ ] **Step 2: Run them to verify they fail**
Run: `cd ui && npx vitest run src/lib/workspace-kind.test.ts src/components/account-nav.test.tsx`
Expected: FAIL. `workspace-kind.test.ts` fails with `Failed to resolve import "./workspace-copy"`. In `account-nav.test.tsx`, "offers spaces as their own section" fails with `Unable to find role="link" and name /^spaces$/i`. The two existing nav tests still pass.

- [ ] **Step 3: Write the implementation**

`ui/src/lib/workspace-copy.ts` (new file — complete content)

```ts
'use client'

import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import type { OrganizationKind } from './types'

/**
 * The key to render for a workspace of this kind. A space reads
 * `spaces.<rest>` where one exists — `organizations.settings.transfer` becomes
 * `spaces.settings.transfer`, `toast.transferFailed` becomes
 * `spaces.toast.transferFailed` — and the shared key where nothing differs
 * ("E-mail", "Expira em"). An organization is never touched.
 */
export function workspaceKey(
  key: string,
  kind: OrganizationKind | undefined,
  exists: (key: string) => boolean,
): string {
  if (kind !== 'personal') return key
  const spaceKey = `spaces.${key.replace(/^organizations\./, '')}`
  return exists(spaceKey) ? spaceKey : key
}

/**
 * `t` for a workspace: the same components render an organization and a space,
 * and a space must never be called an organization or offered a company.
 */
export function useWorkspaceT(kind: OrganizationKind | undefined) {
  const { t, i18n } = useTranslation()
  return useCallback(
    (key: string, options?: Record<string, unknown>) =>
      t(workspaceKey(key, kind, (k) => i18n.exists(k)), options),
    [kind, t, i18n],
  )
}
```

`ui/src/lib/types.ts` — apply these exact replacements, in order:

1. Replace:

```ts
export const GRANTABLE_ROLES: OrganizationRole[] = ['admin', 'member', 'viewer']

const ROLE_RANK: Record<OrganizationRole, number> = { viewer: 1, member: 2, admin: 3, owner: 4 }

```

   With:

```ts
export const GRANTABLE_ROLES: OrganizationRole[] = ['admin', 'member', 'viewer']

/**
 * What a workspace is. `organization` is a company's workspace; `personal` is
 * a space — a household, a shared budget — with no company and no admin. An
 * absent kind is an organization: rows written before spaces existed carry
 * none, and nothing was migrated.
 */
export type OrganizationKind = 'organization' | 'personal'

/** A space's two levels below the owner: full access and read-only. */
export const SPACE_GRANTABLE_ROLES: OrganizationRole[] = ['member', 'viewer']

export function isPersonal(workspace: { kind?: OrganizationKind }): boolean {
  return workspace.kind === 'personal'
}

const ROLE_RANK: Record<OrganizationRole, number> = { viewer: 1, member: 2, admin: 3, owner: 4 }

```

2. Replace:

```ts
 * broken.
 */
export function assignableRoles(callerRole: OrganizationRole): OrganizationRole[] {
  // The admin floor first: a member outranks a viewer but manages nobody.
  if (ROLE_RANK[callerRole] < ROLE_RANK.admin) return []
  return GRANTABLE_ROLES.filter((role) => outranks(callerRole, role))
}

```

   With:

```ts
 * broken.
 */
export function assignableRoles(
  callerRole: OrganizationRole,
  kind: OrganizationKind = 'organization',
): OrganizationRole[] {
  // A space has two levels and one person who hands them out. There is no
  // admin to fall back on, so nobody below the owner grants anything.
  if (kind === 'personal') return callerRole === 'owner' ? [...SPACE_GRANTABLE_ROLES] : []
  // The admin floor first: a member outranks a viewer but manages nobody.
  if (ROLE_RANK[callerRole] < ROLE_RANK.admin) return []
  return GRANTABLE_ROLES.filter((role) => outranks(callerRole, role))
}

/**
 * Whether ownership may go to somebody holding this role. Never to the owner
 * themselves; on a space, only to somebody with full access — a viewer is
 * promoted first, and the server refuses the shortcut.
 */
export function canTransferTo(kind: OrganizationKind | undefined, role: OrganizationRole): boolean {
  if (role === 'owner') return false
  return kind === 'personal' ? role === 'member' : true
}

```

3. Replace:

```ts
  role: OrganizationRole
  joined_at: string
}

```

   With:

```ts
  role: OrganizationRole
  joined_at: string
  /** Set at creation, never changed. Absent reads as `organization`. */
  kind?: OrganizationKind
}

```


`ui/src/lib/queries.ts` — apply these exact replacements, in order:

1. Replace:

```ts
}

export async function fetchOrganization(id: string): Promise<Organization> {
  const { data } = await api.get<Organization>(`/v1.0/organizations/${encodeURIComponent(id)}`)
```

   With:

```ts
}

/**
 * The person's spaces. The same endpoint as organizations, filtered by kind on
 * the server — the unfiltered list carries organizations only, so neither
 * screen has to sort the other's rows out.
 */
export async function fetchSpaces(): Promise<Organization[]> {
  const { data } = await api.get<{ organizations: Organization[] }>('/v1.0/organizations', {
    params: { kind: 'personal' },
  })
  return data.organizations ?? []
}

export async function fetchOrganization(id: string): Promise<Organization> {
  const { data } = await api.get<Organization>(`/v1.0/organizations/${encodeURIComponent(id)}`)
```


`ui/src/lib/mutations.ts` — apply these exact replacements, in order:

1. Replace:

```ts
import axios from 'axios'
import { api } from './axios'
import type { AdminKYCDocument, KYCBasicSubmission, KYCDocumentType, KYCRejectionCode, KYCReviewDecision, KYCStatus, OAuthClient, PresignedUpload, TermsPending, Organization, OrganizationMember, OrganizationRole, Company } from './types'

export async function loginAPI(email: string, password: string) {
```

   With:

```ts
import axios from 'axios'
import { api } from './axios'
import type { AdminKYCDocument, KYCBasicSubmission, KYCDocumentType, KYCRejectionCode, KYCReviewDecision, KYCStatus, OAuthClient, PresignedUpload, TermsPending, Organization, OrganizationKind, OrganizationMember, OrganizationRole, Company } from './types'

export async function loginAPI(email: string, password: string) {
```

2. Replace:

```ts
}

export async function createOrganizationAPI(displayName: string) {
  const { data } = await api.post<Organization>('/v1.0/organizations', { display_name: displayName })
  return data
}
```

   With:

```ts
}

/**
 * The kind is sent only for a space. Every organization path leaves it out and
 * gets the server's default, so the organization handoff cannot create a space
 * by accident.
 */
export async function createOrganizationAPI(displayName: string, kind?: OrganizationKind) {
  const { data } = await api.post<Organization>(
    '/v1.0/organizations',
    kind ? { display_name: displayName, kind } : { display_name: displayName },
  )
  return data
}
```


`ui/src/components/organization-role-badge.tsx` (complete new content)

```tsx
'use client'

import { Badge } from '@/components/ui/badge'
import { useWorkspaceT } from '@/lib/workspace-copy'
import type { OrganizationKind, OrganizationRole } from '@/lib/types'

/**
 * One vocabulary for the role, wherever it appears. Owner is the only one that
 * carries the accent — it is the role with the powers nobody else has, and
 * tinting all four would spend the accent on rank rather than on meaning
 * (DESIGN.md §2: cobalt on ≤10% of a screen).
 *
 * A space names the same roles differently — Dono, Acesso total, Leitura — so
 * the kind travels with the role.
 */
export function OrganizationRoleBadge({
  role,
  kind,
}: {
  role: OrganizationRole
  kind?: OrganizationKind
}) {
  const wt = useWorkspaceT(kind)
  return (
    <Badge variant={role === 'owner' ? 'default' : 'secondary'} className="text-xs">
      {wt(`organizations.roles.${role}`)}
    </Badge>
  )
}
```

`ui/src/components/account-nav.tsx` — apply these exact replacements, in order:

1. Replace:

```tsx
  LifeBuoy,
  ShieldCheck,
} from 'lucide-react'

```

   With:

```tsx
  LifeBuoy,
  ShieldCheck,
  Users,
} from 'lucide-react'

```

2. Replace:

```tsx
    { href: '/account/identity', label: t('nav.identity'), icon: IdCard },
    { href: '/account/organizations', label: t('nav.organizations'), icon: Building2 },
    { href: '/account/sessions', label: t('nav.sessions'), icon: MonitorSmartphone },
    { href: '/account/activity', label: t('nav.activity'), icon: Activity },
```

   With:

```tsx
    { href: '/account/identity', label: t('nav.identity'), icon: IdCard },
    { href: '/account/organizations', label: t('nav.organizations'), icon: Building2 },
    { href: '/account/spaces', label: t('nav.spaces'), icon: Users },
    { href: '/account/sessions', label: t('nav.sessions'), icon: MonitorSmartphone },
    { href: '/account/activity', label: t('nav.activity'), icon: Activity },
```


`ui/src/components/route-title.tsx` — apply these exact replacements, in order:

1. Replace:

```tsx
  ['/account/activity', (t) => t('nav.activity')],
  ['/account/organizations', (t) => t('nav.organizations')],
  ['/account/identity', (t) => t('nav.identity')],
  ['/account/profile', (t) => t('nav.profile')],
```

   With:

```tsx
  ['/account/activity', (t) => t('nav.activity')],
  ['/account/organizations', (t) => t('nav.organizations')],
  ['/account/spaces', (t) => t('nav.spaces')],
  ['/account/identity', (t) => t('nav.identity')],
  ['/account/profile', (t) => t('nav.profile')],
```


`ui/src/locales/pt-BR.json` — apply these exact replacements, in order:

1. Replace:

```json
    "menu": "Menu",
    "organizations": "Organizações",
    "admin": "Área administrativa"
  },
```

   With:

```json
    "menu": "Menu",
    "organizations": "Organizações",
    "spaces": "Espaços",
    "admin": "Área administrativa"
  },
```

2. Replace:

```json
      "companyRequiredInHandoff": "Informe o CNPJ ou CPF: o produto que te trouxe aqui precisa de uma empresa, não só de um espaço de trabalho."
    }
  }
}
```

   With:

```json
      "companyRequiredInHandoff": "Informe o CNPJ ou CPF: o produto que te trouxe aqui precisa de uma empresa, não só de um espaço de trabalho."
    }
  },
  "spaces": {
    "title": "Espaços",
    "subtitle": "Lugares para dividir com quem você quiser: a casa, um orçamento, um projeto.",
    "create": "Novo espaço",
    "role": "Acesso",
    "empty": {
      "title": "Você ainda não tem nenhum espaço",
      "body": "Um espaço serve para dividir algo com outras pessoas sem burocracia: a casa, um orçamento, um projeto paralelo. Dê um nome e convide quem quiser."
    },
    "roles": {
      "owner": "Dono",
      "admin": "Acesso total",
      "member": "Acesso total",
      "viewer": "Leitura"
    },
    "roleDescriptions": {
      "owner": "Faz tudo: convida, muda o acesso das pessoas, remove e passa o espaço adiante.",
      "member": "Usa o espaço por inteiro, mas não gerencia quem participa.",
      "viewer": "Consulta o que há no espaço, sem alterar nada."
    },
    "new": {
      "title": "Novo espaço",
      "body": "Só o nome. Depois você convida quem quiser.",
      "name": "Nome do espaço",
      "placeholder": "Casa, Viagem de julho, Orçamento",
      "submit": "Criar espaço",
      "failed": "Não foi possível criar o espaço.",
      "handoffBanner": "Criando um espaço para o {{product}}. Você volta para lá ao terminar.",
      "handoffInvalidTitle": "Este link não é válido",
      "handoffInvalidBody": "O produto que te trouxe aqui não está configurado corretamente. Você ainda pode criar um espaço pela sua conta.",
      "goToSpaces": "Ir para meus espaços"
    },
    "detail": {
      "back": "Todos os espaços",
      "members": "Pessoas",
      "invitations": "Convites",
      "settings": "Configurações",
      "noAccess": "Você não tem acesso a este espaço.",
      "readOnly": "Só o dono do espaço convida, muda o acesso ou remove pessoas.",
      "backTo": "Voltar ao {{product}}"
    },
    "members": {
      "empty": "Ninguém aqui ainda.",
      "changeRole": "Alterar acesso",
      "removeTitle": "Remover esta pessoa?",
      "removeBody": "A pessoa perde o acesso a este espaço na hora. Você pode convidá-la de novo depois."
    },
    "invitations": {
      "roleLabel": "Acesso"
    },
    "settings": {
      "transfer": "Transferir o espaço",
      "transferDescription": "Entregue este espaço a alguém com Acesso total. Você continua com Acesso total.",
      "transferConfirmBody": "Essa pessoa passa a ser a dona e você fica com Acesso total. Só ela pode devolver.",
      "leave": "Sair do espaço",
      "leaveDescription": "Você perde o acesso. Só o dono pode convidá-lo de volta.",
      "leaveConfirmTitle": "Sair deste espaço?",
      "ownerCannotLeave": "Você é o dono deste espaço, então não pode sair dele. Transfira-o antes para alguém com Acesso total — um espaço sem dono não tem quem convide, renomeie ou o repasse.",
      "noOtherMembers": "Ninguém aqui tem Acesso total. Dê Acesso total a alguém antes de transferir."
    },
    "toast": {
      "createOrganizationFailed": "Não foi possível criar o espaço.",
      "renameOrganizationFailed": "Não foi possível renomear o espaço.",
      "organizationRenamed": "Espaço renomeado.",
      "transferFailed": "Não foi possível transferir o espaço.",
      "ownershipTransferred": "Espaço transferido.",
      "leaveFailed": "Não foi possível sair do espaço.",
      "memberRemoved": "Pessoa removida.",
      "roleChanged": "Acesso atualizado.",
      "setRoleFailed": "Não foi possível alterar o acesso."
    }
  }
}
```


`ui/src/locales/en.json` — apply these exact replacements, in order:

1. Replace:

```json
    "menu": "Menu",
    "organizations": "Organizations",
    "admin": "Admin workspace"
  },
```

   With:

```json
    "menu": "Menu",
    "organizations": "Organizations",
    "spaces": "Spaces",
    "admin": "Admin workspace"
  },
```

2. Replace:

```json
      "companyRequiredInHandoff": "A CNPJ or CPF is required: the product that sent you here needs a company, not only a workspace."
    }
  }
}
```

   With:

```json
      "companyRequiredInHandoff": "A CNPJ or CPF is required: the product that sent you here needs a company, not only a workspace."
    }
  },
  "spaces": {
    "title": "Spaces",
    "subtitle": "Places to share with whoever you like: a home, a budget, a project.",
    "create": "New space",
    "role": "Access",
    "empty": {
      "title": "You do not have any space yet",
      "body": "A space is for sharing something with other people without the paperwork: a home, a budget, a side project. Name it and invite whoever you like."
    },
    "roles": {
      "owner": "Owner",
      "admin": "Full access",
      "member": "Full access",
      "viewer": "Read only"
    },
    "roleDescriptions": {
      "owner": "Does everything: invites, changes access, removes people and hands the space on.",
      "member": "Uses the whole space, but does not manage who is in it.",
      "viewer": "Sees what is in the space without changing anything."
    },
    "new": {
      "title": "New space",
      "body": "Just the name. You invite people afterwards.",
      "name": "Space name",
      "placeholder": "Home, July trip, Budget",
      "submit": "Create space",
      "failed": "Could not create the space.",
      "handoffBanner": "Creating a space for {{product}}. You will be returned there when you are done.",
      "handoffInvalidTitle": "This link is not valid",
      "handoffInvalidBody": "The product that sent you here is not configured correctly. You can still create a space from your account.",
      "goToSpaces": "Go to my spaces"
    },
    "detail": {
      "back": "All spaces",
      "members": "People",
      "invitations": "Invitations",
      "settings": "Settings",
      "noAccess": "You do not have access to this space.",
      "readOnly": "Only the owner of this space invites, changes access or removes people.",
      "backTo": "Back to {{product}}"
    },
    "members": {
      "empty": "Nobody here yet.",
      "changeRole": "Change access",
      "removeTitle": "Remove this person?",
      "removeBody": "They lose access to this space immediately. You can invite them again later."
    },
    "invitations": {
      "roleLabel": "Access"
    },
    "settings": {
      "transfer": "Transfer the space",
      "transferDescription": "Give this space to somebody with full access. You keep full access.",
      "transferConfirmBody": "They become the owner and you keep full access. Only they can transfer it back.",
      "leave": "Leave the space",
      "leaveDescription": "You lose access. Only the owner can invite you back.",
      "leaveConfirmTitle": "Leave this space?",
      "ownerCannotLeave": "You own this space, so you cannot leave it. Transfer it to somebody with full access first — a space with no owner has nobody who can invite, rename or hand it on.",
      "noOtherMembers": "Nobody here has full access. Give somebody full access before transferring."
    },
    "toast": {
      "createOrganizationFailed": "Could not create the space.",
      "renameOrganizationFailed": "Could not rename the space.",
      "organizationRenamed": "Space renamed.",
      "transferFailed": "Could not transfer the space.",
      "ownershipTransferred": "Space transferred.",
      "leaveFailed": "Could not leave the space.",
      "memberRemoved": "Person removed.",
      "roleChanged": "Access updated.",
      "setRoleFailed": "Could not change the access."
    }
  }
}
```


- [ ] **Step 4: Run the tests to verify they pass**
Run: `cd ui && npx vitest run src/lib/workspace-kind.test.ts src/components/account-nav.test.tsx`
Expected: PASS (14 tests).
Run: `cd ui && npx vitest run && npx tsc --noEmit && npm run lint`
Expected: every test passes, there are no type errors, and eslint prints nothing.

- [ ] **Step 5: Commit**
```bash
git add ui/src/lib/workspace-copy.ts ui/src/lib/workspace-kind.test.ts ui/src/lib/types.ts ui/src/lib/queries.ts ui/src/lib/mutations.ts ui/src/components/organization-role-badge.tsx ui/src/components/account-nav.tsx ui/src/components/account-nav.test.tsx ui/src/components/route-title.tsx ui/src/locales/pt-BR.json ui/src/locales/en.json
git commit -m "feat(ui): workspace kind in the API client, spaces copy and nav entry"
```

### Task 7: The spaces list and the space create handoff

**Files:**
- Create: `ui/src/app/account/spaces/page.tsx`
- Test: `ui/src/app/account/spaces/page.test.tsx`
- Create: `ui/src/app/account/spaces/new/page.tsx`
- Test: `ui/src/app/account/spaces/new/page.test.tsx`

**Interfaces:**
- Consumes: `fetchSpaces`, `createOrganizationAPI(name, 'personal')`, `OrganizationRoleBadge kind="personal"`, `spaces.*` copy (Task 6), and the existing `fetchHandoff(clientID, returnTo, state)` → `GET /v1.0/organizations/handoff`.
- Produces:
  - `/account/spaces`: a list with the role shown as Dono / Acesso total / Leitura, rows linking to `/account/spaces/people?id=…`, and a "Novo espaço" link to `/account/spaces/new`. React Query key `['spaces']`.
  - `/account/spaces/new[?client_id&return_to&state]` asks for the name only.
    - With a valid handoff: a banner "Criando um espaço para o {client_name}". On success, `window.location.replace(<echoed return_to>?organization_id=…&state=…)`. Cancel sends `?cancelled=1&state=…`.
    - With an invalid handoff: the same "link not valid" page as `organizations/new`, with a link to `/account/spaces`.
    - Without a handoff: it creates the space and goes to `/account/spaces`.

- [ ] **Step 1: Write the failing tests**

`ui/src/app/account/spaces/page.test.tsx` (new file — complete content)

```tsx
import {cleanup, render, screen, within} from '@testing-library/react'
import {QueryClient, QueryClientProvider} from '@tanstack/react-query'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import SpacesPage from './page'
import {fetchSpaces} from '@/lib/queries'

vi.mock('@/lib/queries', () => ({fetchSpaces: vi.fn()}))

afterEach(cleanup)

function renderPage() {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(
    <QueryClientProvider client={client}>
      <SpacesPage/>
    </QueryClientProvider>,
  )
}

describe('spaces', () => {
  beforeEach(() => vi.clearAllMocks())

  // A space's roles have their own names. "Member" and "Viewer" are an
  // organization's ladder; a household reads "Full access" and "Read only".
  it('lists each space with the space names for the role', async () => {
    vi.mocked(fetchSpaces).mockResolvedValue([
      {id: 'org_a', display_name: 'Casa', owner_user_id: 'usr_me', role: 'owner', kind: 'personal', joined_at: new Date().toISOString()},
      {id: 'org_b', display_name: 'Viagem', owner_user_id: 'usr_x', role: 'member', kind: 'personal', joined_at: new Date().toISOString()},
      {id: 'org_c', display_name: 'Orçamento', owner_user_id: 'usr_y', role: 'viewer', kind: 'personal', joined_at: new Date().toISOString()},
    ])
    renderPage()

    const table = within(await screen.findByRole('table'))
    expect(table.getByRole('link', {name: 'Casa'})).toHaveAttribute(
      'href', '/account/spaces/people?id=org_a',
    )
    expect(table.getByText('Owner')).toBeInTheDocument()
    expect(table.getByText('Full access')).toBeInTheDocument()
    expect(table.getByText('Read only')).toBeInTheDocument()
    expect(table.queryByText(/^member$|^viewer$/i)).toBeNull()
  })

  it('offers a new space from the list', async () => {
    vi.mocked(fetchSpaces).mockResolvedValue([
      {id: 'org_a', display_name: 'Casa', owner_user_id: 'usr_me', role: 'owner', kind: 'personal', joined_at: new Date().toISOString()},
    ])
    renderPage()
    expect(await screen.findByRole('link', {name: /new space/i})).toHaveAttribute(
      'href', '/account/spaces/new',
    )
  })

  // Having none is where everybody starts. The screen says what a space is for
  // and hands over the one action there is.
  it('teaches what a space is when there are none', async () => {
    vi.mocked(fetchSpaces).mockResolvedValue([])
    renderPage()
    expect(await screen.findByText(/do not have any space yet/i)).toBeInTheDocument()
    expect(screen.getByRole('link', {name: /new space/i})).toHaveAttribute(
      'href', '/account/spaces/new',
    )
    expect(screen.queryByRole('table')).toBeNull()
  })
})
```

`ui/src/app/account/spaces/new/page.test.tsx` (new file — complete content)

```tsx
import {cleanup, render, screen, waitFor} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {QueryClient, QueryClientProvider} from '@tanstack/react-query'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import NewSpacePage from './page'
import {fetchHandoff} from '@/lib/queries'
import {createOrganizationAPI} from '@/lib/mutations'

vi.mock('@/lib/queries', () => ({fetchHandoff: vi.fn()}))
vi.mock('@/lib/mutations', () => ({createOrganizationAPI: vi.fn()}))

const push = vi.fn()
let search = new URLSearchParams()

vi.mock('next/navigation', () => ({
  useRouter: () => ({push}),
  useSearchParams: () => search,
}))

afterEach(cleanup)

// The page leaves through window.location.replace, which jsdom does not
// implement. Captured rather than stubbed away: the exact URL is the contract
// with the product that sent us.
let replaced: string | null = null

function renderPage(query: string) {
  search = new URLSearchParams(query)
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(
    <QueryClientProvider client={client}>
      <NewSpacePage/>
    </QueryClientProvider>,
  )
}

describe('new space', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    replaced = null
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: {replace: (url: string) => { replaced = url }},
    })
    vi.mocked(createOrganizationAPI).mockResolvedValue({
      id: 'org_space', display_name: 'Casa', owner_user_id: 'usr_1',
      role: 'owner', kind: 'personal', joined_at: new Date().toISOString(),
    })
    vi.mocked(fetchHandoff).mockResolvedValue({
      client_name: 'Billing',
      return_to: 'https://billing.example/espacos/vincular',
    })
  })

  // The name and nothing else: no tax id, no company, none of an
  // organization's questions.
  it('asks for the name only', async () => {
    renderPage('')
    expect(await screen.findByLabelText(/space name/i)).toBeInTheDocument()
    expect(screen.getAllByRole('textbox')).toHaveLength(1)
    expect(screen.queryByText(/cnpj|company|organization/i)).toBeNull()
    expect(fetchHandoff).not.toHaveBeenCalled()
  })

  it('creates a personal workspace and goes to the spaces', async () => {
    const user = userEvent.setup()
    renderPage('')
    await user.type(await screen.findByLabelText(/space name/i), '  Casa  ')
    await user.click(screen.getByRole('button', {name: /create space/i}))

    await waitFor(() => expect(push).toHaveBeenCalledWith('/account/spaces'))
    expect(createOrganizationAPI).toHaveBeenCalledWith('Casa', 'personal')
    expect(replaced).toBeNull()
  })

  // The banner's product name comes from the server. A client_name in the
  // query string is a banner anybody can make say whatever they like.
  it('names the product from the server, never from the query string', async () => {
    renderPage('client_id=billing&return_to=https://billing.example/x&client_name=Banco%20Falso')
    expect(await screen.findByText(/creating a space for billing/i)).toBeInTheDocument()
    expect(screen.queryByText(/Banco Falso/)).toBeNull()
    expect(fetchHandoff).toHaveBeenCalledWith('billing', 'https://billing.example/x', '')
  })

  // The round trip: the new id and the echoed state, on the URL the server
  // validated — not the one in the address bar.
  it('returns the new id to the echoed return_to', async () => {
    const user = userEvent.setup()
    renderPage('client_id=billing&return_to=https://billing.example/raw&state=abc123')
    await user.type(await screen.findByLabelText(/space name/i), 'Casa')
    await user.click(screen.getByRole('button', {name: /create space/i}))

    await waitFor(() => expect(replaced).not.toBeNull())
    const url = new URL(replaced!)
    expect(url.origin + url.pathname).toBe('https://billing.example/espacos/vincular')
    expect(url.searchParams.get('organization_id')).toBe('org_space')
    expect(url.searchParams.get('state')).toBe('abc123')
    // A space has no company, so the product is never handed an empty one.
    expect(url.searchParams.has('company_id')).toBe(false)
    expect(push).not.toHaveBeenCalled()
  })

  // Cancel is a real action: the product has to be told, or it cannot put the
  // person back where they were.
  it('tells the product when somebody backs out', async () => {
    const user = userEvent.setup()
    renderPage('client_id=billing&return_to=https://billing.example/x&state=abc123')
    await screen.findByLabelText(/space name/i)
    await user.click(screen.getByRole('button', {name: /cancel/i}))

    await waitFor(() => expect(replaced).not.toBeNull())
    const url = new URL(replaced!)
    expect(url.searchParams.get('cancelled')).toBe('1')
    expect(url.searchParams.get('state')).toBe('abc123')
    expect(url.searchParams.get('organization_id')).toBeNull()
    expect(createOrganizationAPI).not.toHaveBeenCalled()
  })

  // A misconfigured integration strands nobody: no redirect anywhere, and a
  // way to do this from the account instead.
  it('strands nobody when the handoff is refused', async () => {
    vi.mocked(fetchHandoff).mockRejectedValue(new Error('422'))
    renderPage('client_id=billing&return_to=https://evil.example/x')
    expect(await screen.findByRole('link', {name: /my spaces/i})).toHaveAttribute(
      'href', '/account/spaces',
    )
    expect(screen.queryByLabelText(/space name/i)).toBeNull()
    expect(replaced).toBeNull()
  })
})
```

- [ ] **Step 2: Run them to verify they fail**
Run: `cd ui && npx vitest run src/app/account/spaces`
Expected: FAIL. Both files fail with `Failed to resolve import "./page"`.

- [ ] **Step 3: Write the implementation**

`ui/src/app/account/spaces/page.tsx` (new file — complete content)

```tsx
'use client'

import Link from 'next/link'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Plus, Users } from 'lucide-react'
import { fetchSpaces } from '@/lib/queries'
import { formatDate } from '@/lib/format'
import { cn } from '@/lib/utils'
import { QueryError } from '@/components/query-error'
import { ResponsiveDataList, type Column } from '@/components/responsive-data-list'
import { OrganizationRoleBadge } from '@/components/organization-role-badge'
import { buttonVariants } from '@/components/ui/button'
import type { Organization } from '@/lib/types'

/**
 * The person's spaces — personal workspaces, the same record as an
 * organization with a different kind. Its own section rather than a filter on
 * Organizações: a household budget is not a company, and a list that mixes the
 * two makes both read wrong.
 */
export default function SpacesPage() {
  const { t } = useTranslation()
  const { data: spaces = [], isLoading, isError, error, refetch } = useQuery({
    queryKey: ['spaces'],
    queryFn: fetchSpaces,
  })

  if (isLoading) {
    return (
      <div className="space-y-3">
        {[...Array(2)].map((_, i) => (
          <div key={i} className="h-20 animate-pulse bg-muted rounded-lg" />
        ))}
      </div>
    )
  }

  if (isError) {
    return <QueryError error={error} onRetry={() => refetch()} />
  }

  if (spaces.length === 0) {
    return (
      <div className="space-y-6">
        <Header />
        <div className="rounded-xl border bg-card px-6 py-12 text-center">
          <Users className="mx-auto size-8 opacity-40" />
          <h2 className="mt-4 text-base font-medium">{t('spaces.empty.title')}</h2>
          <p className="mx-auto mt-2 max-w-prose text-sm text-muted-foreground">
            {t('spaces.empty.body')}
          </p>
          <div className="mt-6 flex justify-center">
            <NewSpaceLink />
          </div>
        </div>
      </div>
    )
  }

  const columns: Column<Organization>[] = [
    {
      key: 'name',
      header: t('organizations.name'),
      title: true,
      cell: (space) => (
        <Link
          href={`/account/spaces/people?id=${encodeURIComponent(space.id)}`}
          className="block max-w-96 truncate text-sm font-medium text-primary hover:underline max-md:flex max-md:min-h-11 max-md:items-center"
          title={space.display_name}
        >
          {space.display_name}
        </Link>
      ),
    },
    {
      key: 'role',
      header: t('spaces.role'),
      cell: (space) => <OrganizationRoleBadge role={space.role} kind="personal" />,
    },
    {
      key: 'joined',
      header: t('organizations.joined'),
      cell: (space) => (
        <span className="text-sm text-muted-foreground">{formatDate(space.joined_at)}</span>
      ),
    },
  ]

  return (
    <div className="space-y-6">
      <Header action={<NewSpaceLink />} />
      <ResponsiveDataList rows={spaces} columns={columns} rowKey={(space) => space.id} />
    </div>
  )
}

/**
 * A link, not a dialog: /account/spaces/new is the one create screen, the
 * same one a product hands people to, so there is one form to keep right.
 */
function NewSpaceLink() {
  const { t } = useTranslation()
  return (
    <Link href="/account/spaces/new" className={cn(buttonVariants({ size: 'sm' }), 'max-sm:min-h-11')}>
      <Plus className="size-4" />
      {t('spaces.create')}
    </Link>
  )
}

function Header({ action }: { action?: React.ReactNode }) {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col items-start gap-4 sm:flex-row sm:items-center sm:justify-between">
      <div className="min-w-0">
        <h1 className="text-2xl font-semibold">{t('spaces.title')}</h1>
        <p className="mt-1 text-sm text-muted-foreground">{t('spaces.subtitle')}</p>
      </div>
      {action}
    </div>
  )
}
```

`ui/src/app/account/spaces/new/page.tsx` (new file — complete content)

```tsx
'use client'

import {Suspense, useState, type SyntheticEvent} from 'react'
import Link from 'next/link'
import {useRouter, useSearchParams} from 'next/navigation'
import {useMutation, useQuery, useQueryClient} from '@tanstack/react-query'
import {useTranslation} from 'react-i18next'
import {Users} from 'lucide-react'
import {fetchHandoff} from '@/lib/queries'
import {createOrganizationAPI} from '@/lib/mutations'
import {isAxiosError} from '@/lib/axios'
import {cn} from '@/lib/utils'
import {Button, buttonVariants} from '@/components/ui/button'
import {Input} from '@/components/ui/input'
import {Label} from '@/components/ui/label'
import {Alert, AlertDescription} from '@/components/ui/alert'

/** Matches the server's `validate:"required,max=120"`. */
const MAX_NAME = 120

/**
 * Creating a space, with an optional return trip — the organization handoff's
 * rules, with the name as the only question. Without handoff parameters this
 * is simply the create screen, which is what makes it safe to link to.
 */
export default function NewSpacePage() {
  return (
    <Suspense fallback={<div className="h-64 animate-pulse rounded-xl bg-muted"/>}>
      <NewSpace/>
    </Suspense>
  )
}

function NewSpace() {
  const {t} = useTranslation()
  const router = useRouter()
  const queryClient = useQueryClient()
  const params = useSearchParams()
  const clientID = params.get('client_id') ?? ''
  const rawReturnTo = params.get('return_to') ?? ''
  const state = params.get('state') ?? ''
  const isHandoff = !!clientID && !!rawReturnTo

  const [displayName, setDisplayName] = useState('')

  // The server decides whether this handoff is legitimate and what the product
  // is called — the same check the organization handoff uses.
  const {data: handoff, isLoading, isError} = useQuery({
    queryKey: ['handoff', clientID, rawReturnTo],
    queryFn: () => fetchHandoff(clientID, rawReturnTo, state),
    enabled: isHandoff,
    retry: false,
  })

  /**
   * Leaves through the URL the server echoed back, never the raw parameter.
   * `replace`, so Back in the product does not land on a create form whose
   * work is already done.
   */
  function leave(result: {organization_id: string} | 'cancelled') {
    if (!handoff) return
    const url = new URL(handoff.return_to)
    if (result === 'cancelled') {
      url.searchParams.set('cancelled', '1')
    } else {
      url.searchParams.set('organization_id', result.organization_id)
    }
    if (state) url.searchParams.set('state', state)
    window.location.replace(url.toString())
  }

  const {mutate, isPending, error} = useMutation({
    mutationFn: () => createOrganizationAPI(displayName.trim(), 'personal'),
    onSuccess: (space) => {
      void queryClient.invalidateQueries({queryKey: ['spaces']})
      if (isHandoff && handoff) {
        leave({organization_id: space.id})
        return
      }
      router.push('/account/spaces')
    },
  })

  const errorMsg = isAxiosError(error)
    ? (error.response?.data?.detail ?? t('spaces.new.failed'))
    : (error?.message ?? null)

  if (isHandoff && isLoading) {
    return <div className="h-64 animate-pulse rounded-xl bg-muted"/>
  }

  // A misconfigured integration strands nobody: the person is told, and given
  // the way to do this from their own account instead.
  if (isHandoff && (isError || !handoff)) {
    return (
      <div className="mx-auto max-w-md space-y-4 py-8 text-center">
        <Users className="mx-auto size-8 text-muted-foreground opacity-40"/>
        <h1 className="text-lg font-semibold">{t('spaces.new.handoffInvalidTitle')}</h1>
        <p className="text-sm text-muted-foreground">{t('spaces.new.handoffInvalidBody')}</p>
        <Link href="/account/spaces" className={cn(buttonVariants({variant: 'outline'}), 'max-sm:min-h-11')}>
          {t('spaces.new.goToSpaces')}
        </Link>
      </div>
    )
  }

  function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    mutate()
  }

  return (
    <div className="mx-auto max-w-md space-y-6 py-4">
      <div className="space-y-1">
        <h1 className="text-xl font-semibold tracking-tight">{t('spaces.new.title')}</h1>
        <p className="text-sm text-muted-foreground">{t('spaces.new.body')}</p>
      </div>

      {/* Somebody who tapped a button in another product and landed on a
          different domain needs to be told why, or it reads as a bug — or a
          phish. */}
      {handoff && (
        <Alert>
          <AlertDescription>
            {t('spaces.new.handoffBanner', {product: handoff.client_name})}
          </AlertDescription>
        </Alert>
      )}

      <form onSubmit={handleSubmit} className="space-y-4">
        {errorMsg && (
          <Alert variant="destructive">
            <AlertDescription>{errorMsg}</AlertDescription>
          </Alert>
        )}

        <div className="space-y-2">
          <Label htmlFor="space-name">{t('spaces.new.name')}</Label>
          <Input
            id="space-name"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            required
            maxLength={MAX_NAME}
            autoFocus
            autoComplete="off"
            placeholder={t('spaces.new.placeholder')}
            className="max-sm:h-11"
          />
        </div>

        <div className="flex items-center gap-2 pt-2">
          <Button type="submit" disabled={isPending} className="max-sm:min-h-11">
            {isPending ? t('common.saving') : t('spaces.new.submit')}
          </Button>
          {/* A real action, not a back button: the product that sent them has
              to be told, or it cannot put the person back where they were. */}
          {isHandoff ? (
            <Button type="button" variant="ghost" onClick={() => leave('cancelled')} className="max-sm:min-h-11">
              {t('common.cancel')}
            </Button>
          ) : (
            <Link href="/account/spaces" className={cn(buttonVariants({variant: 'ghost'}), 'max-sm:min-h-11')}>
              {t('common.cancel')}
            </Link>
          )}
        </div>
      </form>
    </div>
  )
}
```

- [ ] **Step 4: Run the tests to verify they pass**
Run: `cd ui && npx vitest run src/app/account/spaces`
Expected: PASS (9 tests).
Run: `cd ui && npx vitest run && npx tsc --noEmit && npm run lint`
Expected: everything is green.

- [ ] **Step 5: Commit**
```bash
git add ui/src/app/account/spaces/page.tsx ui/src/app/account/spaces/page.test.tsx ui/src/app/account/spaces/new/page.tsx ui/src/app/account/spaces/new/page.test.tsx
git commit -m "feat(ui): spaces list and the space create handoff"
```

### Task 8: The member, invitation and settings tabs read the workspace kind, and the organization page passes a space on

**Files:**
- Modify: `ui/src/app/account/organizations/detail/members-tab.tsx`
- Test: `ui/src/app/account/organizations/detail/members-tab.test.tsx`
- Modify: `ui/src/app/account/organizations/detail/invitations-tab.tsx`
- Test: `ui/src/app/account/organizations/detail/invitations-tab.test.tsx`
- Modify: `ui/src/app/account/organizations/detail/settings-tab.tsx`
- Test: `ui/src/app/account/organizations/detail/settings-tab.test.tsx` (new)
- Modify: `ui/src/app/account/organizations/detail/page.tsx`
- Test: `ui/src/app/account/organizations/detail/page.test.tsx` (new)

**Interfaces:**
- Consumes: `organization.kind`, `assignableRoles(role, kind)`, `canTransferTo`, `isPersonal`, `useWorkspaceT`, `OrganizationRoleBadge kind` (Task 6).
- Produces: the same components, `MembersTab`/`InvitationsTab`/`SettingsTab({organization})`. They need no new prop, because the kind travels on `organization.kind`. On a space:
  - only the owner gets role and remove controls, and the options are Acesso total and Leitura (never admin)
  - the invite dialog never fetches companies or shows a company picker
  - transfer lists only `member`s
  - only the owner renames
  - leaving goes to `/account/spaces`
  - copy comes from `spaces.*`
  - `['spaces']` is invalidated beside `['organizations']`

  `/account/organizations/detail?id=<space>` calls `router.replace('/account/spaces/people?id=<space>')`. This is how an accepted invitation to a space, which `/invite` sends to the organization page, reaches the right screen.

- [ ] **Step 1: Write the failing tests**

`ui/src/app/account/organizations/detail/members-tab.test.tsx` — apply these exact replacements, in order:

1. Replace:

```tsx
    expect(screen.queryByRole('option', { name: /^admin$/i })).not.toBeInTheDocument()
  })
})
```

   With:

```tsx
    expect(screen.queryByRole('option', { name: /^admin$/i })).not.toBeInTheDocument()
  })
})

describe('on a space', () => {
  function space(role: OrganizationRole): Organization {
    return {...organization(role), kind: 'personal'}
  }

  function renderSpace(role: OrganizationRole) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    return render(
      <QueryClientProvider client={client}>
        <MembersTab organization={space(role)} />
      </QueryClientProvider>,
    )
  }

  beforeEach(() => {
    vi.clearAllMocks()
    signedInAs('usr_me')
    vi.mocked(fetchOrganizationMembers).mockResolvedValue([
      { organization_id: 'org_1', user_id: 'usr_owner', name: 'Dona', role: 'owner', created_at: new Date().toISOString() },
      { organization_id: 'org_1', user_id: 'usr_me', name: 'Eu', role: 'member', created_at: new Date().toISOString() },
      { organization_id: 'org_1', user_id: 'usr_viewer', name: 'Leitor', role: 'viewer', created_at: new Date().toISOString() },
    ])
  })

  // Two levels and nothing else: admin would be a choice the server answers
  // with 422.
  it('offers the owner full access and read only, never admin', async () => {
    signedInAs('usr_owner')
    const user = userEvent.setup()
    renderSpace('owner')
    await screen.findAllByText('Leitor')

    const table = within(screen.getByRole('table'))
    const row = table.getByText('Leitor').closest('tr') as HTMLElement
    await user.click(within(row).getByRole('combobox', { name: /change access/i }))

    expect(await screen.findByRole('option', { name: /full access/i })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: /read only/i })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /admin/i })).not.toBeInTheDocument()
  })

  // Only the owner acts on a space. Full access outranks read only on the
  // ladder, but it does not manage anybody.
  it('gives somebody with full access no controls, even over a reader', async () => {
    renderSpace('member')
    await screen.findAllByText('Leitor')

    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /remove/i })).not.toBeInTheDocument()
  })

  it('names the roles the way a space does', async () => {
    renderSpace('viewer')
    await screen.findAllByText('Leitor')

    const table = within(screen.getByRole('table'))
    expect(table.getByText('Full access')).toBeInTheDocument()
    expect(table.getByText('Read only')).toBeInTheDocument()
    expect(table.getByRole('columnheader', { name: /access/i })).toBeInTheDocument()
    expect(table.queryByText(/^member$|^viewer$/i)).toBeNull()
  })
})
```


`ui/src/app/account/organizations/detail/invitations-tab.test.tsx` — apply these exact replacements, in order:

1. Replace:

```tsx
}

function renderTab() {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(
    <QueryClientProvider client={client}>
      <InvitationsTab organization={organization('owner')}/>
    </QueryClientProvider>,
  )
```

   With:

```tsx
}

function renderTab(workspace: Organization = organization('owner')) {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(
    <QueryClientProvider client={client}>
      <InvitationsTab organization={workspace}/>
    </QueryClientProvider>,
  )
```

2. Replace:

```tsx
  })
})
```

   With:

```tsx
  })
})

describe('inviting into a space', () => {
  const space: Organization = {...organization('owner'), kind: 'personal'}

  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(fetchOrganizationInvitations).mockResolvedValue([])
    vi.mocked(inviteMemberAPI).mockResolvedValue({token: 'tok', email: 'a@example.com', role: 'viewer'})
  })

  // A space never has a company. Asking for its companies would be a 409, and
  // a picker would be a question with no possible answer.
  it('never asks for companies', async () => {
    const user = userEvent.setup()
    renderTab(space)
    await user.click(await screen.findByRole('button', {name: /invite/i}))

    const dialog = within(screen.getByRole('dialog'))
    expect(dialog.queryAllByRole('checkbox')).toHaveLength(0)
    expect(dialog.queryByText(/compan/i)).toBeNull()
    expect(fetchCompanies).not.toHaveBeenCalled()
  })

  it('offers full access and read only, and sends the choice', async () => {
    const user = userEvent.setup()
    renderTab(space)
    await user.click(await screen.findByRole('button', {name: /invite/i}))
    await user.type(screen.getByLabelText(/e-?mail/i), 'mae@example.com')

    await user.click(screen.getByRole('combobox', {name: /access/i}))
    expect(await screen.findByRole('option', {name: /full access/i})).toBeInTheDocument()
    expect(screen.queryByRole('option', {name: /admin/i})).toBeNull()
    await user.click(screen.getByRole('option', {name: /read only/i}))
    await user.click(within(screen.getByRole('dialog')).getByRole('button', {name: /create invitation/i}))

    await waitFor(() =>
      expect(inviteMemberAPI).toHaveBeenCalledWith('org_1', 'mae@example.com', 'viewer', []),
    )
  })
})
```


`ui/src/app/account/organizations/detail/settings-tab.test.tsx` (new file — complete content)

```tsx
import {cleanup, render, screen} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {QueryClient, QueryClientProvider} from '@tanstack/react-query'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {SettingsTab} from './settings-tab'
import {fetchOrganizationMembers, fetchProfile} from '@/lib/queries'
import type {Organization, OrganizationRole} from '@/lib/types'

vi.mock('@/lib/queries', () => ({
  fetchOrganizationMembers: vi.fn(),
  fetchProfile: vi.fn(),
}))

vi.mock('@/lib/mutations', () => ({
  removeMemberAPI: vi.fn(),
  renameOrganizationAPI: vi.fn(),
  transferOwnershipAPI: vi.fn(),
}))

vi.mock('next/navigation', () => ({useRouter: () => ({push: vi.fn()})}))

afterEach(cleanup)

function workspace(role: OrganizationRole, kind?: Organization['kind']): Organization {
  return {
    id: 'org_1', display_name: 'Casa', owner_user_id: 'usr_owner',
    role, kind, joined_at: new Date().toISOString(),
  }
}

function renderTab(org: Organization) {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(
    <QueryClientProvider client={client}>
      <SettingsTab organization={org}/>
    </QueryClientProvider>,
  )
}

describe('settings on a space', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(fetchProfile).mockResolvedValue({user_id: 'usr_owner'} as never)
    vi.mocked(fetchOrganizationMembers).mockResolvedValue([
      {organization_id: 'org_1', user_id: 'usr_owner', name: 'Dona', role: 'owner', created_at: new Date().toISOString()},
      {organization_id: 'org_1', user_id: 'usr_full', name: 'Pedro', role: 'member', created_at: new Date().toISOString()},
      {organization_id: 'org_1', user_id: 'usr_read', name: 'Ana', role: 'viewer', created_at: new Date().toISOString()},
    ])
  })

  // The server refuses a transfer to a reader. Listing one is offering a
  // choice that always fails.
  it('offers ownership only to people with full access', async () => {
    const user = userEvent.setup()
    renderTab(workspace('owner', 'personal'))
    expect(await screen.findByRole('heading', {name: /transfer the space/i})).toBeInTheDocument()

    await user.click(await screen.findByRole('combobox', {name: /new owner/i}))
    expect(await screen.findByRole('option', {name: 'Pedro'})).toBeInTheDocument()
    expect(screen.queryByRole('option', {name: 'Ana'})).toBeNull()
  })

  it('says what to do when nobody has full access', async () => {
    vi.mocked(fetchOrganizationMembers).mockResolvedValue([
      {organization_id: 'org_1', user_id: 'usr_owner', name: 'Dona', role: 'owner', created_at: new Date().toISOString()},
      {organization_id: 'org_1', user_id: 'usr_read', name: 'Ana', role: 'viewer', created_at: new Date().toISOString()},
    ])
    renderTab(workspace('owner', 'personal'))
    expect(await screen.findByText(/nobody here has full access/i)).toBeInTheDocument()
  })

  // Renaming is the owner's on a space: there is no admin to share it with.
  it('does not offer a rename to somebody with full access', async () => {
    renderTab(workspace('member', 'personal'))
    expect(await screen.findByRole('heading', {name: /leave the space/i})).toBeInTheDocument()
    expect(screen.queryByRole('button', {name: /^save$/i})).toBeNull()
  })

  it('never calls a space an organization', async () => {
    renderTab(workspace('owner', 'personal'))
    await screen.findByRole('heading', {name: /transfer the space/i})
    expect(screen.queryByText(/organization/i)).toBeNull()
  })

  // The organization's ladder is untouched: a viewer can still be handed an
  // organization, as before.
  it('keeps every non-owner as a candidate on an organization', async () => {
    const user = userEvent.setup()
    renderTab(workspace('owner'))
    await user.click(await screen.findByRole('combobox', {name: /new owner/i}))
    expect(await screen.findByRole('option', {name: 'Ana'})).toBeInTheDocument()
    expect(screen.getByRole('option', {name: 'Pedro'})).toBeInTheDocument()
  })
})
```

`ui/src/app/account/organizations/detail/page.test.tsx` (new file — complete content)

```tsx
import {cleanup, render, screen, waitFor} from '@testing-library/react'
import {QueryClient, QueryClientProvider} from '@tanstack/react-query'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import OrganizationDetailPage from './page'
import {fetchOrganization} from '@/lib/queries'

// The tabs fetch their own data; they are not what this test is about.
vi.mock('./members-tab', () => ({MembersTab: () => <div>members</div>}))
vi.mock('./companies-tab', () => ({CompaniesTab: () => <div>companies</div>}))
vi.mock('./invitations-tab', () => ({InvitationsTab: () => <div>invitations</div>}))
vi.mock('./settings-tab', () => ({SettingsTab: () => <div>settings</div>}))

vi.mock('@/lib/queries', () => ({fetchOrganization: vi.fn()}))

const replace = vi.fn()
let search = new URLSearchParams()

vi.mock('next/navigation', () => ({
  useRouter: () => ({replace, push: vi.fn()}),
  useSearchParams: () => search,
}))

afterEach(cleanup)

function renderPage(query: string) {
  search = new URLSearchParams(query)
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(
    <QueryClientProvider client={client}>
      <OrganizationDetailPage/>
    </QueryClientProvider>,
  )
}

describe('organization detail', () => {
  beforeEach(() => vi.clearAllMocks())

  // An accepted invitation to a space lands here, because the invite page
  // cannot know the kind before it accepts. A space has no companies tab and
  // none of this page's vocabulary, so it goes to its own screen.
  it('sends a space to its people page', async () => {
    vi.mocked(fetchOrganization).mockResolvedValue({
      id: 'org_s', display_name: 'Casa', owner_user_id: 'usr_1', role: 'member',
      kind: 'personal', joined_at: new Date().toISOString(),
    })
    renderPage('id=org_s')
    await waitFor(() => expect(replace).toHaveBeenCalledWith('/account/spaces/people?id=org_s'))
    expect(screen.queryByRole('tab', {name: /companies/i})).toBeNull()
  })

  it('shows an organization as before, absent kind included', async () => {
    vi.mocked(fetchOrganization).mockResolvedValue({
      id: 'org_o', display_name: 'CTech', owner_user_id: 'usr_1', role: 'owner',
      joined_at: new Date().toISOString(),
    })
    renderPage('id=org_o')
    expect(await screen.findByRole('tab', {name: /companies/i})).toBeInTheDocument()
    expect(replace).not.toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: Run them to verify they fail**
Run: `cd ui && npx vitest run src/app/account/organizations/detail`
Expected: FAIL. Nine tests fail and 27 pass:
  - members-tab: "offers the owner full access and read only, never admin" and "names the roles the way a space does"
  - invitations-tab: "never asks for companies" and "offers full access and read only, and sends the choice"
  - settings-tab: the four space tests
  - page: "sends a space to its people page"

  Two of the new tests are regression guards that already pass: "gives somebody with full access no controls, even over a reader" and "keeps every non-owner as a candidate on an organization".

- [ ] **Step 3: Write the implementation**

`ui/src/app/account/organizations/detail/members-tab.tsx` — apply these exact replacements, in order:

1. Replace:

```tsx

import {useMutation, useQuery, useQueryClient} from '@tanstack/react-query'
import {useTranslation} from 'react-i18next'
import {Users} from 'lucide-react'
import {toast} from 'sonner'
import {fetchOrganizationMembers, fetchProfile} from '@/lib/queries'
import {removeMemberAPI, setMemberRoleAPI} from '@/lib/mutations'
import {formatDate} from '@/lib/format'
import {isAxiosError} from '@/lib/axios'
import {QueryError} from '@/components/query-error'
```

   With:

```tsx

import {useMutation, useQuery, useQueryClient} from '@tanstack/react-query'
import {Users} from 'lucide-react'
import {toast} from 'sonner'
import {fetchOrganizationMembers, fetchProfile} from '@/lib/queries'
import {removeMemberAPI, setMemberRoleAPI} from '@/lib/mutations'
import {formatDate} from '@/lib/format'
import {useWorkspaceT} from '@/lib/workspace-copy'
import {isAxiosError} from '@/lib/axios'
import {QueryError} from '@/components/query-error'
```

2. Replace:

```tsx
} from '@/lib/types'

export function MembersTab({organization}: { organization: Organization }) {
  const {t} = useTranslation()
  const queryClient = useQueryClient()
  const {data: profile} = useQuery({queryKey: ['profile'], queryFn: fetchProfile})
  // What this caller may hand out. Empty below admin, and never their own rank.
  const options = assignableRoles(organization.role)
  const canActOn = (m: OrganizationMember) =>
    !!profile && canManageMember(organization.role, profile.user_id, m)
```

   With:

```tsx
} from '@/lib/types'

/**
 * The roster of a workspace of either kind. A space (`kind: personal`) reads
 * its own vocabulary and its own rules: two levels, and only the owner acts.
 */
export function MembersTab({organization}: { organization: Organization }) {
  const t = useWorkspaceT(organization.kind)
  const queryClient = useQueryClient()
  const {data: profile} = useQuery({queryKey: ['profile'], queryFn: fetchProfile})
  // What this caller may hand out. Empty below admin, and never their own rank;
  // on a space, empty for everybody but the owner.
  const options = assignableRoles(organization.role, organization.kind)
  const canActOn = (m: OrganizationMember) =>
    !!profile && canManageMember(organization.role, profile.user_id, m)
```

3. Replace:

```tsx
    void queryClient.invalidateQueries({queryKey: ['organization', organization.id]})
    void queryClient.invalidateQueries({queryKey: ['organizations']})
  }

```

   With:

```tsx
    void queryClient.invalidateQueries({queryKey: ['organization', organization.id]})
    void queryClient.invalidateQueries({queryKey: ['organizations']})
    void queryClient.invalidateQueries({queryKey: ['spaces']})
  }

```

4. Replace:

```tsx
          </Select>
        ) : (
          <OrganizationRoleBadge role={m.role}/>
        ),
    },
```

   With:

```tsx
          </Select>
        ) : (
          <OrganizationRoleBadge role={m.role} kind={organization.kind}/>
        ),
    },
```


`ui/src/app/account/organizations/detail/invitations-tab.tsx` — apply these exact replacements, in order:

1. Replace:

```tsx
import { useState, type SyntheticEvent } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Copy, MailPlus, Send } from 'lucide-react'
import { toast } from 'sonner'
import { fetchOrganizationInvitations } from '@/lib/queries'
import { inviteMemberAPI, revokeInvitationAPI } from '@/lib/mutations'
import { fetchCompanies } from '@/lib/queries'
import { formatDate } from '@/lib/format'
import { isAxiosError } from '@/lib/axios'
import { QueryError } from '@/components/query-error'
```

   With:

```tsx
import { useState, type SyntheticEvent } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Copy, MailPlus, Send } from 'lucide-react'
import { toast } from 'sonner'
import { fetchOrganizationInvitations } from '@/lib/queries'
import { inviteMemberAPI, revokeInvitationAPI } from '@/lib/mutations'
import { fetchCompanies } from '@/lib/queries'
import { formatDate } from '@/lib/format'
import { useWorkspaceT } from '@/lib/workspace-copy'
import { isAxiosError } from '@/lib/axios'
import { QueryError } from '@/components/query-error'
```

2. Replace:

```tsx
  assignableRoles,
  formatTaxID,
  type Organization,
  type OrganizationInvitation,
```

   With:

```tsx
  assignableRoles,
  formatTaxID,
  isPersonal,
  type Organization,
  type OrganizationInvitation,
```

3. Replace:

```tsx

export function InvitationsTab({ organization }: { organization: Organization }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

```

   With:

```tsx

export function InvitationsTab({ organization }: { organization: Organization }) {
  const t = useWorkspaceT(organization.kind)
  const queryClient = useQueryClient()

```

4. Replace:

```tsx
      key: 'role',
      header: t('organizations.role'),
      cell: (inv) => <OrganizationRoleBadge role={inv.role} />,
    },
    {
```

   With:

```tsx
      key: 'role',
      header: t('organizations.role'),
      cell: (inv) => <OrganizationRoleBadge role={inv.role} kind={organization.kind} />,
    },
    {
```

5. Replace:

```tsx

function InviteDialog({ organization }: { organization: Organization }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [role, setRole] = useState<OrganizationRole>('member')
  // Inviting is granting, so the list stops below the caller's own rank —
  // exactly what SetRole offers, and exactly what the server accepts.
  const options = assignableRoles(organization.role)
  // Held in state, never re-fetchable: the server returns the token once and
  // stores only its hash.
```

   With:

```tsx

function InviteDialog({ organization }: { organization: Organization }) {
  const t = useWorkspaceT(organization.kind)
  // A space never has a company: its company routes answer 409, and a picker
  // would be a question with no possible answer.
  const personal = isPersonal(organization)
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [role, setRole] = useState<OrganizationRole>('member')
  // Inviting is granting, so the list stops below the caller's own rank —
  // exactly what SetRole offers, and exactly what the server accepts.
  const options = assignableRoles(organization.role, organization.kind)
  // Held in state, never re-fetchable: the server returns the token once and
  // stores only its hash.
```

6. Replace:

```tsx
    queryKey: ['companies', organization.id],
    queryFn: () => fetchCompanies(organization.id),
    enabled: open,
  })
  const normalizedSearch = companySearch.trim().toLocaleLowerCase()
```

   With:

```tsx
    queryKey: ['companies', organization.id],
    queryFn: () => fetchCompanies(organization.id),
    enabled: open && !personal,
  })
  const normalizedSearch = companySearch.trim().toLocaleLowerCase()
```

7. Replace:

```tsx
            </div>

            {(areCompaniesLoading || didCompaniesFail || companies.length > 0) && (
              <fieldset className="min-w-0 space-y-2">
                <legend className="text-sm font-medium">
```

   With:

```tsx
            </div>

            {!personal && (areCompaniesLoading || didCompaniesFail || companies.length > 0) && (
              <fieldset className="min-w-0 space-y-2">
                <legend className="text-sm font-medium">
```


`ui/src/app/account/organizations/detail/settings-tab.tsx` — apply these exact replacements, in order:

1. Replace:

```tsx
import {useRouter} from 'next/navigation'
import {useMutation, useQuery, useQueryClient} from '@tanstack/react-query'
import {useTranslation} from 'react-i18next'
import {toast} from 'sonner'
import {fetchOrganizationMembers, fetchProfile} from '@/lib/queries'
import {removeMemberAPI, renameOrganizationAPI, transferOwnershipAPI} from '@/lib/mutations'
import {isAxiosError} from '@/lib/axios'
import {ConfirmDialog} from '@/components/confirm-dialog'
import {QueryError} from '@/components/query-error'
```

   With:

```tsx
import {useRouter} from 'next/navigation'
import {useMutation, useQuery, useQueryClient} from '@tanstack/react-query'
import {toast} from 'sonner'
import {fetchOrganizationMembers, fetchProfile} from '@/lib/queries'
import {removeMemberAPI, renameOrganizationAPI, transferOwnershipAPI} from '@/lib/mutations'
import {isAxiosError} from '@/lib/axios'
import {useWorkspaceT} from '@/lib/workspace-copy'
import {ConfirmDialog} from '@/components/confirm-dialog'
import {QueryError} from '@/components/query-error'
```

2. Replace:

```tsx
import {Alert, AlertDescription} from '@/components/ui/alert'
import {Select, SelectContent, SelectItem, SelectTrigger, SelectValue} from '@/components/ui/select'
import type {Organization} from '@/lib/types'

export function SettingsTab({organization}: { organization: Organization }) {
  const {t} = useTranslation()
  const isOwner = organization.role === 'owner'
  const canManage = isOwner || organization.role === 'admin'

  return (
```

   With:

```tsx
import {Alert, AlertDescription} from '@/components/ui/alert'
import {Select, SelectContent, SelectItem, SelectTrigger, SelectValue} from '@/components/ui/select'
import {canTransferTo, isPersonal, type Organization} from '@/lib/types'

export function SettingsTab({organization}: { organization: Organization }) {
  const t = useWorkspaceT(organization.kind)
  const isOwner = organization.role === 'owner'
  // A space has no admin: its owner is the only one who renames it.
  const canManage = isOwner || (!isPersonal(organization) && organization.role === 'admin')

  return (
```

3. Replace:

```tsx

function RenameSection({organization}: { organization: Organization }) {
  const {t} = useTranslation()
  const queryClient = useQueryClient()

```

   With:

```tsx

function RenameSection({organization}: { organization: Organization }) {
  const t = useWorkspaceT(organization.kind)
  const queryClient = useQueryClient()

```

4. Replace:

```tsx
      queryClient.invalidateQueries({queryKey: ['organization', organization.id]})
      queryClient.invalidateQueries({queryKey: ['organizations']})
      toast.success(t('toast.organizationRenamed'))
    },
```

   With:

```tsx
      queryClient.invalidateQueries({queryKey: ['organization', organization.id]})
      queryClient.invalidateQueries({queryKey: ['organizations']})
      queryClient.invalidateQueries({queryKey: ['spaces']})
      toast.success(t('toast.organizationRenamed'))
    },
```

5. Replace:

```tsx

function TransferSection({organization}: { organization: Organization }) {
  const {t} = useTranslation()
  const queryClient = useQueryClient()
  const [target, setTarget] = useState('')
```

   With:

```tsx

function TransferSection({organization}: { organization: Organization }) {
  const t = useWorkspaceT(organization.kind)
  const queryClient = useQueryClient()
  const [target, setTarget] = useState('')
```

6. Replace:

```tsx

  // Never a free-text user id: the API requires an existing membership, and
  // typing an id is how an organization gets handed to a stranger.
  const candidates = members.filter((m) => m.role !== 'owner')

  const {mutateAsync, isPending} = useMutation({
```

   With:

```tsx

  // Never a free-text user id: the API requires an existing membership, and
  // typing an id is how an organization gets handed to a stranger. On a space,
  // only somebody with full access: the server refuses a reader.
  const candidates = members.filter((m) => canTransferTo(organization.kind, m.role))

  const {mutateAsync, isPending} = useMutation({
```

7. Replace:

```tsx
      void queryClient.invalidateQueries({queryKey: ['organization-members', organization.id]})
      void queryClient.invalidateQueries({queryKey: ['organizations']})
      setTarget('')
      toast.success(t('toast.ownershipTransferred'))
```

   With:

```tsx
      void queryClient.invalidateQueries({queryKey: ['organization-members', organization.id]})
      void queryClient.invalidateQueries({queryKey: ['organizations']})
      void queryClient.invalidateQueries({queryKey: ['spaces']})
      setTarget('')
      toast.success(t('toast.ownershipTransferred'))
```

8. Replace:

```tsx

function LeaveSection({organization}: { organization: Organization }) {
  const {t} = useTranslation()
  const router = useRouter()
  const queryClient = useQueryClient()
```

   With:

```tsx

function LeaveSection({organization}: { organization: Organization }) {
  const t = useWorkspaceT(organization.kind)
  const router = useRouter()
  const queryClient = useQueryClient()
```

9. Replace:

```tsx
    onSuccess: () => {
      queryClient.invalidateQueries({queryKey: ['organizations']})
      router.push('/account/organizations')
    },
    onError: (err) => {
```

   With:

```tsx
    onSuccess: () => {
      queryClient.invalidateQueries({queryKey: ['organizations']})
      queryClient.invalidateQueries({queryKey: ['spaces']})
      router.push(isPersonal(organization) ? '/account/spaces' : '/account/organizations')
    },
    onError: (err) => {
```


`ui/src/app/account/organizations/detail/page.tsx` — apply these exact replacements, in order:

1. Replace:

```tsx
'use client'

import { Suspense } from 'react'
import Link from 'next/link'
import { useSearchParams } from 'next/navigation'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
```

   With:

```tsx
'use client'

import { Suspense, useEffect } from 'react'
import Link from 'next/link'
import { useRouter, useSearchParams } from 'next/navigation'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
```

2. Replace:

```tsx
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { OrganizationRoleBadge } from '@/components/organization-role-badge'
import { MembersTab } from './members-tab'
import { CompaniesTab } from './companies-tab'
```

   With:

```tsx
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { OrganizationRoleBadge } from '@/components/organization-role-badge'
import { isPersonal } from '@/lib/types'
import { MembersTab } from './members-tab'
import { CompaniesTab } from './companies-tab'
```

3. Replace:

```tsx
function OrganizationDetail() {
  const { t } = useTranslation()
  const id = useSearchParams().get('id') ?? ''

```

   With:

```tsx
function OrganizationDetail() {
  const { t } = useTranslation()
  const router = useRouter()
  const id = useSearchParams().get('id') ?? ''

```

4. Replace:

```tsx
  const denied = id === '' || (isAxiosError(error) && error.response?.status === 403)

  if (denied) {
    return (
```

   With:

```tsx
  const denied = id === '' || (isAxiosError(error) && error.response?.status === 403)

  // A space is not an organization: no companies, no admin, none of this
  // page's words. Accepting an invitation lands here because the invite page
  // cannot know the kind before it accepts, so a space is passed on to its own
  // screen rather than rendered as something it is not.
  const space = !!organization && isPersonal(organization)
  useEffect(() => {
    if (space) router.replace(`/account/spaces/people?id=${encodeURIComponent(id)}`)
  }, [space, id, router])

  if (denied) {
    return (
```

5. Replace:

```tsx
  }

  if (isLoading || !organization) {
    return (
      <div className="space-y-6">
```

   With:

```tsx
  }

  if (isLoading || !organization || space) {
    return (
      <div className="space-y-6">
```


- [ ] **Step 4: Run the tests to verify they pass**
Run: `cd ui && npx vitest run src/app/account/organizations`
Expected: PASS (45 tests across the organization pages).
Run: `cd ui && npx vitest run && npx tsc --noEmit && npm run lint`
Expected: everything is green.

- [ ] **Step 5: Commit**
```bash
git add ui/src/app/account/organizations/detail/
git commit -m "feat(ui): member, invitation and settings tabs read the workspace kind; a space leaves the organization page"
```

### Task 9: The people page of a space, with the way back to the product

**Files:**
- Create: `ui/src/app/account/spaces/people/page.tsx`
- Test: `ui/src/app/account/spaces/people/page.test.tsx`

**Interfaces:**
- Consumes: `fetchOrganization(id)` (DTO with `kind`), `fetchHandoff`, and the kind-aware `MembersTab`, `InvitationsTab` and `SettingsTab` (Task 8).
- Produces: `/account/spaces/people?id={id}[&client_id&return_to&state]`.
  - Tabs: Pessoas, Convites (owner only) and Configurações. There is never a companies tab.
  - Non-owners see the read-only roster with the line "Só o dono do espaço convida, muda o acesso ou remove pessoas."
  - With a valid handoff: a "Voltar ao {client_name}" link to `<echoed return_to>?state=…`. A refused handoff hides that link and nothing else.
  - An organization id goes to `/account/organizations/detail?id=` through `router.replace`.
  - A 403 or 404 shows "Você não tem acesso a este espaço."

- [ ] **Step 1: Write the failing test**

`ui/src/app/account/spaces/people/page.test.tsx` (new file — complete content)

```tsx
import {cleanup, render, screen, waitFor} from '@testing-library/react'
import {QueryClient, QueryClientProvider} from '@tanstack/react-query'
import {AxiosError, AxiosHeaders} from 'axios'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import SpacePeoplePage from './page'
import {fetchHandoff, fetchOrganization} from '@/lib/queries'
import type {Organization, OrganizationRole} from '@/lib/types'

// The tabs are the organization page's own, tested there with a space. This
// test is about the page around them.
vi.mock('@/app/account/organizations/detail/members-tab', () => ({
  MembersTab: ({organization}: {organization: Organization}) => <div>members of {organization.kind}</div>,
}))
vi.mock('@/app/account/organizations/detail/invitations-tab', () => ({
  InvitationsTab: () => <div>invitations</div>,
}))
vi.mock('@/app/account/organizations/detail/settings-tab', () => ({
  SettingsTab: () => <div>settings</div>,
}))

vi.mock('@/lib/queries', () => ({
  fetchOrganization: vi.fn(),
  fetchHandoff: vi.fn(),
}))

const replace = vi.fn()
let search = new URLSearchParams()

vi.mock('next/navigation', () => ({
  useRouter: () => ({replace, push: vi.fn()}),
  useSearchParams: () => search,
}))

afterEach(cleanup)

function space(role: OrganizationRole): Organization {
  return {
    id: 'org_s', display_name: 'Casa', owner_user_id: 'usr_owner',
    role, kind: 'personal', joined_at: new Date().toISOString(),
  }
}

function renderPage(query: string) {
  search = new URLSearchParams(query)
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(
    <QueryClientProvider client={client}>
      <SpacePeoplePage/>
    </QueryClientProvider>,
  )
}

describe('space people', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(fetchHandoff).mockResolvedValue({
      client_name: 'Billing',
      return_to: 'https://billing.example/espacos',
    })
  })

  it('gives the owner the roster, the invitations and the settings', async () => {
    vi.mocked(fetchOrganization).mockResolvedValue(space('owner'))
    renderPage('id=org_s')

    expect(await screen.findByRole('heading', {name: 'Casa'})).toBeInTheDocument()
    expect(screen.getByRole('tab', {name: /people/i})).toBeInTheDocument()
    expect(screen.getByRole('tab', {name: /invitations/i})).toBeInTheDocument()
    expect(screen.getByRole('tab', {name: /settings/i})).toBeInTheDocument()
    expect(screen.getByText('members of personal')).toBeInTheDocument()
    // A space never has a company.
    expect(screen.queryByRole('tab', {name: /compan/i})).toBeNull()
    expect(screen.getByRole('link', {name: /all spaces/i})).toHaveAttribute('href', '/account/spaces')
  })

  // Only the owner acts. Everybody else reads the roster and is told why
  // there is nothing to press, rather than left to wonder.
  it('gives anybody else the read-only roster and says so', async () => {
    vi.mocked(fetchOrganization).mockResolvedValue(space('member'))
    renderPage('id=org_s')

    expect(await screen.findByText(/only the owner of this space/i)).toBeInTheDocument()
    expect(screen.queryByRole('tab', {name: /invitations/i})).toBeNull()
    expect(screen.getByText('Full access')).toBeInTheDocument()
  })

  // The way back goes through the URL the server validated, with the state
  // echoed — never the raw return_to in the address bar.
  it('offers the way back to the product that sent the person', async () => {
    vi.mocked(fetchOrganization).mockResolvedValue(space('owner'))
    renderPage('id=org_s&client_id=billing&return_to=https://billing.example/raw&state=abc123')

    const back = await screen.findByRole('link', {name: /back to billing/i})
    const url = new URL(back.getAttribute('href')!)
    expect(url.origin + url.pathname).toBe('https://billing.example/espacos')
    expect(url.searchParams.get('state')).toBe('abc123')
    expect(fetchHandoff).toHaveBeenCalledWith('billing', 'https://billing.example/raw', 'abc123')
  })

  // A refused handoff offers no way back anywhere — but the people are still
  // the person's to manage, so the page itself stays.
  it('offers no way back when the handoff is refused', async () => {
    vi.mocked(fetchOrganization).mockResolvedValue(space('owner'))
    vi.mocked(fetchHandoff).mockRejectedValue(new Error('422'))
    renderPage('id=org_s&client_id=billing&return_to=https://evil.example/x')

    expect(await screen.findByRole('heading', {name: 'Casa'})).toBeInTheDocument()
    await waitFor(() => expect(fetchHandoff).toHaveBeenCalled())
    expect(screen.queryByRole('link', {name: /back to/i})).toBeNull()
  })

  it('asks the server nothing about a handoff when there is none', async () => {
    vi.mocked(fetchOrganization).mockResolvedValue(space('owner'))
    renderPage('id=org_s')
    await screen.findByRole('heading', {name: 'Casa'})
    expect(fetchHandoff).not.toHaveBeenCalled()
  })

  // An organization id typed into this URL goes to the organization's page:
  // this screen has no companies tab and calls everything a space.
  it('passes an organization on to its own page', async () => {
    // An absent kind is an organization.
    vi.mocked(fetchOrganization).mockResolvedValue({...space('owner'), kind: undefined})
    renderPage('id=org_s')
    await waitFor(() =>
      expect(replace).toHaveBeenCalledWith('/account/organizations/detail?id=org_s'),
    )
    expect(screen.queryByRole('heading', {name: 'Casa'})).toBeNull()
  })

  // The server refuses to say whether a space exists or the person is not in
  // it, so this screen does not either.
  it('says the same thing for a missing space and a refused one', async () => {
    vi.mocked(fetchOrganization).mockRejectedValue(
      new AxiosError('forbidden', '403', undefined, undefined, {
        status: 403, statusText: 'Forbidden', data: {}, headers: {}, config: {headers: new AxiosHeaders()},
      }),
    )
    renderPage('id=org_x')
    expect(await screen.findByText(/do not have access to this space/i)).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run it to verify it fails**
Run: `cd ui && npx vitest run src/app/account/spaces/people`
Expected: FAIL with `Failed to resolve import "./page"`.

- [ ] **Step 3: Write the implementation**

`ui/src/app/account/spaces/people/page.tsx` (new file — complete content)

```tsx
'use client'

import { Suspense, useEffect } from 'react'
import Link from 'next/link'
import { useRouter, useSearchParams } from 'next/navigation'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { ArrowLeft } from 'lucide-react'
import { fetchHandoff, fetchOrganization } from '@/lib/queries'
import { isAxiosError } from '@/lib/axios'
import { isPersonal } from '@/lib/types'
import { QueryError } from '@/components/query-error'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { OrganizationRoleBadge } from '@/components/organization-role-badge'
import { MembersTab } from '@/app/account/organizations/detail/members-tab'
import { InvitationsTab } from '@/app/account/organizations/detail/invitations-tab'
import { SettingsTab } from '@/app/account/organizations/detail/settings-tab'

/**
 * The people of one space: roster, invitations, access levels, transfer.
 *
 * `?id=` rather than `/account/spaces/{id}/people`: production is a static
 * export (`next.config.ts`: `output: 'export'`), which has no dynamic
 * segments — the same shape `/account/organizations/detail` uses. A product
 * handing somebody here passes `client_id`, `return_to` and `state` beside it.
 *
 * The tabs are the organization page's own, which read the workspace's kind:
 * one implementation of invitations and roles, not two drifting apart.
 */
export default function SpacePeoplePage() {
  return (
    <Suspense fallback={<div className="h-40 animate-pulse rounded-lg bg-muted" />}>
      <SpacePeople />
    </Suspense>
  )
}

function SpacePeople() {
  const { t } = useTranslation()
  const router = useRouter()
  const params = useSearchParams()
  const id = params.get('id') ?? ''
  const clientID = params.get('client_id') ?? ''
  const rawReturnTo = params.get('return_to') ?? ''
  const state = params.get('state') ?? ''
  const isHandoff = !!clientID && !!rawReturnTo

  const { data: space, isLoading, isError, error, refetch } = useQuery({
    queryKey: ['organization', id],
    queryFn: () => fetchOrganization(id),
    enabled: id !== '',
    retry: false,
  })

  // The way back is offered only once the server has vouched for it. A
  // refused handoff hides the link and nothing else: the people are still the
  // person's to manage.
  const { data: handoff } = useQuery({
    queryKey: ['handoff', clientID, rawReturnTo],
    queryFn: () => fetchHandoff(clientID, rawReturnTo, state),
    enabled: isHandoff,
    retry: false,
  })

  // An organization's id typed into this URL belongs on the organization's
  // page, which has its companies and its own words.
  const organization = !!space && !isPersonal(space)
  useEffect(() => {
    if (organization) router.replace(`/account/organizations/detail?id=${encodeURIComponent(id)}`)
  }, [organization, id, router])

  // The server answers 403 for "not a member" and for "no such space" alike,
  // and this screen does not tell them apart either.
  const status = isAxiosError(error) ? error.response?.status : undefined
  const denied = id === '' || status === 403 || status === 404

  if (denied) {
    return (
      <div className="space-y-6">
        <BackLink />
        <Alert>
          <AlertDescription>{t('spaces.detail.noAccess')}</AlertDescription>
        </Alert>
      </div>
    )
  }

  if (isError) {
    return <QueryError error={error} onRetry={() => refetch()} />
  }

  if (isLoading || !space || organization) {
    return (
      <div className="space-y-6">
        <BackLink />
        <div className="h-40 animate-pulse rounded-lg bg-muted" />
      </div>
    )
  }

  const isOwner = space.role === 'owner'
  // Built from the URL the server echoed, never the raw parameter, with the
  // state handed back untouched.
  const returnURL = handoff ? new URL(handoff.return_to) : null
  if (returnURL && state) returnURL.searchParams.set('state', state)

  return (
    <div className="space-y-6">
      <div className="space-y-3">
        <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
          {returnURL && handoff && (
            <a
              href={returnURL.toString()}
              className="inline-flex items-center gap-1.5 text-sm font-medium text-primary hover:underline max-sm:min-h-11"
            >
              <ArrowLeft className="size-3.5" />
              {t('spaces.detail.backTo', { product: handoff.client_name })}
            </a>
          )}
          <BackLink />
        </div>
        <div className="flex min-w-0 flex-wrap items-center gap-3">
          <h1 className="min-w-0 text-balance text-2xl font-semibold [overflow-wrap:anywhere]">
            {space.display_name}
          </h1>
          <OrganizationRoleBadge role={space.role} kind="personal" />
        </div>
        {/* Said once, at the top, instead of leaving a column with nothing to
            press and no reason given. */}
        {!isOwner && (
          <p className="max-w-prose text-sm text-muted-foreground">{t('spaces.detail.readOnly')}</p>
        )}
      </div>

      <Tabs defaultValue="members">
        <TabsList className="min-h-11 w-full max-w-full justify-start overflow-x-auto overflow-y-hidden md:min-h-0">
          <TabsTrigger value="members" className="min-h-11 shrink-0 px-3 md:min-h-0">{t('spaces.detail.members')}</TabsTrigger>
          {/* Pending invitations are addresses of people who have not joined
              yet, and only the owner invites — so only the owner sees them. */}
          {isOwner && (
            <TabsTrigger value="invitations" className="min-h-11 shrink-0 px-3 md:min-h-0">{t('spaces.detail.invitations')}</TabsTrigger>
          )}
          <TabsTrigger value="settings" className="min-h-11 shrink-0 px-3 md:min-h-0">{t('spaces.detail.settings')}</TabsTrigger>
        </TabsList>

        <TabsContent value="members" className="mt-6">
          <MembersTab organization={space} />
        </TabsContent>
        {isOwner && (
          <TabsContent value="invitations" className="mt-6">
            <InvitationsTab organization={space} />
          </TabsContent>
        )}
        <TabsContent value="settings" className="mt-6">
          <SettingsTab organization={space} />
        </TabsContent>
      </Tabs>
    </div>
  )
}

function BackLink() {
  const { t } = useTranslation()
  return (
    <Link
      href="/account/spaces"
      className="inline-flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground max-sm:min-h-11"
    >
      <ArrowLeft className="size-3.5" />
      {t('spaces.detail.back')}
    </Link>
  )
}
```

- [ ] **Step 4: Run the tests to verify they pass**
Run: `cd ui && npx vitest run src/app/account/spaces/people`
Expected: PASS (7 tests).
Run: `cd ui && npx vitest run && npx tsc --noEmit && npm run lint`
Expected: everything is green (122 tests in the full suite).
Optional: `cd ui && npx next build` should list `/account/spaces`, `/account/spaces/new` and `/account/spaces/people` as static (`○`) routes.

- [ ] **Step 5: Commit**
```bash
git add ui/src/app/account/spaces/people/
git commit -m "feat(ui): the people page of a space, with the way back to the product"
```


---

### Task 10: Record the deviations and verify the whole branch

**Files:**
- Modify: `docs/specs/2026-10-09-personal-workspaces.md`

**Interfaces:**
- Consumes: everything above.
- Produces: a spec that says what was built; ctech-billing reads the people URL from it.

- [ ] **Step 1: Amend the spec's § 3 with what the implementation settled**

Append to the end of `docs/specs/2026-10-09-personal-workspaces.md`:

```markdown

## Amendment, implementation (2026-10-09)

- **People route:** `/account/spaces/people?id={id}[&client_id&return_to&state]`, not
  `/account/spaces/{id}/people` — the UI is a static export and has no dynamic segments, like
  `/account/organizations/detail?id=`. Products build this URL.
- **Handoff validation:** spaces reuse `GET /v1.0/organizations/handoff`; there is no
  `/v1.0/spaces/handoff`. The check does not depend on the kind.
- **List:** `GET /v1.0/organizations` returns organizations only; `?kind=personal` returns spaces, same
  envelope.
- **Transfer:** the former owner of a space becomes `member` (*Acesso total*), not `admin`.
- **Leaving:** a non-owner may still leave a space; only management is owner-only.
- **Invite page:** `/invite` keeps its organization wording, since the kind is not known before
  accepting; after accepting, the organization page forwards a space to its people page.
```

- [ ] **Step 2: Run everything the way CI does**

Run: `cd api && go vet ./... && go test ./... && cd ../ui && npx vitest run && npx tsc --noEmit && npm run lint`
Expected: every Go package `ok`; vitest all green (122 tests at the time of planning); no type or lint errors.

- [ ] **Step 3: Commit**

```bash
git add docs/specs/2026-10-09-personal-workspaces.md
git commit -m "docs: personal workspaces — record the implementation's route and transfer decisions"
```

## Deploy order (outside this plan)

This repository first. Then ctech-dfe's kind check (`docs/specs/2026-10-09-personal-workspaces-in-dfe.md` there), then ctech-billing 6.8 — which must build `/account/spaces/new` and `/account/spaces/people?id=` links and read `kind` from the two internal routes.

