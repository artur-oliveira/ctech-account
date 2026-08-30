package company

import (
	"context"
	"testing"
	"time"
)

func fixedClock() time.Time { return time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC) }

// fakeRepo is a map-backed Repository. It reproduces the conditions the real
// one enforces in DynamoDB — notably the tax-id lock — because a fake that
// accepts what the database refuses makes these tests agree with nothing that
// runs in production.
type fakeRepo struct {
	companies map[string]map[string]*Company // orgID -> companyID
	taxIDs    map[string]map[string]string   // orgID -> canonical tax id -> companyID
	actors    map[string]map[string]*Actor   // orgID|companyID -> userID
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		companies: map[string]map[string]*Company{},
		taxIDs:    map[string]map[string]string{},
		actors:    map[string]map[string]*Actor{},
	}
}

func actorKey(orgID, companyID string) string { return orgID + "|" + companyID }

func (f *fakeRepo) Create(ctx context.Context, c *Company, first *Actor) error {
	if f.taxIDs[c.OrganizationID] == nil {
		f.taxIDs[c.OrganizationID] = map[string]string{}
	}
	if _, taken := f.taxIDs[c.OrganizationID][c.TaxID]; taken {
		return ErrTaxIDTaken
	}
	f.taxIDs[c.OrganizationID][c.TaxID] = c.ID
	if f.companies[c.OrganizationID] == nil {
		f.companies[c.OrganizationID] = map[string]*Company{}
	}
	copied := *c
	f.companies[c.OrganizationID][c.ID] = &copied
	if first != nil {
		return f.PutActor(ctx, first)
	}
	return nil
}

func (f *fakeRepo) Get(_ context.Context, orgID, companyID string) (*Company, error) {
	c, ok := f.companies[orgID][companyID]
	if !ok {
		return nil, ErrNotFound
	}
	copied := *c
	return &copied, nil
}

func (f *fakeRepo) List(_ context.Context, orgID string) ([]*Company, error) {
	out := make([]*Company, 0, len(f.companies[orgID]))
	for _, c := range f.companies[orgID] {
		copied := *c
		out = append(out, &copied)
	}
	return out, nil
}

func (f *fakeRepo) GetBySourceRef(_ context.Context, system, ref string) (*Company, error) {
	for _, byID := range f.companies {
		for _, c := range byID {
			if c.SourceSystem == system && c.SourceRef == ref && ref != "" {
				copied := *c
				return &copied, nil
			}
		}
	}
	return nil, ErrNotFound
}

func (f *fakeRepo) UpdateNames(_ context.Context, orgID, companyID, legal, trade string, now time.Time) error {
	c, ok := f.companies[orgID][companyID]
	if !ok {
		return ErrNotFound
	}
	c.LegalName, c.TradeName, c.UpdatedAt = legal, trade, now
	return nil
}

func (f *fakeRepo) PutActor(_ context.Context, a *Actor) error {
	k := actorKey(a.OrganizationID, a.CompanyID)
	if f.actors[k] == nil {
		f.actors[k] = map[string]*Actor{}
	}
	copied := *a
	f.actors[k][a.UserID] = &copied
	return nil
}

func (f *fakeRepo) GetActor(_ context.Context, orgID, companyID, userID string) (*Actor, error) {
	a, ok := f.actors[actorKey(orgID, companyID)][userID]
	if !ok {
		return nil, ErrNotFound
	}
	copied := *a
	return &copied, nil
}

func (f *fakeRepo) ListActors(_ context.Context, orgID, companyID string) ([]*Actor, error) {
	byUser := f.actors[actorKey(orgID, companyID)]
	out := make([]*Actor, 0, len(byUser))
	for _, a := range byUser {
		copied := *a
		out = append(out, &copied)
	}
	return out, nil
}

func (f *fakeRepo) ListForUser(_ context.Context, userID string) ([]*Actor, error) {
	out := make([]*Actor, 0)
	for _, byUser := range f.actors {
		if a, ok := byUser[userID]; ok {
			copied := *a
			out = append(out, &copied)
		}
	}
	return out, nil
}

func (f *fakeRepo) RemoveActor(_ context.Context, orgID, companyID, userID string) error {
	k := actorKey(orgID, companyID)
	if _, ok := f.actors[k][userID]; !ok {
		return ErrNotFound
	}
	delete(f.actors[k], userID)
	return nil
}

func newService() (*Service, *fakeRepo) {
	repo := newFakeRepo()
	return NewService(repo, fixedClock), repo
}

