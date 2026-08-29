package handler_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"gopkg.aoctech.app/account/api/internal/apierror"
	orgDomain "gopkg.aoctech.app/account/api/internal/domain/organization"
	"gopkg.aoctech.app/account/api/internal/handler"
	"gopkg.aoctech.app/account/api/internal/middleware"
)

// memOrgRepo is an in-memory organization.Repository. It reproduces the two
// conditions the DynamoDB implementation enforces — a membership is written
// only if absent, and the owner's row is neither re-roled nor deleted — because
// a fake that allows what the database refuses tests nothing that ships.
type memOrgRepo struct {
	orgs        map[string]*orgDomain.Organization
	memberships map[string]map[string]*orgDomain.Membership
	invitations map[string]map[string]*orgDomain.Invitation
}

func newMemOrgRepo() *memOrgRepo {
	return &memOrgRepo{
		orgs:        map[string]*orgDomain.Organization{},
		memberships: map[string]map[string]*orgDomain.Membership{},
		invitations: map[string]map[string]*orgDomain.Invitation{},
	}
}

func (m *memOrgRepo) CreateWithOwner(_ context.Context, org *orgDomain.Organization) error {
	if _, exists := m.orgs[org.ID]; exists {
		return orgDomain.ErrAlreadyMember
	}
	copied := *org
	m.orgs[org.ID] = &copied
	m.memberships[org.ID] = map[string]*orgDomain.Membership{
		org.OwnerUserID: {OrganizationID: org.ID, UserID: org.OwnerUserID, Role: orgDomain.RoleOwner, CreatedAt: org.CreatedAt},
	}
	return nil
}

func (m *memOrgRepo) Get(_ context.Context, id string) (*orgDomain.Organization, error) {
	org, ok := m.orgs[id]
	if !ok {
		return nil, orgDomain.ErrNotFound
	}
	copied := *org
	return &copied, nil
}

func (m *memOrgRepo) GetBySourceRef(_ context.Context, system, ref string) (*orgDomain.Organization, error) {
	for _, org := range m.orgs {
		if org.SourceSystem == system && org.SourceRef == ref && ref != "" {
			copied := *org
			return &copied, nil
		}
	}
	return nil, orgDomain.ErrNotFound
}

func (m *memOrgRepo) UpdateDisplayName(_ context.Context, id, name string, now time.Time) error {
	org, ok := m.orgs[id]
	if !ok {
		return orgDomain.ErrNotFound
	}
	org.DisplayName, org.UpdatedAt = name, now
	return nil
}

func (m *memOrgRepo) GetMembership(_ context.Context, orgID, userID string) (*orgDomain.Membership, error) {
	mm, ok := m.memberships[orgID][userID]
	if !ok {
		return nil, orgDomain.ErrNotFound
	}
	copied := *mm
	return &copied, nil
}

func (m *memOrgRepo) ListMembers(_ context.Context, orgID string) ([]*orgDomain.Membership, error) {
	out := make([]*orgDomain.Membership, 0, len(m.memberships[orgID]))
	for _, mm := range m.memberships[orgID] {
		copied := *mm
		out = append(out, &copied)
	}
	return out, nil
}

func (m *memOrgRepo) ListForUser(_ context.Context, userID string) ([]*orgDomain.Membership, error) {
	out := make([]*orgDomain.Membership, 0)
	for _, byUser := range m.memberships {
		if mm, ok := byUser[userID]; ok {
			copied := *mm
			out = append(out, &copied)
		}
	}
	return out, nil
}

func (m *memOrgRepo) PutMembership(_ context.Context, mm *orgDomain.Membership) error {
	if m.memberships[mm.OrganizationID] == nil {
		m.memberships[mm.OrganizationID] = map[string]*orgDomain.Membership{}
	}
	if _, exists := m.memberships[mm.OrganizationID][mm.UserID]; exists {
		return orgDomain.ErrAlreadyMember
	}
	copied := *mm
	m.memberships[mm.OrganizationID][mm.UserID] = &copied
	return nil
}

func (m *memOrgRepo) SetRole(_ context.Context, orgID, userID, role string) error {
	mm, ok := m.memberships[orgID][userID]
	if !ok || mm.Role == orgDomain.RoleOwner {
		return orgDomain.ErrNotFound
	}
	mm.Role = role
	return nil
}

