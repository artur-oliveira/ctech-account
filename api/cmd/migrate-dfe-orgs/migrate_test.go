package main

import (
	"context"
	"strings"
	"testing"
	"time"

	orgDomain "gopkg.aoctech.app/account/api/internal/domain/organization"
)

// The role rename is the one lossy-looking step and the one worth pinning.
func TestRoleMapping(t *testing.T) {
	for dfe, want := range map[string]string{
		"OWNER": orgDomain.RoleOwner, "ADMIN": orgDomain.RoleAdmin,
		"USER": orgDomain.RoleMember, "VIEWER": orgDomain.RoleViewer,
	} {
		if got, ok := mapRole(dfe); !ok || got != want {
			t.Errorf("mapRole(%q) = %q, %v; want %q", dfe, got, ok, want)
		}
	}
	if _, ok := mapRole("SUPERUSER"); ok {
		t.Error("an unknown dfe role must be reported, never mapped to something plausible")
	}
}

// knownUsers stands in for "does this account exist here".
func knownUsers(ids ...string) func(context.Context, string) (bool, error) {
	set := map[string]bool{}
	for _, id := range ids {
		set[id] = true
	}
	return func(_ context.Context, id string) (bool, error) { return set[id], nil }
}

func org(pk, owner string) dfeOrg {
	return dfeOrg{PK: pk, Name: "Empresa", OwnerUserID: owner, CreatedAt: "2024-01-02T03:04:05Z"}
}

