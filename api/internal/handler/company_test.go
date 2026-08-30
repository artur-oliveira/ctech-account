package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"gopkg.aoctech.app/account/api/internal/apierror"
	companyDomain "gopkg.aoctech.app/account/api/internal/domain/company"
	"gopkg.aoctech.app/account/api/internal/domain/company/registry"
	orgDomain "gopkg.aoctech.app/account/api/internal/domain/organization"
	"gopkg.aoctech.app/account/api/internal/handler"
	"gopkg.aoctech.app/account/api/internal/middleware"
	"gopkg.aoctech.app/account/api/internal/scopes"
)

// memCompanyRepo is an in-memory company.Repository reproducing the one
// condition the DynamoDB implementation enforces: a tax id is claimed once per
// organization, by a conditional write. A fake that allows what the database
// refuses tests nothing that ships.
type memCompanyRepo struct {
	companies map[string]map[string]*companyDomain.Company
	taxIDs    map[string]map[string]string
	actors    map[string]map[string]*companyDomain.Actor
}

func newMemCompanyRepo() *memCompanyRepo {
	return &memCompanyRepo{
		companies: map[string]map[string]*companyDomain.Company{},
		taxIDs:    map[string]map[string]string{},
		actors:    map[string]map[string]*companyDomain.Actor{},
	}
}

func memActorKey(orgID, companyID string) string { return orgID + "|" + companyID }

func (m *memCompanyRepo) Create(ctx context.Context, c *companyDomain.Company, first *companyDomain.Actor) error {
	if m.taxIDs[c.OrganizationID] == nil {
		m.taxIDs[c.OrganizationID] = map[string]string{}
	}
	if _, taken := m.taxIDs[c.OrganizationID][c.TaxID]; taken {
		return companyDomain.ErrTaxIDTaken
	}
	m.taxIDs[c.OrganizationID][c.TaxID] = c.ID
	if m.companies[c.OrganizationID] == nil {
		m.companies[c.OrganizationID] = map[string]*companyDomain.Company{}
	}
	copied := *c
	m.companies[c.OrganizationID][c.ID] = &copied
	if first != nil {
		return m.PutActor(ctx, first)
	}
	return nil
}

func (m *memCompanyRepo) Get(_ context.Context, orgID, companyID string) (*companyDomain.Company, error) {
	c, ok := m.companies[orgID][companyID]
	if !ok {
		return nil, companyDomain.ErrNotFound
	}
	copied := *c
	return &copied, nil
}

func (m *memCompanyRepo) List(_ context.Context, orgID string) ([]*companyDomain.Company, error) {
	out := make([]*companyDomain.Company, 0, len(m.companies[orgID]))
	for _, c := range m.companies[orgID] {
		copied := *c
		out = append(out, &copied)
	}
	return out, nil
}

func (m *memCompanyRepo) GetBySourceRef(_ context.Context, system, ref string) (*companyDomain.Company, error) {
	for _, byID := range m.companies {
		for _, c := range byID {
			if c.SourceSystem == system && c.SourceRef == ref && ref != "" {
				copied := *c
				return &copied, nil
			}
		}
	}
	return nil, companyDomain.ErrNotFound
}

func (m *memCompanyRepo) UpdateNames(_ context.Context, orgID, companyID, legal, trade string, now time.Time) error {
	c, ok := m.companies[orgID][companyID]
	if !ok {
		return companyDomain.ErrNotFound
	}
	c.LegalName, c.TradeName, c.UpdatedAt = legal, trade, now
	return nil
}

func (m *memCompanyRepo) PutActor(_ context.Context, a *companyDomain.Actor) error {
	k := memActorKey(a.OrganizationID, a.CompanyID)
	if m.actors[k] == nil {
		m.actors[k] = map[string]*companyDomain.Actor{}
	}
	copied := *a
	m.actors[k][a.UserID] = &copied
	return nil
}

func (m *memCompanyRepo) GetActor(_ context.Context, orgID, companyID, userID string) (*companyDomain.Actor, error) {
	a, ok := m.actors[memActorKey(orgID, companyID)][userID]
	if !ok {
		return nil, companyDomain.ErrNotFound
	}
	copied := *a
	return &copied, nil
}