func (m *memOrgRepo) RemoveMembership(_ context.Context, orgID, userID string) error {
	mm, ok := m.memberships[orgID][userID]
	if !ok || mm.Role == orgDomain.RoleOwner {
		return orgDomain.ErrNotFound
	}
	delete(m.memberships[orgID], userID)
	return nil
}

func (m *memOrgRepo) TransferOwnership(_ context.Context, orgID, fromUserID, toUserID string, now time.Time) error {
	from, okFrom := m.memberships[orgID][fromUserID]
	to, okTo := m.memberships[orgID][toUserID]
	if !okFrom || !okTo || from.Role != orgDomain.RoleOwner || to.Role == orgDomain.RoleOwner {
		return orgDomain.ErrNotFound
	}
	from.Role, to.Role = orgDomain.RoleAdmin, orgDomain.RoleOwner
	if org, ok := m.orgs[orgID]; ok {
		org.OwnerUserID, org.UpdatedAt = toUserID, now
	}
	return nil
}

func (m *memOrgRepo) PutInvitation(_ context.Context, inv *orgDomain.Invitation) error {
	if m.invitations[inv.OrganizationID] == nil {
		m.invitations[inv.OrganizationID] = map[string]*orgDomain.Invitation{}
	}
	copied := *inv
	m.invitations[inv.OrganizationID][orgDomain.NormalizeEmail(inv.Email)] = &copied
	return nil
}

func (m *memOrgRepo) GetInvitationByToken(_ context.Context, tokenHash string) (*orgDomain.Invitation, error) {
	for _, byEmail := range m.invitations {
		for _, inv := range byEmail {
			if inv.TokenHash == tokenHash {
				copied := *inv
				return &copied, nil
			}
		}
	}
	return nil, orgDomain.ErrNotFound
}

func (m *memOrgRepo) ListInvitations(_ context.Context, orgID string) ([]*orgDomain.Invitation, error) {
	out := make([]*orgDomain.Invitation, 0, len(m.invitations[orgID]))
	for _, inv := range m.invitations[orgID] {
		copied := *inv
		out = append(out, &copied)
	}
	return out, nil
}

func (m *memOrgRepo) DeleteInvitation(_ context.Context, orgID, email string) error {
	delete(m.invitations[orgID], orgDomain.NormalizeEmail(email))
	return nil
}

func (m *memOrgRepo) AcceptInvitation(ctx context.Context, mm *orgDomain.Membership, invitedEmail string) error {
	if err := m.PutMembership(ctx, mm); err != nil {
		return err
	}
	return m.DeleteInvitation(ctx, mm.OrganizationID, invitedEmail)
}

// orgTestApp mounts the organization routes on the shared test account service,
// so acceptance runs against a real user record with a real verified flag.
type orgTestApp struct {
	*testApp
	app  *fiber.App
	repo *memOrgRepo
	svc  *orgDomain.Service
}

func newOrgTestApp(t *testing.T) *orgTestApp {
	t.Helper()
	base := newTestApp(t)
	repo := newMemOrgRepo()
	svc := orgDomain.NewService(repo, time.Now)

	app := fiber.New(fiber.Config{
		ErrorHandler: func(c fiber.Ctx, err error) error {
			if problem, ok := errors.AsType[*apierror.Problem](err); ok {
				return problem.Send(c)
			}
			return apierror.ServerError(c.Path()).Send(c)
		},
	})
	app.Use(recover.New())
	v1 := app.Group("/v1.0", middleware.RequireAuth(base.jwtSvc))
	handler.NewOrganizationHandler(svc, base.userSvc).Register(v1)
	return &orgTestApp{testApp: base, app: app, repo: repo, svc: svc}
}

