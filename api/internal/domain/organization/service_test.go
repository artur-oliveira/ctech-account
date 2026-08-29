package organization

import (
	"context"
	"testing"
	"time"
)

func fixedClock() time.Time { return time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC) }

// fakeRepo is a map-backed Repository. It reproduces the conditions the real
// one enforces in DynamoDB — put-if-absent, owner is not overwritable — because
// a fake that accepts what the database refuses makes the service tests agree
// with nothing that runs in production.
type fakeRepo struct {
	orgs        map[string]*Organization
	memberships map[string]map[string]*Membership // orgID -> userID -> membership
	invitations map[string]map[string]*Invitation // orgID -> email -> invitation
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		orgs:        map[string]*Organization{},
		memberships: map[string]map[string]*Membership{},
		invitations: map[string]map[string]*Invitation{},
	}
}

func (f *fakeRepo) CreateWithOwner(_ context.Context, org *Organization) error {
	if _, exists := f.orgs[org.ID]; exists {
		return ErrAlreadyMember
	}
	copied := *org
	f.orgs[org.ID] = &copied
	f.memberships[org.ID] = map[string]*Membership{
		org.OwnerUserID: {OrganizationID: org.ID, UserID: org.OwnerUserID, Role: RoleOwner, CreatedAt: org.CreatedAt},
	}
	return nil
}

func (f *fakeRepo) Get(_ context.Context, id string) (*Organization, error) {
	org, ok := f.orgs[id]
	if !ok {
		return nil, ErrNotFound
	}
	copied := *org
	return &copied, nil
}

func (f *fakeRepo) UpdateDisplayName(_ context.Context, id, name string, now time.Time) error {
	org, ok := f.orgs[id]
	if !ok {
		return ErrNotFound
	}
	org.DisplayName = name
	org.UpdatedAt = now
	return nil
}

func (f *fakeRepo) GetMembership(_ context.Context, orgID, userID string) (*Membership, error) {
	m, ok := f.memberships[orgID][userID]
	if !ok {
		return nil, ErrNotFound
	}
	copied := *m
	return &copied, nil
}

func (f *fakeRepo) ListMembers(_ context.Context, orgID string) ([]*Membership, error) {
	out := make([]*Membership, 0, len(f.memberships[orgID]))
	for _, m := range f.memberships[orgID] {
		copied := *m
		out = append(out, &copied)
	}
	return out, nil
}

func (f *fakeRepo) ListForUser(_ context.Context, userID string) ([]*Membership, error) {
	out := make([]*Membership, 0)
	for _, byUser := range f.memberships {
		if m, ok := byUser[userID]; ok {
			copied := *m
			out = append(out, &copied)
		}
	}
	return out, nil
}

func (f *fakeRepo) PutMembership(_ context.Context, m *Membership) error {
	if f.memberships[m.OrganizationID] == nil {
		f.memberships[m.OrganizationID] = map[string]*Membership{}
	}
	if _, exists := f.memberships[m.OrganizationID][m.UserID]; exists {
		return ErrAlreadyMember
	}
	copied := *m
	f.memberships[m.OrganizationID][m.UserID] = &copied
	return nil
}

func (f *fakeRepo) SetRole(_ context.Context, orgID, userID, role string) error {
	if !IsGrantableRole(role) {
		return ErrNotGrantable
	}
	m, ok := f.memberships[orgID][userID]
	if !ok || m.Role == RoleOwner {
		return ErrNotFound
	}
	m.Role = role
	return nil
}

func (f *fakeRepo) RemoveMembership(_ context.Context, orgID, userID string) error {
	m, ok := f.memberships[orgID][userID]
	if !ok || m.Role == RoleOwner {
		return ErrNotFound
	}
	delete(f.memberships[orgID], userID)
	return nil
}

func (f *fakeRepo) PutInvitation(_ context.Context, inv *Invitation) error {
	if f.invitations[inv.OrganizationID] == nil {
		f.invitations[inv.OrganizationID] = map[string]*Invitation{}
	}
	copied := *inv
	f.invitations[inv.OrganizationID][NormalizeEmail(inv.Email)] = &copied
	return nil
}

func (f *fakeRepo) GetInvitationByToken(_ context.Context, tokenHash string) (*Invitation, error) {
	for _, byEmail := range f.invitations {
		for _, inv := range byEmail {
			if inv.TokenHash == tokenHash {
				copied := *inv
				return &copied, nil
			}
		}
	}
	return nil, ErrNotFound
}

func (f *fakeRepo) ListInvitations(_ context.Context, orgID string) ([]*Invitation, error) {
	out := make([]*Invitation, 0, len(f.invitations[orgID]))
	for _, inv := range f.invitations[orgID] {
		copied := *inv
		out = append(out, &copied)
	}
	return out, nil
}

func (f *fakeRepo) DeleteInvitation(_ context.Context, orgID, email string) error {
	delete(f.invitations[orgID], NormalizeEmail(email))
	return nil
}

func TestCreateMakesTheCallerTheOwner(t *testing.T) {
	svc := NewService(newFakeRepo(), fixedClock)

	org, err := svc.Create(context.Background(), "usr_1", "CTech")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if org.ID == "" {
		t.Fatal("an organization needs an id")
	}
	role, err := svc.RoleOf(context.Background(), org.ID, "usr_1")
	if err != nil {
		t.Fatalf("RoleOf: %v", err)
	}
	if role != RoleOwner {
		t.Fatalf("role = %q, want owner", role)
	}
}

// A workspace with no name is a workspace nobody can tell apart in a switcher.
func TestCreateRefusesAnEmptyName(t *testing.T) {
	svc := NewService(newFakeRepo(), fixedClock)
	if _, err := svc.Create(context.Background(), "usr_1", "   "); err == nil {
		t.Fatal("accepted an organization with no name")
	}
}

func TestListForUserReturnsEveryMembership(t *testing.T) {
	svc := NewService(newFakeRepo(), fixedClock)
	ctx := context.Background()

	first, _ := svc.Create(ctx, "usr_1", "CTech")
	second, _ := svc.Create(ctx, "usr_1", "Contabilidade Silva")

	got, err := svc.ListForUser(ctx, "usr_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("memberships = %d, want 2 — a person may own several workspaces", len(got))
	}
	seen := map[string]bool{}
	for _, m := range got {
		seen[m.OrganizationID] = true
	}
	if !seen[first.ID] || !seen[second.ID] {
		t.Fatalf("memberships = %+v, want both organizations", got)
	}
}

// A stranger asking for an organization must not learn its name.
func TestGetRefusesANonMember(t *testing.T) {
	svc := NewService(newFakeRepo(), fixedClock)
	ctx := context.Background()
	org, _ := svc.Create(ctx, "usr_1", "CTech")
	if _, err := svc.Get(ctx, org.ID, "usr_stranger"); err == nil {
		t.Fatal("a non-member read the organization")
	}
}