func TestRegisterNormalizesTheTaxIDAndNamesTheKind(t *testing.T) {
	svc, _ := newService()
	c, err := svc.Register(context.Background(), "org_1", "usr_1", "Ana", "12.ABC.345/01DE-35", " Acme LTDA ", "Acme")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if c.TaxID != "12ABC34501DE35" || c.TaxIDKind != KindCNPJ {
		t.Errorf("tax id = %q %q", c.TaxID, c.TaxIDKind)
	}
	if c.LegalName != "Acme LTDA" {
		t.Errorf("legal name = %q", c.LegalName)
	}
	if c.ID == "" {
		t.Error("company id is empty")
	}
}

// The person who registers a company can act for it immediately. Anything else
// means registering one and then being unable to use it.
func TestRegisteringGrantsTheRegistrantTheActorEdge(t *testing.T) {
	svc, _ := newService()
	c, _ := svc.Register(context.Background(), "org_1", "usr_1", "Ana", "11222333000181", "Acme LTDA", "")
	ok, err := svc.MayAct(context.Background(), "org_1", c.ID, "usr_1")
	if err != nil || !ok {
		t.Fatalf("MayAct = %v %v, want true", ok, err)
	}
}

// The load-bearing case: an accountant and their client each hold the same
// CNPJ. Pinned so nobody "fixes" the scoping into a global unique key — under
// which a client changing accountants could not be registered until the former
// one deleted, which they must not do for five years.
func TestTheSameTaxIDInTwoOrganizationsIsAllowed(t *testing.T) {
	svc, _ := newService()
	if _, err := svc.Register(context.Background(), "org_client", "usr_1", "Ana", "11222333000181", "Acme LTDA", ""); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := svc.Register(context.Background(), "org_accountant", "usr_2", "Bruno", "11222333000181", "Acme LTDA", ""); err != nil {
		t.Fatalf("second organization refused the same tax id: %v", err)
	}
}

func TestTheSameTaxIDTwiceInOneOrganizationIsRefused(t *testing.T) {
	svc, _ := newService()
	if _, err := svc.Register(context.Background(), "org_1", "usr_1", "Ana", "12ABC34501DE35", "Acme LTDA", ""); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Masked and lowercased on purpose: normalization must happen before the
	// lock is consulted, or one company registers twice by being typed twice.
	_, err := svc.Register(context.Background(), "org_1", "usr_1", "Ana", "12.abc.345/01de-35", "Acme LTDA", "")
	if err != ErrTaxIDTaken {
		t.Fatalf("err = %v, want ErrTaxIDTaken", err)
	}
}

func TestRegisterRefusesABadTaxID(t *testing.T) {
	svc, _ := newService()
	if _, err := svc.Register(context.Background(), "org_1", "usr_1", "Ana", "11222333000182", "Acme LTDA", ""); err != ErrInvalidTaxID {
		t.Fatalf("err = %v, want ErrInvalidTaxID", err)
	}
}

func TestRegisterRefusesAnEmptyLegalName(t *testing.T) {
	svc, _ := newService()
	if _, err := svc.Register(context.Background(), "org_1", "usr_1", "Ana", "11222333000181", "  ", ""); err != ErrInvalidName {
		t.Fatalf("err = %v, want ErrInvalidName", err)
	}
}

// Membership is what gets you into the workspace; the edge is what lets you act
// for a company inside it. A colleague with no edge acts for nothing.
func TestBeingInTheOrganizationDoesNotGrantTheActorEdge(t *testing.T) {
	svc, _ := newService()
	c, _ := svc.Register(context.Background(), "org_1", "usr_1", "Ana", "11222333000181", "Acme LTDA", "")
	ok, err := svc.MayAct(context.Background(), "org_1", c.ID, "usr_colleague")
	if err != nil {
		t.Fatalf("MayAct: %v", err)
	}
	if ok {
		t.Error("a colleague with no edge may act for the company")
	}
}

func TestGrantingAndRevokingTheEdge(t *testing.T) {
	svc, _ := newService()
	c, _ := svc.Register(context.Background(), "org_1", "usr_1", "Ana", "11222333000181", "Acme LTDA", "")
	if err := svc.GrantActor(context.Background(), "org_1", c.ID, "usr_2", "Bruno", "usr_1"); err != nil {
		t.Fatalf("GrantActor: %v", err)
	}
	if ok, _ := svc.MayAct(context.Background(), "org_1", c.ID, "usr_2"); !ok {
		t.Fatal("grant did not take effect")
	}
	if err := svc.RevokeActor(context.Background(), "org_1", c.ID, "usr_2"); err != nil {
		t.Fatalf("RevokeActor: %v", err)
	}
	if ok, _ := svc.MayAct(context.Background(), "org_1", c.ID, "usr_2"); ok {
		t.Error("revoke did not take effect")
	}
}

