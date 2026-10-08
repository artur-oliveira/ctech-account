package handler_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"gopkg.aoctech.app/account/api/internal/handler"
	"gopkg.aoctech.app/account/api/internal/middleware"
	"gopkg.aoctech.app/account/api/internal/scopes"
)

// newInternalMembershipApp mounts both internal route groups the way main.go
// does, each with its own scope guard, so the tests exercise the gates and the
// separation between them, not just the handlers.
func newInternalMembershipApp(t *testing.T) *companyTestApp {
	t.Helper()
	a := newInternalReachApp(t)
	handler.NewOrganizationHandler(a.orgSvc, a.testApp.userSvc).
		RegisterInternal(a.app.Group("/v1.0"),
			middleware.RequireAuth(a.testApp.jwtSvc),
			middleware.RequireInternalScope(scopes.InternalAccountOrgMember))
	return a
}

func TestTheMembershipCheckNeedsItsOwnInternalScope(t *testing.T) {
	a := newInternalMembershipApp(t)
	orgID, ownerID, _ := a.seedOrg(t, "member-gate@example.com")
	path := "/v1.0/internal/organizations/" + orgID + "/members/" + ownerID

	user := a.registerUser(t, "member-gate-user@example.com", "Sup3rSecret!pass", "Sem escopo")
	if resp := a.do(t, http.MethodGet, path, a.issueToken(t, user.ID()), ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a user token reached the membership route: status %d", resp.StatusCode)
	}
	// Holding the company-reach scope is not holding this one: two products'
	// worth of questions, two grants.
	reach := a.issueServiceToken(t, []string{scopes.InternalAccountCompanyActor})
	if resp := a.do(t, http.MethodGet, path, reach, ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("the company-actor scope answered a membership question: status %d", resp.StatusCode)
	}
}

// ...and the reverse: the company routes must not start answering to the new
// scope just because they share a path prefix.
func TestTheCompanyRoutesStillNeedTheCompanyScope(t *testing.T) {
	a := newInternalMembershipApp(t)
	orgID, ownerID, _ := a.seedOrg(t, "member-sep@example.com")
	member := a.issueServiceToken(t, []string{scopes.InternalAccountOrgMember})
	for _, path := range []string{
		"/v1.0/internal/companies/cmp_1/actors/" + ownerID,
		"/v1.0/internal/organizations/" + orgID + "/companies/cmp_1",
	} {
		if resp := a.do(t, http.MethodGet, path, member, ""); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s answered the membership scope: status %d", path, resp.StatusCode)
		}
	}
}

func TestTheMembershipAnswerCarriesTheRole(t *testing.T) {
	a := newInternalMembershipApp(t)
	orgID, ownerID, _ := a.seedOrg(t, "member-ok@example.com")
	internal := a.issueServiceToken(t, []string{scopes.InternalAccountOrgMember})

	resp := a.do(t, http.MethodGet, "/v1.0/internal/organizations/"+orgID+"/members/"+ownerID, internal, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["member"] != true || body["role"] != "owner" {
		t.Fatalf("got %+v, want member true, role owner", body)
	}
}

// A stranger, a real organization the person is not in and an organization that
// does not exist must answer alike — 200 {"member":false} — or this route is a
// probe for which organization ids are real. A 404 would also invite the caller
// to read a refusal and an outage the same way.
func TestANonMemberAndAnUnknownOrganizationAnswerAlike(t *testing.T) {
	a := newInternalMembershipApp(t)
	orgID, _, _ := a.seedOrg(t, "member-probe@example.com")
	stranger := a.registerUser(t, "member-stranger@example.com", "Sup3rSecret!pass", "Estranho")
	internal := a.issueServiceToken(t, []string{scopes.InternalAccountOrgMember})

	refused := a.do(t, http.MethodGet, "/v1.0/internal/organizations/"+orgID+"/members/"+stranger.ID(), internal, "")
	unknown := a.do(t, http.MethodGet, "/v1.0/internal/organizations/0190a1b2-c3d4-7e5f-8a9b-ffffffffffff/members/"+stranger.ID(), internal, "")
	if refused.StatusCode != http.StatusOK || unknown.StatusCode != http.StatusOK {
		t.Fatalf("statuses %d / %d, want 200 / 200", refused.StatusCode, unknown.StatusCode)
	}
	rb, ub := bodyString(refused), bodyString(unknown)
	if rb != ub || rb != `{"member":false}` {
		t.Fatalf("distinguishable or carries more than the refusal:\n  refused: %s\n  unknown: %s", rb, ub)
	}
}

func TestAServiceCanListAUsersOrganizationsWithRoles(t *testing.T) {
	a := newInternalMembershipApp(t)
	orgID, ownerID, _ := a.seedOrg(t, "list-orgs@example.com")
	internal := a.issueServiceToken(t, []string{scopes.InternalAccountOrgMember})

	resp := a.do(t, http.MethodGet, "/v1.0/internal/users/"+ownerID+"/organizations", internal, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Organizations []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			Role        string `json:"role"`
		} `json:"organizations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Organizations) != 1 || body.Organizations[0].ID != orgID || body.Organizations[0].Role != "owner" {
		t.Fatalf("got %+v, want the one organization with role owner", body.Organizations)
	}
}

func TestListingOrganizationsNeedsTheMembershipScope(t *testing.T) {
	a := newInternalMembershipApp(t)
	_, ownerID, _ := a.seedOrg(t, "list-gate@example.com")
	path := "/v1.0/internal/users/" + ownerID + "/organizations"
	user := a.registerUser(t, "list-gate-user@example.com", "Sup3rSecret!pass", "Sem escopo")
	if resp := a.do(t, http.MethodGet, path, a.issueToken(t, user.ID()), ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a user token listed organizations: %d", resp.StatusCode)
	}
	reach := a.issueServiceToken(t, []string{scopes.InternalAccountCompanyActor})
	if resp := a.do(t, http.MethodGet, path, reach, ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("the company-actor scope listed organizations: %d", resp.StatusCode)
	}
}

func TestAnUnknownUserHasNoOrganizationsAndIsNotA404(t *testing.T) {
	a := newInternalMembershipApp(t)
	internal := a.issueServiceToken(t, []string{scopes.InternalAccountOrgMember})
	resp := a.do(t, http.MethodGet, "/v1.0/internal/users/usr_nobody/organizations", internal, "")
	if resp.StatusCode != http.StatusOK || bodyString(resp) != `{"organizations":[]}` {
		t.Fatalf("status %d body %s, want 200 {\"organizations\":[]}", resp.StatusCode, bodyString(resp))
	}
}