func (a *orgTestApp) do(t *testing.T, method, path, token, body string) *http.Response {
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

// The e-mail an invitation is checked against is the account's stored address,
// never a field in the request body. A body-supplied address would turn the
// token into a bearer capability: whoever found the link could name the invited
// address and join.
func TestAcceptUsesTheAccountsVerifiedEmail(t *testing.T) {
	a := newOrgTestApp(t)
	owner := a.registerUser(t, "dono@example.com", "Sup3rSecret!pass", "Dono")
	intruder := a.registerUser(t, "intruso@example.com", "Sup3rSecret!pass", "Intruso")

	org, err := a.svc.Create(context.Background(), owner.ID(), "CTech")
	if err != nil {
		t.Fatalf("seeding organization: %v", err)
	}
	token, err := a.svc.Invite(context.Background(), org.ID, owner.ID(), "convidado@example.com", orgDomain.RoleMember)
	if err != nil {
		t.Fatalf("inviting: %v", err)
	}

	// The intruder holds the token and names the invited address in the body.
	resp := a.do(t, http.MethodPost, "/v1.0/invitations/accept",
		a.issueToken(t, intruder.ID()),
		`{"token":"`+token+`","email":"convidado@example.com"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — the body's address was trusted (%s)", resp.StatusCode, bodyString(resp))
	}
	if _, err := a.repo.GetMembership(context.Background(), org.ID, intruder.ID()); err == nil {
		t.Fatal("the intruder was added to the organization")
	}
}

// An unverified address is a claim, not evidence: accepting on one would let
// somebody type the invited address at sign-up and walk in.
func TestAcceptRefusesAnUnverifiedAccount(t *testing.T) {
	a := newOrgTestApp(t)
	owner := a.registerUser(t, "dono2@example.com", "Sup3rSecret!pass", "Dono")
	invitee := a.registerUnverifiedUser(t, "convidado2@example.com", "Sup3rSecret!pass", "Convidado")

	org, _ := a.svc.Create(context.Background(), owner.ID(), "CTech")
	token, _ := a.svc.Invite(context.Background(), org.ID, owner.ID(), "convidado2@example.com", orgDomain.RoleMember)

	resp := a.do(t, http.MethodPost, "/v1.0/invitations/accept",
		a.issueToken(t, invitee.ID()), `{"token":"`+token+`"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", resp.StatusCode, bodyString(resp))
	}
}

// The happy path, so the refusals above are refusals and not a broken route.
func TestAcceptAddsTheInvitedMember(t *testing.T) {
	a := newOrgTestApp(t)
	owner := a.registerUser(t, "dono3@example.com", "Sup3rSecret!pass", "Dono")
	invitee := a.registerUser(t, "convidado3@example.com", "Sup3rSecret!pass", "Convidado")

	org, _ := a.svc.Create(context.Background(), owner.ID(), "CTech")
	token, _ := a.svc.Invite(context.Background(), org.ID, owner.ID(), "convidado3@example.com", orgDomain.RoleMember)

	resp := a.do(t, http.MethodPost, "/v1.0/invitations/accept",
		a.issueToken(t, invitee.ID()), `{"token":"`+token+`"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", resp.StatusCode, bodyString(resp))
	}
	m, err := a.repo.GetMembership(context.Background(), org.ID, invitee.ID())
	if err != nil {
		t.Fatalf("the invitee was not added: %v", err)
	}
	if m.Role != orgDomain.RoleMember {
		t.Fatalf("role = %q, want member", m.Role)
	}
}

// A non-member gets the same refusal whether the organization exists or not, so
// the API cannot be walked to discover organization ids.
func TestOrganizationRoutesRefuseNonMembers(t *testing.T) {
	a := newOrgTestApp(t)
	owner := a.registerUser(t, "dono4@example.com", "Sup3rSecret!pass", "Dono")
	stranger := a.registerUser(t, "estranho@example.com", "Sup3rSecret!pass", "Estranho")
	org, _ := a.svc.Create(context.Background(), owner.ID(), "CTech")

	token := a.issueToken(t, stranger.ID())
	real := a.do(t, http.MethodGet, "/v1.0/organizations/"+org.ID, token, "")
	fake := a.do(t, http.MethodGet, "/v1.0/organizations/org_does_not_exist", token, "")
	if real.StatusCode != http.StatusForbidden || fake.StatusCode != http.StatusForbidden {
		t.Fatalf("statuses = %d and %d, want 403 for both", real.StatusCode, fake.StatusCode)
	}
}