// Granting on a company from another organization must fail rather than write
// a row under this organization that nothing can reach.
func TestGrantingOnACompanyOutsideTheOrganizationIsRefused(t *testing.T) {
	svc, _ := newService()
	other, _ := svc.Register(context.Background(), "org_other", "usr_1", "Ana", "11222333000181", "Acme LTDA", "")
	if err := svc.GrantActor(context.Background(), "org_1", other.ID, "usr_2", "Bruno", "usr_1"); err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if err := svc.GrantActor(context.Background(), "org_1", "cmp_nope", "usr_2", "Bruno", "usr_1"); err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestListOrdersByTheNamePeopleRead(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()
	_, _ = svc.Register(ctx, "org_1", "usr_1", "Ana", "11222333000181", "Zeta LTDA", "")
	_, _ = svc.Register(ctx, "org_1", "usr_1", "Ana", "12ABC34501DE35", "acme LTDA", "")
	companies, err := svc.List(ctx, "org_1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(companies) != 2 || companies[0].LegalName != "acme LTDA" {
		t.Fatalf("order = %v", []string{companies[0].LegalName, companies[1].LegalName})
	}
}

// The question ctech-dfe asks: "may this person act for this company", with no
// organization to scope by — the caller does not know it, and finding out is
// half of what it is asking.
func TestReachOfAnswersWithoutAnOrganization(t *testing.T) {
	svc, _ := newService()
	c, _ := svc.Register(context.Background(), "org_1", "usr_1", "Ana", "11222333000181", "Acme LTDA", "")

	orgID, ok, err := svc.ReachOf(context.Background(), c.ID, "usr_1")
	if err != nil {
		t.Fatalf("ReachOf: %v", err)
	}
	if !ok || orgID != "org_1" {
		t.Fatalf("got %q %v, want org_1 true", orgID, ok)
	}
}

// No edge is false with no error. "Not permitted" is an answer, and a caller
// forced to tell it from a failure will treat one as the other.
func TestReachOfIsFalseWithoutAnEdge(t *testing.T) {
	svc, _ := newService()
	c, _ := svc.Register(context.Background(), "org_1", "usr_1", "Ana", "11222333000181", "Acme LTDA", "")

	orgID, ok, err := svc.ReachOf(context.Background(), c.ID, "usr_stranger")
	if err != nil || ok || orgID != "" {
		t.Fatalf("got %q %v %v, want empty false nil", orgID, ok, err)
	}
}

// A person who acts for several companies gets an answer about the one asked
// about, not the first one found.
func TestReachOfPicksTheCompanyAsked(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()
	a, _ := svc.Register(ctx, "org_1", "usr_1", "Ana", "11222333000181", "Acme LTDA", "")
	b, _ := svc.Register(ctx, "org_2", "usr_1", "Ana", "12ABC34501DE35", "Beta LTDA", "")

	if orgID, _, _ := svc.ReachOf(ctx, a.ID, "usr_1"); orgID != "org_1" {
		t.Errorf("company a resolved to %q", orgID)
	}
	if orgID, _, _ := svc.ReachOf(ctx, b.ID, "usr_1"); orgID != "org_2" {
		t.Errorf("company b resolved to %q", orgID)
	}
}

// An unknown company is false, not an error — and indistinguishable from a real
// company this person cannot reach, so the answer is not a probe for which
// company ids exist.
func TestReachOfOnAnUnknownCompanyLooksLikeARefusal(t *testing.T) {
	svc, _ := newService()
	unknownOrg, unknownOK, unknownErr := svc.ReachOf(context.Background(), "cmp_nope", "usr_1")

	c, _ := svc.Register(context.Background(), "org_1", "usr_1", "Ana", "11222333000181", "Acme LTDA", "")
	refusedOrg, refusedOK, refusedErr := svc.ReachOf(context.Background(), c.ID, "usr_stranger")

	if unknownOrg != refusedOrg || unknownOK != refusedOK || (unknownErr == nil) != (refusedErr == nil) {
		t.Fatalf("an unknown company (%q %v %v) is distinguishable from a refusal (%q %v %v)",
			unknownOrg, unknownOK, unknownErr, refusedOrg, refusedOK, refusedErr)
	}
}
