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
