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

func (f *fakeRepo) GetBySourceRef(_ context.Context, system, ref string) (*Organization, error) {
	for _, org := range f.orgs {
		if org.SourceSystem == system && org.SourceRef == ref && ref != "" {
			copied := *org
			return &copied, nil
		}
	}
	return nil, ErrNotFound
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

func (f *fakeRepo) TransferOwnership(_ context.Context, orgID, fromUserID, toUserID string, now time.Time) error {
	from, okFrom := f.memberships[orgID][fromUserID]
	to, okTo := f.memberships[orgID][toUserID]
	if !okFrom || !okTo || from.Role != RoleOwner || to.Role == RoleOwner {
		return ErrNotFound
	}
	from.Role = RoleAdmin
	to.Role = RoleOwner
	if org, ok := f.orgs[orgID]; ok {
		org.OwnerUserID = toUserID
		org.UpdatedAt = now
	}
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

func (f *fakeRepo) AcceptInvitation(ctx context.Context, m *Membership, invitedEmail string) error {
	if err := f.PutMembership(ctx, m); err != nil {
		return err
	}
	return f.DeleteInvitation(ctx, m.OrganizationID, invitedEmail)
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

// seedOrg builds an organization owned by ownerID, plus a second member the
// role tests can move around.
func seedOrg(t *testing.T, ownerID string) (*Service, *Organization) {
	t.Helper()
	repo := newFakeRepo()
	svc := NewService(repo, fixedClock)
	org, err := svc.Create(context.Background(), ownerID, "CTech")
	if err != nil {
		t.Fatalf("seeding: %v", err)
	}
	return svc, org
}

func join(t *testing.T, svc *Service, orgID, userID, role string) {
	t.Helper()
	if err := svc.repo.PutMembership(context.Background(), &Membership{
		OrganizationID: orgID, UserID: userID, Role: role, CreatedAt: fixedClock(),
	}); err != nil {
		t.Fatalf("joining %s: %v", userID, err)
	}
}

func TestOwnerCannotBeGrantedThroughSetRole(t *testing.T) {
	svc, org := seedOrg(t, "usr_owner")
	ctx := context.Background()
	join(t, svc, org.ID, "usr_2", RoleViewer)
	if err := svc.SetRole(ctx, org.ID, "usr_owner", "usr_2", RoleMember); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetRole(ctx, org.ID, "usr_owner", "usr_2", RoleOwner); err == nil {
		t.Fatal("owner was handed out through member management")
	}
}

// The last owner leaving is an organization nobody can administer, and it is
// reachable by one careless click.
func TestTheLastOwnerCannotBeRemoved(t *testing.T) {
	svc, org := seedOrg(t, "usr_owner")
	if err := svc.Remove(context.Background(), org.ID, "usr_owner", "usr_owner"); err == nil {
		t.Fatal("removed the only owner")
	}
}

// Transfer moves the single owner. It never adds a second, and it never leaves
// zero — the demotion and the promotion are one write.
func TestTransferMovesOwnershipAtomically(t *testing.T) {
	svc, org := seedOrg(t, "usr_owner")
	ctx := context.Background()
	join(t, svc, org.ID, "usr_2", RoleAdmin)
	if err := svc.Transfer(ctx, org.ID, "usr_owner", "usr_2"); err != nil {
		t.Fatalf("Transfer: %v", err)
	}
	if role, _ := svc.RoleOf(ctx, org.ID, "usr_2"); role != RoleOwner {
		t.Fatalf("new owner role = %q", role)
	}
	if role, _ := svc.RoleOf(ctx, org.ID, "usr_owner"); role != RoleAdmin {
		t.Fatalf("old owner role = %q, want admin — demoted, not removed", role)
	}
}

// Transferring to a stranger is how an organization is handed to somebody who
// never accepted it.
func TestTransferRequiresAnExistingMember(t *testing.T) {
	svc, org := seedOrg(t, "usr_owner")
	if err := svc.Transfer(context.Background(), org.ID, "usr_owner", "usr_stranger"); err == nil {
		t.Fatal("transferred to somebody who is not a member")
	}
}

// An admin may manage members. Only the owner may hand the organization away.
func TestOnlyTheOwnerMayTransfer(t *testing.T) {
	svc, org := seedOrg(t, "usr_owner")
	ctx := context.Background()
	join(t, svc, org.ID, "usr_admin", RoleAdmin)
	if err := svc.Transfer(ctx, org.ID, "usr_admin", "usr_admin"); err == nil {
		t.Fatal("an admin transferred ownership")
	}
}

// A member must not be able to promote themselves out of the role they were
// given — the actor's floor is checked, not the target's.
func TestAMemberCannotChangeRoles(t *testing.T) {
	svc, org := seedOrg(t, "usr_owner")
	ctx := context.Background()
	join(t, svc, org.ID, "usr_2", RoleMember)
	if err := svc.SetRole(ctx, org.ID, "usr_2", "usr_2", RoleAdmin); err == nil {
		t.Fatal("a member promoted themselves")
	}
}

// The stored row must not be usable to accept the invitation it describes.
func TestInvitationStoresOnlyTheHash(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, fixedClock)
	org, _ := svc.Create(context.Background(), "usr_owner", "CTech")

	token, err := svc.Invite(context.Background(), org.ID, "usr_owner", "novo@example.com", RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("no token to send to the invitee")
	}
	for _, byEmail := range repo.invitations {
		for _, inv := range byEmail {
			if inv.TokenHash == token {
				t.Fatal("the token itself was stored")
			}
			if inv.TokenHash == "" {
				t.Fatal("no hash was stored, so nothing can be accepted")
			}
		}
	}
}

// An invitation is an offer to one address, not a bearer capability to join any
// organization.
func TestAcceptRequiresTheInvitedAddress(t *testing.T) {
	svc, org := seedOrg(t, "usr_owner")
	ctx := context.Background()
	token, _ := svc.Invite(ctx, org.ID, "usr_owner", "convidado@example.com", RoleMember)

	if _, err := svc.Accept(ctx, token, "usr_outro", "outro@example.com"); err == nil {
		t.Fatal("accepted with an address the invitation was not sent to")
	}
	m, err := svc.Accept(ctx, token, "usr_convidado", "Convidado@Example.com")
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if m.Role != RoleMember {
		t.Fatalf("role = %q, want the invited role", m.Role)
	}
}

// A token that worked once must not work twice: the second use would re-add
// somebody who was removed.
func TestAcceptConsumesTheInvitation(t *testing.T) {
	svc, org := seedOrg(t, "usr_owner")
	ctx := context.Background()
	token, _ := svc.Invite(ctx, org.ID, "usr_owner", "convidado@example.com", RoleMember)
	if _, err := svc.Accept(ctx, token, "usr_c", "convidado@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Accept(ctx, token, "usr_c", "convidado@example.com"); err == nil {
		t.Fatal("the same invitation was accepted twice")
	}
}

// An expired invitation is not a slow yes. The TTL reaps the row eventually,
// but eventually is not a guarantee the check can rest on.
func TestAcceptRefusesAnExpiredInvitation(t *testing.T) {
	repo := newFakeRepo()
	clock := fixedClock()
	svc := NewService(repo, func() time.Time { return clock })
	org, _ := svc.Create(context.Background(), "usr_owner", "CTech")
	token, _ := svc.Invite(context.Background(), org.ID, "usr_owner", "c@example.com", RoleMember)

	clock = clock.Add(invitationTTL + time.Hour)
	if _, err := svc.Accept(context.Background(), token, "usr_c", "c@example.com"); err == nil {
		t.Fatal("accepted an expired invitation")
	}
}

func TestInviteRefusesOwner(t *testing.T) {
	svc, org := seedOrg(t, "usr_owner")
	if _, err := svc.Invite(context.Background(), org.ID, "usr_owner", "x@example.com", RoleOwner); err == nil {
		t.Fatal("invited somebody as owner")
	}
}

// A viewer inviting people is a viewer who can grow the organization.
func TestInviteRequiresAdmin(t *testing.T) {
	svc, org := seedOrg(t, "usr_owner")
	join(t, svc, org.ID, "usr_viewer", RoleViewer)
	if _, err := svc.Invite(context.Background(), org.ID, "usr_viewer", "x@example.com", RoleMember); err == nil {
		t.Fatal("a viewer invited somebody")
	}
}

// The list a person sees has to carry the name: a switcher showing three ids is
// a switcher nobody can use, and making the client fetch each organization to
// find out turns one sign-in into N requests.
func TestListWorkspacesCarriesTheName(t *testing.T) {
	svc := NewService(newFakeRepo(), fixedClock)
	ctx := context.Background()
	created, _ := svc.Create(ctx, "usr_1", "CTech")

	got, err := svc.ListWorkspaces(ctx, "usr_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("workspaces = %d, want 1", len(got))
	}
	if got[0].DisplayName != "CTech" {
		t.Fatalf("display_name = %q, want CTech", got[0].DisplayName)
	}
	if got[0].ID != created.ID || got[0].Role != RoleOwner {
		t.Fatalf("workspace = %+v", got[0])
	}
}

// A membership whose organization is gone is not shown. Rendering a row that
// leads to a 403 is worse than not rendering it.
func TestListWorkspacesSkipsAMembershipWithNoOrganization(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, fixedClock)
	ctx := context.Background()
	if err := repo.PutMembership(ctx, &Membership{
		OrganizationID: "org_gone", UserID: "usr_1", Role: RoleMember, CreatedAt: fixedClock(),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.ListWorkspaces(ctx, "usr_1")
	if err != nil {
		t.Fatalf("a dangling membership must not fail the whole list: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("workspaces = %+v, want none", got)
	}
}

// Stable order, or the switcher reshuffles between renders and people click the
// wrong workspace.
func TestListWorkspacesIsOrdered(t *testing.T) {
	svc := NewService(newFakeRepo(), fixedClock)
	ctx := context.Background()
	for _, name := range []string{"Zeta", "Alfa", "Meio"} {
		if _, err := svc.Create(ctx, "usr_1", name); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := svc.ListWorkspaces(ctx, "usr_1")
	if len(got) != 3 {
		t.Fatalf("workspaces = %d", len(got))
	}
	if got[0].DisplayName != "Alfa" || got[2].DisplayName != "Zeta" {
		t.Fatalf("order = %q, %q, %q", got[0].DisplayName, got[1].DisplayName, got[2].DisplayName)
	}
}