func (m *memCompanyRepo) ListActors(_ context.Context, orgID, companyID string) ([]*companyDomain.Actor, error) {
	byUser := m.actors[memActorKey(orgID, companyID)]
	out := make([]*companyDomain.Actor, 0, len(byUser))
	for _, a := range byUser {
		copied := *a
		out = append(out, &copied)
	}
	return out, nil
}

func (m *memCompanyRepo) ListForUser(_ context.Context, userID string) ([]*companyDomain.Actor, error) {
	out := make([]*companyDomain.Actor, 0)
	for _, byUser := range m.actors {
		if a, ok := byUser[userID]; ok {
			copied := *a
			out = append(out, &copied)
		}
	}
	return out, nil
}

func (m *memCompanyRepo) BuildActorTxItem(a *companyDomain.Actor) (types.TransactWriteItem, error) {
	// The fake reproduces the shape, not the keys: what the accept transaction
	// needs from this is that an edge is written, and the real key layout is
	// exercised by the repository's own tests.
	return types.TransactWriteItem{}, nil
}

func (m *memCompanyRepo) RemoveActor(_ context.Context, orgID, companyID, userID string) error {
	k := memActorKey(orgID, companyID)
	if _, ok := m.actors[k][userID]; !ok {
		return companyDomain.ErrNotFound
	}
	delete(m.actors[k], userID)
	return nil
}

// stubLookup stands in for cnpja. found=false is the outage and the miss, which
// the routes must treat identically.
type stubLookup struct {
	names registry.Names
	found bool
}

func (s stubLookup) Names(context.Context, string) (registry.Names, bool) {
	return s.names, s.found
}

type companyTestApp struct {
	*testApp
	app     *fiber.App
	orgRepo *memOrgRepo
	orgSvc  *orgDomain.Service
	repo    *memCompanyRepo
	lookup  *stubLookup
}

func newCompanyTestApp(t *testing.T) *companyTestApp {
	t.Helper()
	base := newTestApp(t)
	orgRepo := newMemOrgRepo()
	orgSvc := orgDomain.NewService(orgRepo, time.Now)
	repo := newMemCompanyRepo()
	svc := companyDomain.NewService(repo, time.Now)
	lookup := &stubLookup{}

	app := fiber.New(fiber.Config{
		ErrorHandler: func(c fiber.Ctx, err error) error {
			if problem, ok := errors.AsType[*apierror.Problem](err); ok {
				return problem.Send(c)
			}
			return apierror.ServerError(c.Path()).Send(c)
		},
	})
	app.Use(recover.New())
	v1 := app.Group("/v1.0")
	companyH := handler.NewCompanyHandler(svc, orgSvc, base.userSvc, lookup)
	companyH.Register(v1.Group("/organizations", middleware.RequireAuth(base.jwtSvc)))
	companyH.RegisterLookup(v1.Group("/companies", middleware.RequireAuth(base.jwtSvc)))
	return &companyTestApp{testApp: base, app: app, orgRepo: orgRepo, orgSvc: orgSvc, repo: repo, lookup: lookup}
}

func (a *companyTestApp) do(t *testing.T, method, path, token, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := a.app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp
}

// seedOrg creates an organization owned by a fresh user and returns both.
func (a *companyTestApp) seedOrg(t *testing.T, email string) (orgID, ownerID, token string) {
	t.Helper()
	owner := a.registerUser(t, email, "Sup3rSecret!pass", "Dono")
	org, err := a.orgSvc.Create(context.Background(), owner.ID(), "Dono", "CTech")
	if err != nil {
		t.Fatalf("seeding organization: %v", err)
	}
	return org.ID, owner.ID(), a.issueToken(t, owner.ID())
}

