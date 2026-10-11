package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	companyDomain "gopkg.aoctech.app/account/api/internal/domain/company"
	"gopkg.aoctech.app/account/api/internal/handler"
	"gopkg.aoctech.app/account/api/internal/scopes"
)

type internalCompanyList struct {
	OrganizationID   string           `json:"organization_id"`
	OrganizationKind string           `json:"organization_kind"`
	Companies        []map[string]any `json:"companies"`
	Truncated        *bool            `json:"truncated"`
}

func listCompaniesPath(orgID string) string {
	return "/v1.0/internal/organizations/" + orgID + "/companies"
}

// The list is gated like the identity read: a user token, even the owner's, is
// not a service and does not get the organization's tax ids.
func TestTheInternalCompanyListNeedsTheInternalScope(t *testing.T) {
	a := newInternalReachApp(t)
	orgID, ownerID, _ := a.seedOrg(t, "list-nogo@example.com")
	resp := a.do(t, http.MethodGet, listCompaniesPath(orgID), a.issueToken(t, ownerID), "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

// The picker's answer: each company's id and identity, ordered by the name
// people read, only this organization's, and nothing that is the product's
// (role) or the platform's own bookkeeping (where an import came from).
func TestTheInternalCompanyListCarriesTheIdentities(t *testing.T) {
	a := newInternalReachApp(t)
	orgID, ownerID, _ := a.seedOrg(t, "list-ok@example.com")
	otherOrg, otherOwner, _ := a.seedOrg(t, "list-other@example.com")
	internal := a.issueServiceToken(t, []string{scopes.InternalAccountCompanyActor})
	svc := companyDomain.NewService(a.repo, time.Now)
	zeta, err := svc.Register(context.Background(), orgID, ownerID, "Dono", "11222333000181", "Zeta LTDA", "Zeta")
	if err != nil {
		t.Fatal(err)
	}
	acme, err := svc.Register(context.Background(), orgID, ownerID, "Dono", "12ABC34501DE35", "Acme LTDA", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Register(context.Background(), otherOrg, otherOwner, "Dono", "11222333000181", "Alheia LTDA", ""); err != nil {
		t.Fatal(err)
	}

	resp := a.do(t, http.MethodGet, listCompaniesPath(orgID), internal, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, bodyString(resp))
	}
	var body internalCompanyList
	decodeJSON(t, resp, &body)
	if body.OrganizationID != orgID || body.OrganizationKind != "organization" {
		t.Fatalf("organization = %q/%q", body.OrganizationID, body.OrganizationKind)
	}
	if body.Truncated == nil || *body.Truncated {
		t.Fatalf("truncated = %v, want false", body.Truncated)
	}
	if len(body.Companies) != 2 {
		t.Fatalf("companies = %+v, want this organization's two", body.Companies)
	}
	first, second := body.Companies[0], body.Companies[1]
	if first["company_id"] != acme.ID || second["company_id"] != zeta.ID {
		t.Fatalf("order = %v, %v; want Acme then Zeta", first["company_id"], second["company_id"])
	}
	want := map[string]any{
		"company_id": zeta.ID, "tax_id": "11222333000181", "tax_id_kind": "cnpj",
		"legal_name": "Zeta LTDA", "trade_name": "Zeta",
	}
	for k, v := range want {
		if second[k] != v {
			t.Errorf("%s = %v, want %v", k, second[k], v)
		}
	}
	if first["trade_name"] != "" {
		t.Errorf("an absent trade name is %v, want the empty string the identity read gives", first["trade_name"])
	}
	for _, item := range body.Companies {
		if len(item) != len(want) {
			t.Errorf("item carries %v, want exactly %d identity fields", item, len(want))
		}
	}
}

// An organization with no companies is an answer, not an error: the picker says
// "register one first". The list is [] and never null.
func TestAnOrganizationWithoutCompaniesListsNone(t *testing.T) {
	a := newInternalReachApp(t)
	orgID, _, _ := a.seedOrg(t, "list-empty@example.com")
	internal := a.issueServiceToken(t, []string{scopes.InternalAccountCompanyActor})
	resp := a.do(t, http.MethodGet, listCompaniesPath(orgID), internal, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	raw := bodyString(resp)
	if !strings.Contains(raw, `"companies":[]`) {
		t.Fatalf("body = %s, want an empty companies array", raw)
	}
}

// An organization that does not exist is 404, as the identity read answers a
// pair that does not resolve: the caller was handed this id, so "there is no
// such organization" is the useful answer and discloses nothing new.
func TestAnUnknownOrganizationIsNotFoundOnTheList(t *testing.T) {
	a := newInternalReachApp(t)
	internal := a.issueServiceToken(t, []string{scopes.InternalAccountCompanyActor})
	resp := a.do(t, http.MethodGet, listCompaniesPath("org_nao_existe"), internal, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", resp.StatusCode, bodyString(resp))
	}
}

// The answer is bounded. Past the cap the first page by name comes back and
// truncated says there is more, so a caller never mistakes a cut list for all.
func TestTheInternalCompanyListIsCapped(t *testing.T) {
	a := newInternalReachApp(t)
	orgID, _, _ := a.seedOrg(t, "list-cap@example.com")
	internal := a.issueServiceToken(t, []string{scopes.InternalAccountCompanyActor})
	for i := range handler.InternalCompanyListLimit + 1 {
		id := fmt.Sprintf("cmp_%04d", i)
		if err := a.repo.Create(context.Background(), &companyDomain.Company{
			OrganizationID: orgID, ID: id, TaxID: id, TaxIDKind: companyDomain.KindCNPJ,
			LegalName: fmt.Sprintf("Empresa %04d LTDA", i),
		}, nil); err != nil {
			t.Fatal(err)
		}
	}
	var body internalCompanyList
	decodeJSON(t, a.do(t, http.MethodGet, listCompaniesPath(orgID), internal, ""), &body)
	if len(body.Companies) != handler.InternalCompanyListLimit {
		t.Fatalf("len = %d, want the cap %d", len(body.Companies), handler.InternalCompanyListLimit)
	}
	if body.Truncated == nil || !*body.Truncated {
		t.Fatalf("truncated = %v, want true past the cap", body.Truncated)
	}
	if body.Companies[0]["company_id"] != "cmp_0000" {
		t.Fatalf("first = %v, want the first by name", body.Companies[0]["company_id"])
	}
}
