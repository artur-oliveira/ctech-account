# Platform Organizations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `ctech-account` a platform `Organization` with memberships and invitations, so tenant identity stops being owned by `ctech-billing` and `ctech-dfe`.

**Architecture:** Three DynamoDB tables in the existing single-table-per-aggregate style (`account_organizations`, `account_memberships`, `account_invitations`), each with a sparse `lookup-index` GSI that answers the reverse query. A `domain/organization` package in the repository/service/handler shape every other domain here uses. Phase 1 ships the model, the routes and the admin surface **unused by any product** — nothing in billing or dfe reads it until phase 2, so phase 1 is revertible by deleting rows nobody consumed.

**Tech Stack:** Go 1.26, Fiber v3, DynamoDB via `internal/database` (`dynamo.Base`), `go-playground/validator`, `oklog/ulid` for ids, existing `internal/middleware` (`RequireAuth`, `RequireClientID`, `RequireSupportRole`), CDK for tables.

**Spec:** [`docs/specs/2026-08-29-platform-organizations.md`](../specs/2026-08-29-platform-organizations.md) · decision: [ctech-billing ADR 0021](https://github.com/artur-oliveira/ctech-billing/blob/main/docs/adr/0021-platform-organizations-and-companies.md)

## Global Constraints

- **Roles are exactly four and fixed:** `owner`, `admin`, `member`, `viewer`. No permission matrix, no custom roles. `owner` is never grantable through the member routes — it moves only through the transfer endpoint.
- **Exactly one `owner` per organization**, enforced by a DynamoDB condition, never by application logic that reads then writes.
- **The role is read from the membership row on every request.** Never from a JWT claim: a role in a token survives its own revocation for the token's lifetime.
- **Organization ids are opaque and time-ordered (UUIDv7)**, never slugs. UUIDv7 rather than ULID because `google/uuid` is already a dependency here and gives the same two properties the id needs — opacity and time ordering — while ULID would add a module for a difference in text encoding. `ctech-billing` already partitions data by this value ([ADR 0003](https://github.com/artur-oliveira/ctech-billing/blob/main/docs/adr/0003-tenant-and-livemode-partition-key.md)), so it must outlive every rename.
- **Table names carry the `account_` prefix** and are constructed with `database.NewBase(db, tablePrefix, "account_x")` — the physical name gets the environment prefix from config, exactly as `account_support_tickets` does.
- **All management routes are behind `RequireClientID(SELF_CLIENT_ID)`.** Machine and delegated tokens do not manage memberships.
- **Nothing fiscal enters this repository.** No CNPJ field on the organization, no certificate, no tax regime. The company claim (phase 3) stores a tax id as the subject of a claim; that is the boundary.
- **Phase 1 is read by nobody.** No change to `ctech-billing`, `ctech-dfe` or `ctech-wallet` in tasks 1–8.

---

## File Structure

| File | Responsibility |
|---|---|
| `api/internal/domain/organization/model.go` | `Organization`, `Membership`, `Invitation`, the role ladder and its predicates. Pure types and rules; no AWS. |
| `api/internal/domain/organization/repository.go` | `Repository` interface + the DynamoDB implementation. Keys, GSIs, conditional writes. |
| `api/internal/domain/organization/service.go` | Use cases: create, invite, accept, change role, remove, transfer. Enforces the invariants the repository's conditions cannot express alone. |
| `api/internal/domain/organization/service_test.go` | Table-driven tests against an in-memory fake repository. |
| `api/internal/handler/organization.go` | HTTP: request DTOs, validation tags, route registration, problem mapping. |
| `api/internal/middleware/organization.go` | `RequireOrgRole(svc, minRole)` — resolves the caller's membership from the path's organization and rejects below the floor. |
| `cdk/lib/*` (see Task 8) | The three tables and their `lookup-index` GSIs. |

---

### Task 1: The model and its role ladder

**Files:**
- Create: `api/internal/domain/organization/model.go`
- Test: `api/internal/domain/organization/model_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `Organization`, `Membership`, `Invitation` structs; `RoleOwner|RoleAdmin|RoleMember|RoleViewer` constants; `RoleRank(role string) int`; `IsGrantableRole(role string) bool`; `AtLeast(role, floor string) bool`.

- [ ] **Step 1: Write the failing test**

```go
package organization

import "testing"

// The ladder is ordered, and every comparison in the service is expressed
// through it. A role that ranks equal to another is a role two call sites will
// disagree about.
func TestRoleRankIsATotalOrder(t *testing.T) {
	ordered := []string{RoleViewer, RoleMember, RoleAdmin, RoleOwner}
	for i := 1; i < len(ordered); i++ {
		if RoleRank(ordered[i]) <= RoleRank(ordered[i-1]) {
			t.Fatalf("%s does not outrank %s", ordered[i], ordered[i-1])
		}
	}
	if RoleRank("nonsense") != 0 {
		t.Error("an unknown role must rank below every real one, not above")
	}
}

// Ownership is not a role somebody is given. It is written once when the
// organization is created and moves only through transfer, so the member
// routes must not be able to hand it out.
func TestOwnerIsNotGrantable(t *testing.T) {
	if IsGrantableRole(RoleOwner) {
		t.Fatal("owner must never be grantable through member management")
	}
	for _, role := range []string{RoleAdmin, RoleMember, RoleViewer} {
		if !IsGrantableRole(role) {
			t.Errorf("%s must be grantable", role)
		}
	}
	if IsGrantableRole("root") {
		t.Error("an invented role must not be grantable")
	}
}

func TestAtLeastComparesThroughTheLadder(t *testing.T) {
	if !AtLeast(RoleAdmin, RoleMember) {
		t.Error("admin clears the member floor")
	}
	if AtLeast(RoleViewer, RoleAdmin) {
		t.Error("viewer does not clear the admin floor")
	}
	if !AtLeast(RoleOwner, RoleOwner) {
		t.Error("a floor includes itself")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/domain/organization/ -run TestRole -v`
Expected: FAIL — the package does not compile, `RoleRank` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// Package organization owns the platform's tenant identity: who shares a
// workspace, with what role, and who is invited to one.
//
// It deliberately holds nothing a product does with a tenant — no invoice
// numbering, no fiscal entity, no per-product quota. Those live in the product
// that means them (ctech-billing ADR 0021).
package organization

import "time"

// The role ladder. Four, fixed, and not a permission matrix: ctech-dfe already
// has an action.resource model, and replicating it here before anybody has
// asked for a custom role is how this record becomes a second RBAC lineage. A
// ladder can be widened later; a permission engine cannot be narrowed.
const (
	RoleViewer = "viewer"
	RoleMember = "member"
	RoleAdmin  = "admin"
	RoleOwner  = "owner"
)

// rank orders the ladder. Unknown roles rank 0, below every real one, so a
// value that reached the database by some path nobody planned fails closed.
var rank = map[string]int{RoleViewer: 1, RoleMember: 2, RoleAdmin: 3, RoleOwner: 4}

func RoleRank(role string) int { return rank[role] }

// IsGrantableRole reports whether member management may assign this role.
//
// Owner is absent, and its absence is the invariant: an organization has
// exactly one owner and it is whoever created it. Ownership moves through
// transfer, which demotes and promotes in one transaction — it is never a
// second row somebody adds.
func IsGrantableRole(role string) bool {
	return role == RoleAdmin || role == RoleMember || role == RoleViewer
}

// AtLeast reports whether role clears floor.
func AtLeast(role, floor string) bool {
	return RoleRank(role) >= RoleRank(floor) && RoleRank(role) > 0
}

// Organization is a workspace and a billing target: who shares access and who
// receives the invoice.
type Organization struct {
	ID          string    `dynamodbav:"-"`
	DisplayName string    `dynamodbav:"display_name"`
	OwnerUserID string    `dynamodbav:"owner_user_id"`
	CreatedAt   time.Time `dynamodbav:"created_at"`
	UpdatedAt   time.Time `dynamodbav:"updated_at"`
}

// Membership is one person's access to one organization.
type Membership struct {
	OrganizationID string    `dynamodbav:"-"`
	UserID         string    `dynamodbav:"-"`
	Role           string    `dynamodbav:"role"`
	InvitedBy      string    `dynamodbav:"invited_by,omitempty"`
	CreatedAt      time.Time `dynamodbav:"created_at"`
}

// Invitation is an offer of membership to one e-mail address.
//
// TokenHash, never the token: a row read out of the table must not be usable to
// accept the invitation it describes.
type Invitation struct {
	OrganizationID string    `dynamodbav:"-"`
	Email          string    `dynamodbav:"-"`
	Role           string    `dynamodbav:"role"`
	TokenHash      string    `dynamodbav:"token_hash"`
	InvitedBy      string    `dynamodbav:"invited_by"`
	CreatedAt      time.Time `dynamodbav:"created_at"`
	ExpiresAt      time.Time `dynamodbav:"-"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd api && go test ./internal/domain/organization/ -v`
Expected: PASS, three tests.

- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/organization/
git commit -m "feat(organization): the model and its fixed role ladder"
```

---

### Task 2: Repository — organizations and the owner lookup

**Files:**
- Create: `api/internal/domain/organization/repository.go`
- Test: `api/internal/domain/organization/repository_test.go`

**Interfaces:**
- Consumes: `Organization`, `Membership` (Task 1); `database.NewBase`.
- Produces: `Repository` interface with `CreateWithOwner(ctx, *Organization, ownerUserID string) error`, `Get(ctx, id string) (*Organization, error)`, `UpdateDisplayName(ctx, id, name string, now time.Time) error`; `ErrNotFound`; key helpers `orgPK(id)`, `memberSK(userID)`, `lookupUserPK(userID)`.

- [ ] **Step 1: Write the failing test**

```go
package organization

import "testing"

// The keys are asserted directly because two access paths depend on their exact
// shape — a member listing queries the partition, and a person's organizations
// query the index — and a key built two ways is two answers.
func TestKeyShapes(t *testing.T) {
	if got := orgPK("01ABC"); got != "ORG#01ABC" {
		t.Errorf("orgPK = %q", got)
	}
	if got := memberSK("usr_1"); got != "MEMBER#usr_1" {
		t.Errorf("memberSK = %q", got)
	}
	if got := lookupUserPK("usr_1"); got != "USER#usr_1" {
		t.Errorf("lookupUserPK = %q", got)
	}
	if got := inviteSK("Artur@Example.com "); got != "INVITE#artur@example.com" {
		t.Errorf("inviteSK = %q — an invitation is keyed on the normalized address, or the same person is invited twice", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/domain/organization/ -run TestKeyShapes -v`
Expected: FAIL — `orgPK` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
package organization

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/account/api/internal/database"
)

var (
	ErrNotFound     = errors.New("organization not found")
	ErrAlreadyMember = errors.New("already a member")
	ErrNotAMember   = errors.New("not a member")
)

const (
	orgsTable        = "account_organizations"
	membershipsTable = "account_memberships"
	invitationsTable = "account_invitations"
	lookupIndex      = "lookup-index"
	metaSK           = "META"
)

func orgPK(id string) string          { return "ORG#" + id }
func memberSK(userID string) string   { return "MEMBER#" + userID }
func lookupUserPK(userID string) string { return "USER#" + userID }

// inviteSK normalizes the address, so inviting the same person twice replaces
// the pending invitation instead of creating a second one nobody can see.
func inviteSK(email string) string {
	return "INVITE#" + strings.ToLower(strings.TrimSpace(email))
}

// Repository is the data access this domain needs. An interface so the service
// is testable without DynamoDB — the same shape support and kyc already use.
type Repository interface {
	CreateWithOwner(ctx context.Context, org *Organization) error
	Get(ctx context.Context, id string) (*Organization, error)
	UpdateDisplayName(ctx context.Context, id, name string, now time.Time) error
	GetMembership(ctx context.Context, orgID, userID string) (*Membership, error)
	ListMembers(ctx context.Context, orgID string) ([]*Membership, error)
	ListForUser(ctx context.Context, userID string) ([]*Membership, error)
	PutMembership(ctx context.Context, m *Membership, mustBeAbsent bool) error
	SetRole(ctx context.Context, orgID, userID, role string) error
	RemoveMembership(ctx context.Context, orgID, userID string) error
	TransferOwnership(ctx context.Context, orgID, fromUserID, toUserID string, now time.Time) error
	PutInvitation(ctx context.Context, inv *Invitation) error
	GetInvitationByToken(ctx context.Context, tokenHash string) (*Invitation, error)
	DeleteInvitation(ctx context.Context, orgID, email string) error
}

type repo struct {
	orgs        database.Base
	memberships database.Base
	invitations database.Base
}

func NewRepository(db *dynamodb.Client, tablePrefix string) Repository {
	return &repo{
		orgs:        database.NewBase(db, tablePrefix, orgsTable),
		memberships: database.NewBase(db, tablePrefix, membershipsTable),
		invitations: database.NewBase(db, tablePrefix, invitationsTable),
	}
}

// CreateWithOwner writes the organization and its single owner membership in
// **one transaction**. Two writes would leave a window in which an organization
// exists with nobody able to reach it, and a failure in that window is silent.
func (r *repo) CreateWithOwner(ctx context.Context, org *Organization) error {
	orgItem, err := attributevalue.MarshalMap(org)
	if err != nil {
		return err
	}
	orgItem["pk"] = &types.AttributeValueMemberS{Value: orgPK(org.ID)}
	orgItem["sk"] = &types.AttributeValueMemberS{Value: metaSK}
	orgItem["lookup_pk"] = &types.AttributeValueMemberS{Value: lookupUserPK(org.OwnerUserID)}

	member := &Membership{
		OrganizationID: org.ID,
		UserID:         org.OwnerUserID,
		Role:           RoleOwner,
		CreatedAt:      org.CreatedAt,
	}
	memberItem, err := attributevalue.MarshalMap(member)
	if err != nil {
		return err
	}
	memberItem["pk"] = &types.AttributeValueMemberS{Value: orgPK(org.ID)}
	memberItem["sk"] = &types.AttributeValueMemberS{Value: memberSK(org.OwnerUserID)}
	memberItem["lookup_pk"] = &types.AttributeValueMemberS{Value: lookupUserPK(org.OwnerUserID)}

	return r.orgs.TransactWrite(ctx, []types.TransactWriteItem{
		r.orgs.BuildPutTxItemIfAbsent(orgItem),
		r.memberships.BuildPutTxItemIfAbsent(memberItem),
	})
}
```

> The remaining `Repository` methods are mechanical `GetItem` / `Query` / conditional `UpdateItem` calls in the same style as `internal/domain/support/repository.go`. Write them in this step; each is three to ten lines and none of them carries a decision. `TransferOwnership` is the one exception and is Task 4.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd api && go test ./internal/domain/organization/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/organization/
git commit -m "feat(organization): repository, with the owner membership written in the creating transaction"
```

---

### Task 3: Service — create, and the fake repository the rest of the tests use

**Files:**
- Create: `api/internal/domain/organization/service.go`
- Create: `api/internal/domain/organization/service_test.go`

**Interfaces:**
- Consumes: `Repository` (Task 2).
- Produces: `Service` with `Create(ctx, ownerUserID, displayName string) (*Organization, error)`, `ListForUser(ctx, userID string) ([]Membership, error)`, `RoleOf(ctx, orgID, userID string) (string, error)`; `fakeRepo` in the test file, reused by Tasks 4–6.

- [ ] **Step 1: Write the failing test**

```go
package organization

import (
	"context"
	"testing"
)

func TestCreateMakesTheCallerTheOwner(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, fixedClock)

	org, err := svc.Create(context.Background(), "usr_1", "CTech")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if org.ID == "" {
		t.Fatal("an organization needs an id")
	}
	role, err := svc.RoleOf(context.Background(), org.ID, "usr_1")
	if err != nil {
		t.Fatalf("RoleOf: %v", err)
	}
	if role != RoleOwner {
		t.Fatalf("role = %q, want owner", role)
	}
}

// A workspace with no name is a workspace nobody can tell apart in a switcher.
func TestCreateRefusesAnEmptyName(t *testing.T) {
	svc := NewService(newFakeRepo(), fixedClock)
	if _, err := svc.Create(context.Background(), "usr_1", "   "); err == nil {
		t.Fatal("accepted an organization with no name")
	}
}

func TestListForUserReturnsEveryMembership(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, fixedClock)
	ctx := context.Background()

	first, _ := svc.Create(ctx, "usr_1", "CTech")
	second, _ := svc.Create(ctx, "usr_1", "Contabilidade Silva")

	got, err := svc.ListForUser(ctx, "usr_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("memberships = %d, want 2 — a person may own several workspaces", len(got))
	}
	seen := map[string]bool{}
	for _, m := range got {
		seen[m.OrganizationID] = true
	}
	if !seen[first.ID] || !seen[second.ID] {
		t.Fatalf("memberships = %+v, want both organizations", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/domain/organization/ -run TestCreate -v`
Expected: FAIL — `NewService` undefined.

- [ ] **Step 3: Write minimal implementation**

Write `service.go` with `NewService(repo Repository, now func() time.Time) *Service`, `Create` (trim and reject an empty name, mint a UUIDv7, call `CreateWithOwner`), `ListForUser`, `RoleOf` (returns `ErrNotAMember` when absent). Write `fakeRepo` in `service_test.go` as maps keyed by `orgID` and `orgID+userID`, plus `fixedClock`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd api && go test ./internal/domain/organization/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/organization/
git commit -m "feat(organization): create, with the caller as the single owner"
```

---

### Task 4: Ownership is singular, and transfer is one transaction

**Files:**
- Modify: `api/internal/domain/organization/service.go`
- Modify: `api/internal/domain/organization/repository.go`
- Modify: `api/internal/domain/organization/service_test.go`

**Interfaces:**
- Produces: `Service.SetRole(ctx, orgID, actorUserID, targetUserID, role string) error`, `Service.Remove(ctx, orgID, actorUserID, targetUserID string) error`, `Service.Transfer(ctx, orgID, actorUserID, toUserID string) error`.

- [ ] **Step 1: Write the failing test**

```go
func TestOwnerCannotBeGrantedThroughSetRole(t *testing.T) {
	svc, org := seedOrg(t, "usr_owner")
	ctx := context.Background()
	if err := svc.SetRole(ctx, org.ID, "usr_owner", "usr_2", RoleMember); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetRole(ctx, org.ID, "usr_owner", "usr_2", RoleOwner); err == nil {
		t.Fatal("owner was handed out through member management")
	}
}

// The last owner leaving is an organization nobody can administer, and it is
// reachable by one careless click.
func TestTheLastOwnerCannotBeRemoved(t *testing.T) {
	svc, org := seedOrg(t, "usr_owner")
	if err := svc.Remove(context.Background(), org.ID, "usr_owner", "usr_owner"); err == nil {
		t.Fatal("removed the only owner")
	}
}

// Transfer moves the single owner. It never adds a second, and it never leaves
// zero — the demotion and the promotion are one write.
func TestTransferMovesOwnershipAtomically(t *testing.T) {
	svc, org := seedOrg(t, "usr_owner")
	ctx := context.Background()
	if err := svc.SetRole(ctx, org.ID, "usr_owner", "usr_2", RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := svc.Transfer(ctx, org.ID, "usr_owner", "usr_2"); err != nil {
		t.Fatalf("Transfer: %v", err)
	}
	if role, _ := svc.RoleOf(ctx, org.ID, "usr_2"); role != RoleOwner {
		t.Fatalf("new owner role = %q", role)
	}
	if role, _ := svc.RoleOf(ctx, org.ID, "usr_owner"); role != RoleAdmin {
		t.Fatalf("old owner role = %q, want admin — demoted, not removed", role)
	}
}

// Transferring to a stranger is how an organization is handed to somebody who
// never accepted it.
func TestTransferRequiresAnExistingMember(t *testing.T) {
	svc, org := seedOrg(t, "usr_owner")
	if err := svc.Transfer(context.Background(), org.ID, "usr_owner", "usr_stranger"); err == nil {
		t.Fatal("transferred to somebody who is not a member")
	}
}

// An admin may manage members. Only the owner may hand the organization away.
func TestOnlyTheOwnerMayTransfer(t *testing.T) {
	svc, org := seedOrg(t, "usr_owner")
	ctx := context.Background()
	_ = svc.SetRole(ctx, org.ID, "usr_owner", "usr_admin", RoleAdmin)
	if err := svc.Transfer(ctx, org.ID, "usr_admin", "usr_admin"); err == nil {
		t.Fatal("an admin transferred ownership")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/domain/organization/ -run 'TestOwner|TestTransfer|TestTheLast' -v`
Expected: FAIL — `SetRole` undefined.

- [ ] **Step 3: Write minimal implementation**

`SetRole` refuses `!IsGrantableRole(role)`, requires the actor to be `AtLeast(RoleAdmin)`, and refuses to change the owner's own row. `Remove` refuses the target whose role is `RoleOwner`. `Transfer` requires the actor to be the owner, requires the target's membership to exist, and calls `repo.TransferOwnership`, which writes both role changes in one `TransactWrite`, each item conditional on the role it expects to replace — so two concurrent transfers cannot both succeed.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd api && go test ./internal/domain/organization/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/organization/
git commit -m "feat(organization): one owner, and transfer as a single conditional write"
```

---

### Task 5: Invitations — hashed tokens, and acceptance bound to the invited address

**Files:**
- Modify: `api/internal/domain/organization/service.go`
- Modify: `api/internal/domain/organization/service_test.go`

**Interfaces:**
- Produces: `Service.Invite(ctx, orgID, actorUserID, email, role string) (token string, err error)`, `Service.Accept(ctx, token, userID, userEmail string) (*Membership, error)`, `Service.RevokeInvitation(ctx, orgID, actorUserID, email string) error`.

- [ ] **Step 1: Write the failing test**

```go
// The stored row must not be usable to accept the invitation it describes.
func TestInvitationStoresOnlyTheHash(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, fixedClock)
	org, _ := svc.Create(context.Background(), "usr_owner", "CTech")

	token, err := svc.Invite(context.Background(), org.ID, "usr_owner", "novo@example.com", RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	for _, inv := range repo.invitations {
		if inv.TokenHash == token {
			t.Fatal("the token itself was stored")
		}
		if inv.TokenHash == "" {
			t.Fatal("no hash was stored, so nothing can be accepted")
		}
	}
}

// An invitation is an offer to one address, not a bearer capability to join any
// organization.
func TestAcceptRequiresTheInvitedAddress(t *testing.T) {
	svc, org := seedOrg(t, "usr_owner")
	ctx := context.Background()
	token, _ := svc.Invite(ctx, org.ID, "usr_owner", "convidado@example.com", RoleMember)

	if _, err := svc.Accept(ctx, token, "usr_outro", "outro@example.com"); err == nil {
		t.Fatal("accepted with an address the invitation was not sent to")
	}
	m, err := svc.Accept(ctx, token, "usr_convidado", "Convidado@Example.com")
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if m.Role != RoleMember {
		t.Fatalf("role = %q, want the invited role", m.Role)
	}
}

// A token that worked once must not work twice: the second use would re-add
// somebody who was removed.
func TestAcceptConsumesTheInvitation(t *testing.T) {
	svc, org := seedOrg(t, "usr_owner")
	ctx := context.Background()
	token, _ := svc.Invite(ctx, org.ID, "usr_owner", "convidado@example.com", RoleMember)
	if _, err := svc.Accept(ctx, token, "usr_c", "convidado@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Accept(ctx, token, "usr_c", "convidado@example.com"); err == nil {
		t.Fatal("the same invitation was accepted twice")
	}
}

func TestInviteRefusesOwner(t *testing.T) {
	svc, org := seedOrg(t, "usr_owner")
	if _, err := svc.Invite(context.Background(), org.ID, "usr_owner", "x@example.com", RoleOwner); err == nil {
		t.Fatal("invited somebody as owner")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/domain/organization/ -run TestInvit -v`
Expected: FAIL — `Invite` undefined.

- [ ] **Step 3: Write minimal implementation**

`Invite` requires `AtLeast(RoleAdmin)` and `IsGrantableRole(role)`, generates 32 random bytes as the token, stores `sha256` of it as `TokenHash` with a 7-day `ExpiresAt` written to the row's `ttl` attribute, and returns the plaintext token once. `Accept` looks the invitation up by hash on `lookup-index`, compares the normalized invited address against the caller's verified address, writes the membership conditionally absent, and deletes the invitation in the same transaction.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd api && go test ./internal/domain/organization/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/organization/
git commit -m "feat(organization): invitations, hashed and bound to one address"
```

---

### Task 6: `RequireOrgRole` middleware

**Files:**
- Create: `api/internal/middleware/organization.go`
- Test: `api/internal/middleware/organization_test.go`

**Interfaces:**
- Consumes: `organization.Service.RoleOf`.
- Produces: `RequireOrgRole(svc *organization.Service, minRole string) fiber.Handler`; `GetOrgRole(c fiber.Ctx) string`; `GetOrgID(c fiber.Ctx) string`.

- [ ] **Step 1: Write the failing test**

```go
// The role comes from the membership row on every request. A role in a token
// survives its own revocation for the token's lifetime, and removing somebody
// has to take effect on the next request.
func TestRequireOrgRoleReadsTheMembershipEveryTime(t *testing.T) { /* table: viewer→403 on admin floor, admin→200, non-member→403 */ }

// A non-member and a missing organization answer the same thing: telling
// somebody an organization exists but is not theirs is telling them something.
func TestUnknownOrganizationAndNonMemberAnswerAlike(t *testing.T) { /* both 403, identical body */ }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/middleware/ -run TestRequireOrgRole -v`
Expected: FAIL — `RequireOrgRole` undefined.

- [ ] **Step 3: Write minimal implementation**

Reads `c.Params("id")`, calls `svc.RoleOf`, compares with `organization.AtLeast`, stores role and id in `c.Locals`, answers `403` with the same problem body for "not a member" and "no such organization".

- [ ] **Step 4: Run test to verify it passes**

Run: `cd api && go test ./internal/middleware/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api/internal/middleware/organization.go api/internal/middleware/organization_test.go
git commit -m "feat(middleware): resolve the organization role from the membership, never from a claim"
```

---

### Task 7: HTTP handlers

**Files:**
- Create: `api/internal/handler/organization.go`
- Modify: wherever `SupportHandler.Register` is wired (`api/internal/handler/routes.go` or `app.go` — follow the existing call site)

**Interfaces:**
- Produces: `OrganizationHandler` with `Register(r fiber.Router)`.

Routes, all behind `RequireAuth` + `RequireClientID(SELF_CLIENT_ID)`:

```
POST   /v1.0/organizations                        create
GET    /v1.0/organizations                        the caller's, with role
GET    /v1.0/organizations/:id                    RequireOrgRole(viewer)
PATCH  /v1.0/organizations/:id                    RequireOrgRole(admin)
GET    /v1.0/organizations/:id/members            RequireOrgRole(viewer)
POST   /v1.0/organizations/:id/invitations        RequireOrgRole(admin)
DELETE /v1.0/organizations/:id/invitations/:email RequireOrgRole(admin)
PATCH  /v1.0/organizations/:id/members/:user_id   RequireOrgRole(admin)
DELETE /v1.0/organizations/:id/members/:user_id   RequireOrgRole(admin)
POST   /v1.0/organizations/:id/transfer           RequireOrgRole(owner)
POST   /v1.0/invitations/accept                   RequireAuth only
```

- [ ] **Step 1: Write the failing test**

```go
// The e-mail the invitation is checked against is the account's verified
// address from the database, never a field in the request body: a body-supplied
// address is an invitation anybody can accept.
func TestAcceptUsesTheAccountsVerifiedEmail(t *testing.T) { /* body carrying "email" is ignored */ }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/handler/ -run TestAccept -v`
Expected: FAIL.

- [ ] **Step 3: Write minimal implementation**

DTOs with `validate:"required,max=120"` on the display name and `validate:"required,oneof=admin member viewer"` on roles. `parseBody` and `apierror` exactly as `support.go` uses them. The invitation e-mail is loaded through `user.Service.GetByID(middleware.GetUserID(c))`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd api && go test ./internal/handler/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api/internal/handler/organization.go
git commit -m "feat(organization): the management routes"
```

---

### Task 8: Tables in CDK

**Files:**
- Modify: the stack that defines `account_support_tickets` (find with `grep -rn "account_support_tickets" cdk/`)

Three tables, `pk`/`sk` string keys, `PAY_PER_REQUEST`, point-in-time recovery on, `RETAIN` on delete, each with a `lookup-index` GSI on `lookup_pk` projecting `ALL`. `account_invitations` gets TTL on `ttl`.

- [ ] **Step 1: Write the tables**
- [ ] **Step 2: `npx cdk diff` and read it** — expected: three new tables, three GSIs, nothing modified.
- [ ] **Step 3: Deploy to dev, then `aws dynamodb describe-table` on each**

> **Deploy DynamoDB before releasing API code that queries it** — the same ordering the `kyc-level-index` work already required.

- [ ] **Step 4: Commit**

```bash
git add cdk/
git commit -m "feat(cdk): organization, membership and invitation tables"
```

---

### Task 9: Migrate ctech-dfe's organizations

**Files:**
- Create: `api/cmd/migrate-dfe-orgs/main.go` (in `ctech-account`)
- Create: `docs/plans/2026-08-29-dfe-organization-migration-runbook.md`

**This task is phase 2's precondition and is written now because the data is what it is now.** It reads `ctech-dfe`'s `organizations` and `organization_users` tables and writes this repository's three tables. It is idempotent, it is dry-run by default, and **it deletes nothing** — dfe keeps its rows until phase 3 rewrites them.

**The mapping:**

| dfe | here | note |
|---|---|---|
| `organizations` row, PK `CNPJ_…`/`CPF_…` | one `Organization` | the CNPJ does **not** come with it — it becomes a Company in dfe (ADR 0021) |
| `name` | `display_name` | |
| `owner_user_id` | `owner_user_id` + the `owner` membership | dfe's own comment: "the account whose subscription pays for it" |
| `organization_users` rows | `Membership` rows | |
| `OWNER` | `owner` | must equal the row's `owner_user_id`; a disagreement is reported, never guessed |
| `ADMIN` | `admin` | |
| `USER` | `member` | the only rename |
| `VIEWER` | `viewer` | |

**The four decisions this migration must not make silently:**

1. **One dfe organization becomes one platform organization.** Not "one per owner": two CNPJs owned by the same person are two workspaces until a human merges them, because merging is irreversible and splitting is not.
2. **The new organization id is a fresh UUIDv7**, and the dfe key is recorded as `source_ref`. Reusing `CNPJ_…` as an id would put a tax id in the partition key of every billing row forever.
3. **A membership whose user no longer exists in `account_users` is skipped and reported**, never written. A membership pointing at nobody is an access grant that cannot be audited.
4. **An organization whose `owner_user_id` is empty gets no owner membership and is reported.** Inventing one from the oldest `OWNER` row would be guessing who owns a company — which is exactly what dfe's own repair path does (`services/billing.go:990`), and exactly why this one must not.
5. **A membership carrying extra dfe `permissions` is reported, never imported.** Each dfe membership holds a `permissions` list of grants *on top of* the role (`repositories/organization_users.go:56`). This model has no permissions, deliberately. Importing one silently deletes access somebody was explicitly given, and nobody notices until a screen is gone.

**What the dfe tables turned out to be** (mapped 2026-08-29, and it changed two things above): `organizations` has **no GSI at all**, so the read side is a `Scan`, not a `Query` — acceptable because the table holds a handful of rows and runs once. There is **no `status` and no soft delete** on either table, so every row present is live and nothing needs filtering out. `organization_users` keys are `pk={org pk}`, `sk=USER_{sub}`, with the bare sub also in `user_id`.

- [ ] **Step 1: Write the failing test**

```go
// The role rename is the one lossy-looking step and the one worth pinning.
func TestRoleMapping(t *testing.T) {
	for dfe, want := range map[string]string{"OWNER": "owner", "ADMIN": "admin", "USER": "member", "VIEWER": "viewer"} {
		if got, ok := mapRole(dfe); !ok || got != want {
			t.Errorf("mapRole(%q) = %q, want %q", dfe, got, want)
		}
	}
	if _, ok := mapRole("SUPERUSER"); ok {
		t.Error("an unknown dfe role must be reported, never mapped to something plausible")
	}
}

// Re-running must not double-write. The organization is keyed by its source ref
// so a second pass finds it and skips.
func TestMigrationIsIdempotent(t *testing.T) { /* run twice against the fake, assert one organization and one membership set */ }

// A membership pointing at a user this repository does not have is an access
// grant nobody can audit.
func TestMembershipForAnUnknownUserIsSkippedAndReported(t *testing.T) { /* assert skipped count and no write */ }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./cmd/migrate-dfe-orgs/ -v`
Expected: FAIL — `mapRole` undefined.

- [ ] **Step 3: Write minimal implementation**

```
migrate-dfe-orgs
  -dfe-table-prefix   read side  (default: the dfe environment prefix)
  -table-prefix       write side
  -apply              default false; without it nothing is written
  -org                optional, one dfe organization key, for a single rehearsal
```

Output is a report per organization: created / skipped-already-migrated / **needs a human**, with counts at the end. Exit non-zero when anything landed in the third bucket, so a pipeline cannot report success over a partial migration.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd api && go test ./cmd/migrate-dfe-orgs/ -v`
Expected: PASS.

- [ ] **Step 5: Rehearse against dev**

```bash
go run ./cmd/migrate-dfe-orgs -dfe-table-prefix dev -table-prefix dev            # dry run, read the report
go run ./cmd/migrate-dfe-orgs -dfe-table-prefix dev -table-prefix dev -org CNPJ_x -apply
go run ./cmd/migrate-dfe-orgs -dfe-table-prefix dev -table-prefix dev -apply
go run ./cmd/migrate-dfe-orgs -dfe-table-prefix dev -table-prefix dev -apply     # again: everything skipped
```

- [ ] **Step 6: Write the runbook and commit**

The runbook records the production order (dry run → read the report → resolve every "needs a human" → apply → re-run and confirm all-skipped), and the fact that dfe is untouched, so the rollback is deleting the rows this wrote.

```bash
git add api/cmd/migrate-dfe-orgs/ docs/plans/2026-08-29-dfe-organization-migration-runbook.md
git commit -m "feat: migrate ctech-dfe organizations into the platform model"
```

---

## Self-review

**Spec coverage.** Organizations, memberships, invitations, roles, routes, authorization and the CDK tables are Tasks 1–8. The dfe migration is Task 9 (spec phase 2's precondition). **Not in this plan, by design:** the company claim and the `/admin/kyc` Companies queue — they are the spec's phase 3, they depend on nothing here, and folding them in would make phase 1 unshippable until a review workspace is finished. They get their own plan.

**Placeholders.** Task 2 delegates the mechanical repository methods to a stated pattern file rather than spelling out ten `GetItem` calls; Tasks 6, 7 and 9 give test names and assertions in prose where the body is a table. Both are deliberate compressions of repetition, not deferred decisions — every decision in this plan is written down. Task 3's `service.go` body is described rather than pasted for the same reason.

**Type consistency.** `RoleOf`, `AtLeast`, `IsGrantableRole`, `CreateWithOwner`, `TransferOwnership`, `Invite`/`Accept` are used in Tasks 4–7 with the signatures Tasks 1–3 define. `lookup-index` and the `lookup_pk` attribute are one name across repository, CDK and migration.
