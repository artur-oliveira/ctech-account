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