// The refusal must be the organization one, not a company-shaped 404: telling a
// stranger that a company id is real is the same disclosure the organization
// routes already refuse to make.
func TestANonMemberIsRefusedTheCompanyList(t *testing.T) {
	a := newCompanyTestApp(t)
	orgID, _, _ := a.seedOrg(t, "dono-cmp1@example.com")
	stranger := a.registerUser(t, "estranho-cmp@example.com", "Sup3rSecret!pass", "Estranho")
	token := a.issueToken(t, stranger.ID())

	real := a.do(t, http.MethodGet, "/v1.0/organizations/"+orgID+"/companies", token, "")
	fake := a.do(t, http.MethodGet, "/v1.0/organizations/org_nao_existe/companies", token, "")
	if real.StatusCode != http.StatusForbidden || fake.StatusCode != http.StatusForbidden {
		t.Fatalf("statuses = %d and %d, want 403 for both", real.StatusCode, fake.StatusCode)
	}
	if got := problemOf(t, real).Detail; got != "You do not have access to this organization." {
		t.Errorf("detail = %q", got)
	}
}

// A viewer reads and does not write. The control is absent on the screen for
// the same reason the route refuses here.
func TestAViewerMayListButNotRegister(t *testing.T) {
	a := newCompanyTestApp(t)
	orgID, ownerID, _ := a.seedOrg(t, "dono-cmp2@example.com")
	viewer := a.registerUser(t, "leitor-cmp@example.com", "Sup3rSecret!pass", "Leitor")
	if err := a.orgRepo.PutMembership(context.Background(), &orgDomain.Membership{
		OrganizationID: orgID, UserID: viewer.ID(), Role: orgDomain.RoleViewer, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seeding viewer: %v", err)
	}
	_ = ownerID
	token := a.issueToken(t, viewer.ID())

	if resp := a.do(t, http.MethodGet, "/v1.0/organizations/"+orgID+"/companies", token, ""); resp.StatusCode != http.StatusOK {
		t.Errorf("list status = %d, want 200", resp.StatusCode)
	}
	body := `{"tax_id":"11222333000181","legal_name":"Acme LTDA"}`
	if resp := a.do(t, http.MethodPost, "/v1.0/organizations/"+orgID+"/companies", token, body); resp.StatusCode != http.StatusForbidden {
		t.Errorf("register status = %d, want 403", resp.StatusCode)
	}
}

// The stored value is canonical, whatever the caller typed. A CNPJ is
// alphanumeric in its first twelve positions, so the response must carry the
// letters — a client assuming digits would mangle it.
func TestRegisteringACompanyReturnsItCanonical(t *testing.T) {
	a := newCompanyTestApp(t)
	orgID, _, token := a.seedOrg(t, "dono-cmp3@example.com")

	body := `{"tax_id":"12.abc.345/01DE-35","legal_name":" Acme LTDA ","trade_name":"Acme"}`
	resp := a.do(t, http.MethodPost, "/v1.0/organizations/"+orgID+"/companies", token, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", resp.StatusCode, bodyString(resp))
	}
	var got struct {
		ID        string `json:"id"`
		TaxID     string `json:"tax_id"`
		TaxIDKind string `json:"tax_id_kind"`
		LegalName string `json:"legal_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.TaxID != "12ABC34501DE35" || got.TaxIDKind != "cnpj" {
		t.Errorf("tax id = %q %q, want 12ABC34501DE35 cnpj", got.TaxID, got.TaxIDKind)
	}
	if got.LegalName != "Acme LTDA" {
		t.Errorf("legal name = %q", got.LegalName)
	}
	if got.ID == "" {
		t.Error("no company id")
	}
}

// The registrant can act for what they registered, without a second request.
func TestRegisteringGrantsTheRegistrantTheEdge(t *testing.T) {
	a := newCompanyTestApp(t)
	orgID, ownerID, token := a.seedOrg(t, "dono-cmp4@example.com")

	body := `{"tax_id":"11222333000181","legal_name":"Acme LTDA"}`
	resp := a.do(t, http.MethodPost, "/v1.0/organizations/"+orgID+"/companies", token, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d (%s)", resp.StatusCode, bodyString(resp))
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&created)

	if _, err := a.repo.GetActor(context.Background(), orgID, created.ID, ownerID); err != nil {
		t.Fatalf("the registrant cannot act for what they registered: %v", err)
	}
}

func TestABadTaxIDIsAValidationProblem(t *testing.T) {
	a := newCompanyTestApp(t)
	orgID, _, token := a.seedOrg(t, "dono-cmp5@example.com")

	body := `{"tax_id":"11222333000182","legal_name":"Acme LTDA"}`
	resp := a.do(t, http.MethodPost, "/v1.0/organizations/"+orgID+"/companies", token, body)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", resp.StatusCode, bodyString(resp))
	}
}

// A second registration of one tax id is a conflict, not a validation failure:
// the request was well formed and the state refused it.
func TestTheSameTaxIDTwiceIsAConflict(t *testing.T) {
	a := newCompanyTestApp(t)
	orgID, _, token := a.seedOrg(t, "dono-cmp6@example.com")

	body := `{"tax_id":"11222333000181","legal_name":"Acme LTDA"}`
	if resp := a.do(t, http.MethodPost, "/v1.0/organizations/"+orgID+"/companies", token, body); resp.StatusCode != http.StatusCreated {
		t.Fatalf("first registration failed: %d (%s)", resp.StatusCode, bodyString(resp))
	}
	// Masked differently, so the canonical form is what the lock sees.
	masked := `{"tax_id":"11.222.333/0001-81","legal_name":"Acme LTDA"}`
	resp := a.do(t, http.MethodPost, "/v1.0/organizations/"+orgID+"/companies", token, masked)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", resp.StatusCode, bodyString(resp))
	}
}

// The lookup reads a public register, not organization data, and the create
// screen needs it before an organization exists — so it lives outside the
// organization scope, where no /:company_id can capture it.
func TestTheLookupNeedsNoOrganization(t *testing.T) {
	a := newCompanyTestApp(t)
	_, _, token := a.seedOrg(t, "dono-cmp7@example.com")
	a.lookup.names = registry.Names{LegalName: "ACME COMERCIO LTDA", TradeName: "Acme"}
	a.lookup.found = true

	resp := a.do(t, http.MethodGet, "/v1.0/companies/lookup?tax_id=11222333000181", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, bodyString(resp))
	}
	var got struct {
		Found     bool   `json:"found"`
		LegalName string `json:"legal_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if !got.Found || got.LegalName != "ACME COMERCIO LTDA" {
		t.Errorf("got %+v", got)
	}
}

// A register that knows nothing answers 200 with found=false. A 404 would read
// as "invalid CNPJ" to a screen that has to branch on something, and the CNPJ
// is valid — the register simply has not heard of it.
func TestAnUnknownCNPJIsNotAnError(t *testing.T) {
	a := newCompanyTestApp(t)
	_, _, token := a.seedOrg(t, "dono-cmp8@example.com")
	a.lookup.found = false

	resp := a.do(t, http.MethodGet, "/v1.0/companies/lookup?tax_id=11222333000181", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, bodyString(resp))
	}
	var got struct {
		Found bool `json:"found"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got.Found {
		t.Error("found = true for a register that answered nothing")
	}
}

// A CPF is never looked up: a person's name is not ours to fetch from a public
// register, and the route must not quietly try.
func TestACPFIsNeverLookedUp(t *testing.T) {
	a := newCompanyTestApp(t)
	_, _, token := a.seedOrg(t, "dono-cmp9@example.com")
	a.lookup.names = registry.Names{LegalName: "SHOULD NOT APPEAR"}
	a.lookup.found = true

	resp := a.do(t, http.MethodGet, "/v1.0/companies/lookup?tax_id=52998224725", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got struct {
		Found     bool   `json:"found"`
		LegalName string `json:"legal_name"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got.Found || got.LegalName != "" {
		t.Errorf("a CPF was looked up: %+v", got)
	}
}

// Granting is idempotent — PUT, not POST — so a repeated grant is not an error
// the caller has to special-case.
func TestGrantingTheEdgeTwiceIsNotAnError(t *testing.T) {
	a := newCompanyTestApp(t)
	orgID, _, token := a.seedOrg(t, "dono-cmp10@example.com")
	colleague := a.registerUser(t, "colega-cmp@example.com", "Sup3rSecret!pass", "Colega")

	created := a.do(t, http.MethodPost, "/v1.0/organizations/"+orgID+"/companies", token,
		`{"tax_id":"11222333000181","legal_name":"Acme LTDA"}`)
	var body struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(created.Body).Decode(&body)

	path := "/v1.0/organizations/" + orgID + "/companies/" + body.ID + "/actors/" + colleague.ID()
	for i := 0; i < 2; i++ {
		if resp := a.do(t, http.MethodPut, path, token, ""); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("grant %d: status = %d, want 204 (%s)", i+1, resp.StatusCode, bodyString(resp))
		}
	}
}

