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
