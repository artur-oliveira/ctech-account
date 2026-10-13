package deletion

import (
	"context"
	"testing"
	"time"

	"gopkg.aoctech.app/account/api/internal/domain/company"
	"gopkg.aoctech.app/account/api/internal/domain/mfa/passkey"
	"gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	"gopkg.aoctech.app/account/api/internal/domain/oauth/consent"
	"gopkg.aoctech.app/account/api/internal/domain/organization"
)

type fakeOrgs struct {
	memberships map[string][]*organization.Membership // by user
	members     map[string][]*organization.Membership // by org
	kinds       map[string]string
	removed     []string
	log         *[]string
}

func (f *fakeOrgs) ListForUser(_ context.Context, userID string) ([]*organization.Membership, error) {
	return f.memberships[userID], nil
}
func (f *fakeOrgs) ListMembers(_ context.Context, orgID, _ string) ([]*organization.Membership, error) {
	return f.members[orgID], nil
}
func (f *fakeOrgs) KindOf(_ context.Context, orgID string) (string, error) {
	if k, ok := f.kinds[orgID]; ok {
		return k, nil
	}
	return organization.KindOrganization, nil
}
func (f *fakeOrgs) Remove(_ context.Context, orgID, _, _ string) error {
	f.removed = append(f.removed, orgID)
	*f.log = append(*f.log, "membership")
	return nil
}

type fakeClients struct{ owned []*client.OAuthClient }

func (f fakeClients) ListByOwner(context.Context, string) ([]*client.OAuthClient, error) {
	return f.owned, nil
}

func m(org, user, role string) *organization.Membership {
	return &organization.Membership{OrganizationID: org, UserID: user, Role: role}
}

func TestAccountOwnership_Assess(t *testing.T) {
	orgs := &fakeOrgs{
		memberships: map[string][]*organization.Membership{"u1": {
			m("solo", "u1", organization.RoleOwner), m("space", "u1", organization.RoleOwner),
			m("team", "u1", organization.RoleOwner), m("other", "u1", organization.RoleMember),
		}},
		members: map[string][]*organization.Membership{
			"solo":  {m("solo", "u1", organization.RoleOwner)},
			"space": {m("space", "u1", organization.RoleOwner)},
			"team":  {m("team", "u1", organization.RoleOwner), m("team", "u2", organization.RoleMember)},
		},
		kinds: map[string]string{"space": organization.KindPersonal},
		log:   &[]string{},
	}
	clients := fakeClients{owned: []*client.OAuthClient{
		{PK: client.BuildPK("dev-app")},
		{PK: client.BuildPK("platform"), FirstParty: true},
	}}
	sole, blockers, err := NewAccountOwnership(orgs, clients).Assess(context.Background(), "u1")
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if len(sole) != 2 || sole[0] != (Workspace{ID: "solo", Kind: "organization"}) || sole[1] != (Workspace{ID: "space", Kind: "personal"}) {
		t.Fatalf("sole = %v, want solo (organization) and space (personal)", sole)
	}
	if len(blockers) != 2 || blockers[0].Code != BlockerSharedOrganization || blockers[0].Detail["organization_id"] != "team" ||
		blockers[1].Code != BlockerOwnedClient || blockers[1].Detail["client_id"] != "dev-app" {
		t.Fatalf("blockers = %+v", blockers)
	}
}

type recCompanies struct{ log *[]string }

func (r recCompanies) ListForUser(context.Context, string) ([]*company.Actor, error) {
	return []*company.Actor{{OrganizationID: "other", CompanyID: "c1", UserID: "u1"}}, nil
}
func (r recCompanies) RemoveActor(context.Context, string, string, string) error {
	*r.log = append(*r.log, "company")
	return nil
}

type recConsents struct{ log *[]string }

func (r recConsents) List(context.Context, string) ([]*consent.Grant, error) {
	return []*consent.Grant{{PK: consent.BuildPK("u1"), SK: consent.BuildSK("dfe")}}, nil
}
func (r recConsents) Revoke(context.Context, string, string) error {
	*r.log = append(*r.log, "consent")
	return nil
}

type recSessions struct{ log *[]string }

func (r recSessions) RevokeAll(context.Context, string, string) error {
	*r.log = append(*r.log, "sessions")
	return nil
}

type recTOTP struct{ log *[]string }

func (r recTOTP) Remove(context.Context, string) error { *r.log = append(*r.log, "totp"); return nil }

type recPasskeys struct{ log *[]string }

func (r recPasskeys) List(context.Context, string) ([]*passkey.Credential, error) {
	return []*passkey.Credential{{SK: "PASSKEY_ab"}}, nil
}
func (r recPasskeys) Delete(context.Context, string, string) error {
	*r.log = append(*r.log, "passkey")
	return nil
}

func TestAccountPurger_RunsEveryStepThenTombstone(t *testing.T) {
	var log []string
	orgs := &fakeOrgs{
		memberships: map[string][]*organization.Membership{"u1": {
			m("solo", "u1", organization.RoleOwner), m("other", "u1", organization.RoleMember),
		}},
		log: &log,
	}
	keys := &fakeKeys{}
	var purgedFor string
	purgeUser := func(_ context.Context, userID, requestID string, _ time.Time) error {
		log = append(log, "tombstone")
		purgedFor = userID + "/" + requestID
		return nil
	}
	p := NewAccountPurger(orgs, recCompanies{&log}, recConsents{&log}, recSessions{&log}, keys, recTOTP{&log}, recPasskeys{&log}, purgeUser)
	r := &Request{ID: "r1", UserID: "u1", Organizations: []string{"solo"}}
	for i := 0; i < 2; i++ { // idempotent: a second run must also succeed
		log = nil
		if err := p.Purge(context.Background(), r); err != nil {
			t.Fatalf("Purge run %d: %v", i+1, err)
		}
	}
	want := []string{"membership", "company", "consent", "sessions", "totp", "passkey", "tombstone"}
	if len(log) != len(want) {
		t.Fatalf("steps = %v, want %v", log, want)
	}
	for i := range want {
		if log[i] != want[i] {
			t.Fatalf("steps = %v, want %v (tombstone must be last)", log, want)
		}
	}
	if len(orgs.removed) == 0 || orgs.removed[len(orgs.removed)-1] != "other" || purgedFor != "u1/r1" {
		t.Fatalf("owner membership must be skipped (R6); removed=%v purgedFor=%q", orgs.removed, purgedFor)
	}
}