// newInternalReachApp mounts the internal route the way main.go does, with the
// same scope guard, so the test exercises the gate and not just the handler.
func newInternalReachApp(t *testing.T) *companyTestApp {
	t.Helper()
	a := newCompanyTestApp(t)
	handler.NewCompanyHandler(companyDomain.NewService(a.repo, time.Now), a.orgSvc, a.testApp.userSvc, nil).
		RegisterInternal(a.app.Group("/v1.0"),
			middleware.RequireAuth(a.testApp.jwtSvc),
			middleware.RequireInternalScope(scopes.InternalAccountCompanyActor))
	return a
}

// This route is how another product learns reach. A token without the internal
// scope must not be able to ask it — including a perfectly valid user token,
// which is what a compromised first-party client would hold.
func TestTheReachCheckNeedsTheInternalScope(t *testing.T) {
	a := newInternalReachApp(t)
	user := a.registerUser(t, "reach-nogo@example.com", "Sup3rSecret!pass", "Sem escopo")
	resp := a.do(t, http.MethodGet, "/v1.0/internal/companies/cmp_1/actors/usr_1", a.issueToken(t, user.ID()), "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — a user token reached the internal route", resp.StatusCode)
	}
}

// An unknown company and a real one this person cannot reach must be
// indistinguishable, or the route is a probe for which company ids exist.
func TestAnUnknownCompanyAndARefusalAnswerAlike(t *testing.T) {
	a := newInternalReachApp(t)
	orgID, ownerID, _ := a.seedOrg(t, "reach-probe@example.com")
	stranger := a.registerUser(t, "reach-stranger@example.com", "Sup3rSecret!pass", "Estranho")
	internal := a.issueServiceToken(t, []string{scopes.InternalAccountCompanyActor})

	real, err := companyDomain.NewService(a.repo, time.Now).
		Register(context.Background(), orgID, ownerID, "Dono", "11222333000181", "Acme LTDA", "")
	if err != nil {
		t.Fatalf("seeding company: %v", err)
	}

	unknown := bodyString(a.do(t, http.MethodGet, "/v1.0/internal/companies/cmp_nope/actors/"+stranger.ID(), internal, ""))
	refused := bodyString(a.do(t, http.MethodGet, "/v1.0/internal/companies/"+real.ID+"/actors/"+stranger.ID(), internal, ""))
	if unknown != refused {
		t.Fatalf("distinguishable:\n  unknown: %s\n  refused: %s", unknown, refused)
	}
}

// The happy path, and the shape the DF-e depends on: reach plus the
// organization it did not know, and nothing that belongs to the product.
func TestTheReachAnswerCarriesTheOrganizationAndNoRole(t *testing.T) {
	a := newInternalReachApp(t)
	orgID, ownerID, _ := a.seedOrg(t, "reach-ok@example.com")
	internal := a.issueServiceToken(t, []string{scopes.InternalAccountCompanyActor})

	real, err := companyDomain.NewService(a.repo, time.Now).
		Register(context.Background(), orgID, ownerID, "Dono", "11222333000181", "Acme LTDA", "")
	if err != nil {
		t.Fatalf("seeding company: %v", err)
	}

	resp := a.do(t, http.MethodGet, "/v1.0/internal/companies/"+real.ID+"/actors/"+ownerID, internal, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if body["may_act"] != true || body["organization_id"] != orgID {
		t.Fatalf("got %+v, want may_act true and org %q", body, orgID)
	}
	for _, forbidden := range []string{"role", "roles", "permissions"} {
		if _, present := body[forbidden]; present {
			t.Errorf("the reach answer carries %q, which belongs to the product", forbidden)
		}
	}
}
