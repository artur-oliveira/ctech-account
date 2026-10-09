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
