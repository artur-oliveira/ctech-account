package middleware

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/domain/organization"
)

// orgRepoStub answers GetMembership from a fixed table and nothing else. The
// middleware only reads a role, so every other method is a compile-time
// obligation, not behaviour under test.
type orgRepoStub struct{ roles map[string]string } // orgID+"/"+userID -> role

func (s orgRepoStub) GetMembership(_ context.Context, orgID, userID string) (*organization.Membership, error) {
	role, ok := s.roles[orgID+"/"+userID]
	if !ok {
		return nil, organization.ErrNotFound
	}
	return &organization.Membership{OrganizationID: orgID, UserID: userID, Role: role}, nil
}

func (orgRepoStub) CreateWithOwner(context.Context, *organization.Organization, string) error {
	return nil
}
func (orgRepoStub) RenameMember(context.Context, string, string) error { return nil }
func (orgRepoStub) Get(context.Context, string) (*organization.Organization, error) {
	return nil, organization.ErrNotFound
}
func (orgRepoStub) GetBySourceRef(context.Context, string, string) (*organization.Organization, error) {
	return nil, organization.ErrNotFound
}
func (orgRepoStub) UpdateDisplayName(context.Context, string, string, time.Time) error { return nil }
func (orgRepoStub) ListMembers(context.Context, string) ([]*organization.Membership, error) {
	return nil, nil
}
func (orgRepoStub) ListForUser(context.Context, string) ([]*organization.Membership, error) {
	return nil, nil
}
func (orgRepoStub) PutMembership(context.Context, *organization.Membership) error { return nil }
func (orgRepoStub) SetRole(context.Context, string, string, string) error         { return nil }
func (orgRepoStub) RemoveMembership(context.Context, string, string) error        { return nil }
func (orgRepoStub) TransferOwnership(context.Context, string, string, string, time.Time) error {
	return nil
}
func (orgRepoStub) PutInvitation(context.Context, *organization.Invitation) error { return nil }
func (orgRepoStub) GetInvitationByToken(context.Context, string) (*organization.Invitation, error) {
	return nil, organization.ErrNotFound
}
func (orgRepoStub) ListInvitations(context.Context, string) ([]*organization.Invitation, error) {
	return nil, nil
}
func (orgRepoStub) DeleteInvitation(context.Context, string, string) error { return nil }
func (orgRepoStub) AcceptInvitation(context.Context, *organization.Membership, string) error {
	return nil
}

func detailOf(t *testing.T, resp *http.Response) string {
	t.Helper()
	body, _ := io.ReadAll(resp.Body)
	var problem struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(body, &problem); err != nil {
		t.Fatalf("decoding problem: %v (%s)", err, body)
	}
	return problem.Detail
}

func orgApp(t *testing.T, roles map[string]string, floor, userID string) *fiber.App {
	t.Helper()
	svc := organization.NewService(orgRepoStub{roles: roles}, time.Now)
	app := fiber.New()
	app.Get("/orgs/:id/x",
		func(c fiber.Ctx) error { c.Locals(LocalUserID, userID); return c.Next() },
		RequireOrgRole(svc, floor),
		func(c fiber.Ctx) error { return c.SendString(GetOrgRole(c) + "|" + GetOrgID(c)) },
	)
	return app
}

// The role comes from the membership row on every request. A role in a token
// survives its own revocation for the token's lifetime, and removing somebody
// has to take effect on the next request.
func TestRequireOrgRoleReadsTheMembershipEveryTime(t *testing.T) {
	t.Parallel()
	roles := map[string]string{
		"org_1/usr_viewer": organization.RoleViewer,
		"org_1/usr_admin":  organization.RoleAdmin,
	}
	for _, tt := range []struct {
		name, user, floor string
		want              int
	}{
		{"viewer below the admin floor", "usr_viewer", organization.RoleAdmin, http.StatusForbidden},
		{"admin clears the admin floor", "usr_admin", organization.RoleAdmin, http.StatusOK},
		{"admin clears a lower floor", "usr_admin", organization.RoleViewer, http.StatusOK},
		{"a stranger clears nothing", "usr_stranger", organization.RoleViewer, http.StatusForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app := orgApp(t, roles, tt.floor, tt.user)
			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/orgs/org_1/x", nil))
			if err != nil {
				t.Fatalf("app.Test: %v", err)
			}
			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}
}

// The handler downstream reads the role the middleware resolved rather than
// asking again, so it has to be there and it has to be right.
func TestRequireOrgRolePublishesTheResolvedRole(t *testing.T) {
	t.Parallel()
	app := orgApp(t, map[string]string{"org_1/usr_admin": organization.RoleAdmin}, organization.RoleViewer, "usr_admin")
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/orgs/org_1/x", nil))
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != organization.RoleAdmin+"|org_1" {
		t.Fatalf("locals = %q, want %q", body, organization.RoleAdmin+"|org_1")
	}
}

// A non-member and a missing organization answer the same thing: telling
// somebody an organization exists but is not theirs is telling them something.
func TestUnknownOrganizationAndNonMemberAnswerAlike(t *testing.T) {
	t.Parallel()
	app := orgApp(t, map[string]string{"org_1/usr_admin": organization.RoleAdmin}, organization.RoleViewer, "usr_outsider")

	nonMember, err := app.Test(httptest.NewRequest(http.MethodGet, "/orgs/org_1/x", nil))
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	unknown, err := app.Test(httptest.NewRequest(http.MethodGet, "/orgs/org_missing/x", nil))
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if nonMember.StatusCode != unknown.StatusCode {
		t.Fatalf("status %d vs %d — the difference says whether the organization exists", nonMember.StatusCode, unknown.StatusCode)
	}
	// Everything but "instance" must match. instance echoes the caller's own
	// path, which they already knew; "detail" is the field that would say
	// whether the organization exists.
	if detailOf(t, nonMember) != detailOf(t, unknown) {
		t.Fatalf("details differ: %q vs %q", detailOf(t, nonMember), detailOf(t, unknown))
	}
}
