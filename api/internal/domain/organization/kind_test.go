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

// Handing a space over leaves the former owner with full access, not as an
// admin — a rung a space does not have.
func TestTransferringASpaceLeavesTheFormerOwnerWithFullAccess(t *testing.T) {
	svc, space := seedSpace(t, "usr_owner")
	ctx := context.Background()
	join(t, svc, space.ID, "usr_full", RoleMember)
	if err := svc.Transfer(ctx, space.ID, "usr_owner", "usr_full"); err != nil {
		t.Fatal(err)
	}
	if role, _ := svc.RoleOf(ctx, space.ID, "usr_owner"); role != RoleMember {
		t.Fatalf("former owner = %q, want member", role)
	}
	if role, _ := svc.RoleOf(ctx, space.ID, "usr_full"); role != RoleOwner {
		t.Fatalf("new owner = %q, want owner", role)
	}
}

func TestTransferringAnOrganizationStillLeavesAnAdmin(t *testing.T) {
	svc, org := seedOrg(t, "usr_owner")
	ctx := context.Background()
	join(t, svc, org.ID, "usr_2", RoleMember)
	if err := svc.Transfer(ctx, org.ID, "usr_owner", "usr_2"); err != nil {
		t.Fatal(err)
	}
	if role, _ := svc.RoleOf(ctx, org.ID, "usr_owner"); role != RoleAdmin {
		t.Fatalf("former owner = %q, want admin", role)
	}
}