func TestPlanCreatesAStraightforwardOrganization(t *testing.T) {
	d, err := planOrg(context.Background(),
		org("CNPJ_11111111000191", "usr_dono"),
		[]dfeMember{
			{UserID: "usr_dono", Role: "OWNER"},
			{UserID: "usr_2", Role: "USER"},
		},
		knownUsers("usr_dono", "usr_2"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != actionCreate {
		t.Fatalf("action = %q, want create (%v)", d.Action, d.Review)
	}
	if len(d.Members) != 1 || d.Members[0].Role != orgDomain.RoleMember {
		t.Fatalf("members = %+v — the owner is written by CreateWithOwner, the rest here", d.Members)
	}
}

// An organization with no owner gets no invented one. Deriving it from the
// oldest OWNER row is guessing who owns a company.
func TestAnOrganizationWithNoOwnerNeedsAHuman(t *testing.T) {
	d, _ := planOrg(context.Background(),
		org("CNPJ_11111111000191", ""),
		[]dfeMember{{UserID: "usr_a", Role: "OWNER"}},
		knownUsers("usr_a"))
	if d.Action != actionReview {
		t.Fatalf("action = %q, want review", d.Action)
	}
}

// A membership pointing at a user this repository does not have is an access
// grant nobody can audit.
func TestMembershipForAnUnknownUserIsSkippedAndReported(t *testing.T) {
	d, _ := planOrg(context.Background(),
		org("CNPJ_11111111000191", "usr_dono"),
		[]dfeMember{
			{UserID: "usr_dono", Role: "OWNER"},
			{UserID: "usr_fantasma", Role: "ADMIN"},
		},
		knownUsers("usr_dono"))
	if d.Action != actionReview {
		t.Fatalf("action = %q, want review", d.Action)
	}
	for _, m := range d.Members {
		if m.UserID == "usr_fantasma" {
			t.Fatal("a membership was planned for a user that does not exist here")
		}
	}
	if !strings.Contains(strings.Join(d.Review, " "), "usr_fantasma") {
		t.Fatalf("the report does not name the skipped user: %v", d.Review)
	}
}

// dfe grants extra permissions per member, on top of the role. This model has
// no permissions, on purpose — so migrating one silently is deleting access
// somebody was explicitly given, and nobody notices until a screen is gone.
func TestExtraPermissionsNeedAHuman(t *testing.T) {
	d, _ := planOrg(context.Background(),
		org("CNPJ_11111111000191", "usr_dono"),
		[]dfeMember{
			{UserID: "usr_dono", Role: "OWNER"},
			{UserID: "usr_2", Role: "VIEWER", Permissions: []string{"nfe.emit"}},
		},
		knownUsers("usr_dono", "usr_2"))
	if d.Action != actionReview {
		t.Fatalf("action = %q, want review — extra permissions would be dropped in silence", d.Action)
	}
	if !strings.Contains(strings.Join(d.Review, " "), "nfe.emit") {
		t.Fatalf("the report does not name the dropped permission: %v", d.Review)
	}
}

// dfe's own repair path back-fills owner_user_id from the OWNER row. This one
// does not: a disagreement between the two is a question, not a default.
func TestOwnerRowDisagreeingWithOwnerUserIDNeedsAHuman(t *testing.T) {
	d, _ := planOrg(context.Background(),
		org("CNPJ_11111111000191", "usr_dono"),
		[]dfeMember{{UserID: "usr_outro", Role: "OWNER"}, {UserID: "usr_dono", Role: "ADMIN"}},
		knownUsers("usr_dono", "usr_outro"))
	if d.Action != actionReview {
		t.Fatalf("action = %q, want review", d.Action)
	}
}

func TestAnUnknownRoleNeedsAHuman(t *testing.T) {
	d, _ := planOrg(context.Background(),
		org("CNPJ_11111111000191", "usr_dono"),
		[]dfeMember{{UserID: "usr_dono", Role: "OWNER"}, {UserID: "usr_2", Role: "SUPERUSER"}},
		knownUsers("usr_dono", "usr_2"))
	if d.Action != actionReview {
		t.Fatalf("action = %q, want review", d.Action)
	}
}

// The dfe timestamp travels, so an imported organization does not claim to have
// been created on migration day.
func TestCreatedAtIsPreserved(t *testing.T) {
	d, _ := planOrg(context.Background(),
		org("CNPJ_11111111000191", "usr_dono"),
		[]dfeMember{{UserID: "usr_dono", Role: "OWNER"}},
		knownUsers("usr_dono"))
	want := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	if !d.CreatedAt.Equal(want) {
		t.Fatalf("created_at = %s, want %s", d.CreatedAt, want)
	}
}

// Re-running must not double-write. The organization is keyed by its source ref
// so a second pass finds it, and any membership the first pass did not finish
// is filled in rather than skipped along with it.
func TestMigrationIsIdempotent(t *testing.T) {
	repo := newFakeRepo()
	ctx := context.Background()
	d, _ := planOrg(ctx,
		org("CNPJ_11111111000191", "usr_dono"),
		[]dfeMember{{UserID: "usr_dono", Role: "OWNER"}, {UserID: "usr_2", Role: "USER"}},
		knownUsers("usr_dono", "usr_2"))

	first, err := apply(ctx, repo, d)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if !first.CreatedOrganization || first.CreatedMemberships != 1 {
		t.Fatalf("first pass = %+v, want the organization and one extra membership", first)
	}

	second, err := apply(ctx, repo, d)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if second.CreatedOrganization || second.CreatedMemberships != 0 {
		t.Fatalf("second pass = %+v, want everything already there", second)
	}
	if len(repo.orgs) != 1 {
		t.Fatalf("organizations = %d, want 1", len(repo.orgs))
	}
	if got := len(repo.memberships[first.OrganizationID]); got != 2 {
		t.Fatalf("memberships = %d, want 2", got)
	}
}

// A first pass that died between the organization and the second membership
// must be completed by the next run, not skipped as "already migrated".
func TestARerunFinishesAPartialImport(t *testing.T) {
	repo := newFakeRepo()
	ctx := context.Background()
	d, _ := planOrg(ctx,
		org("CNPJ_11111111000191", "usr_dono"),
		[]dfeMember{{UserID: "usr_dono", Role: "OWNER"}, {UserID: "usr_2", Role: "USER"}},
		knownUsers("usr_dono", "usr_2"))

	partial := d
	partial.Members = nil // the run died before the second membership
	if _, err := apply(ctx, repo, partial); err != nil {
		t.Fatal(err)
	}
	done, err := apply(ctx, repo, d)
	if err != nil {
		t.Fatal(err)
	}
	if done.CreatedOrganization {
		t.Fatal("the organization was written twice")
	}
	if done.CreatedMemberships != 1 {
		t.Fatalf("memberships written = %d, want the one the first run missed", done.CreatedMemberships)
	}
}
