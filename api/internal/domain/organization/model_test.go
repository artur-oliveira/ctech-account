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
