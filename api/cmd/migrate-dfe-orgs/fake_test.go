package main

import (
	"context"
	"time"

	orgDomain "gopkg.aoctech.app/account/api/internal/domain/organization"
)

// fakeRepo is a map-backed orgDomain.Repository holding the two conditions
// apply depends on: an organization is found by its source ref, and a
// membership is written only if absent.
type fakeRepo struct {
	orgs        map[string]*orgDomain.Organization
	memberships map[string]map[string]*orgDomain.Membership
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		orgs:        map[string]*orgDomain.Organization{},
		memberships: map[string]map[string]*orgDomain.Membership{},
	}
}

func (f *fakeRepo) CreateWithOwner(_ context.Context, org *orgDomain.Organization, ownerName string) error {
	if _, exists := f.orgs[org.ID]; exists {
		return orgDomain.ErrAlreadyMember
	}
	copied := *org
	f.orgs[org.ID] = &copied
	f.memberships[org.ID] = map[string]*orgDomain.Membership{
		org.OwnerUserID: {OrganizationID: org.ID, UserID: org.OwnerUserID, Name: ownerName, Role: orgDomain.RoleOwner, CreatedAt: org.CreatedAt},
	}
	return nil
}

func (f *fakeRepo) GetBySourceRef(_ context.Context, system, ref string) (*orgDomain.Organization, error) {
	for _, org := range f.orgs {
		if org.SourceSystem == system && org.SourceRef == ref && ref != "" {
			copied := *org
			return &copied, nil
		}
	}
	return nil, orgDomain.ErrNotFound
}

func (f *fakeRepo) PutMembership(_ context.Context, m *orgDomain.Membership) error {
	if f.memberships[m.OrganizationID] == nil {
		f.memberships[m.OrganizationID] = map[string]*orgDomain.Membership{}
	}
	if _, exists := f.memberships[m.OrganizationID][m.UserID]; exists {
		return orgDomain.ErrAlreadyMember
	}
	copied := *m
	f.memberships[m.OrganizationID][m.UserID] = &copied
	return nil
}

// The migration reads and writes nothing else. These exist to satisfy the
// interface, and each returns the answer that makes a misuse loud.
func (f *fakeRepo) RenameMember(context.Context, string, string) error { return nil }

func (f *fakeRepo) Get(context.Context, string) (*orgDomain.Organization, error) {
	return nil, orgDomain.ErrNotFound
}
func (f *fakeRepo) UpdateDisplayName(context.Context, string, string, time.Time) error { return nil }
func (f *fakeRepo) GetMembership(context.Context, string, string) (*orgDomain.Membership, error) {
	return nil, orgDomain.ErrNotFound
}
func (f *fakeRepo) ListMembers(context.Context, string) ([]*orgDomain.Membership, error) {
	return nil, nil
}
func (f *fakeRepo) ListForUser(context.Context, string) ([]*orgDomain.Membership, error) {
	return nil, nil
}
func (f *fakeRepo) SetRole(context.Context, string, string, string) error  { return nil }
func (f *fakeRepo) RemoveMembership(context.Context, string, string) error { return nil }
func (f *fakeRepo) TransferOwnership(context.Context, string, string, string, time.Time) error {
	return nil
}
func (f *fakeRepo) PutInvitation(context.Context, *orgDomain.Invitation) error { return nil }
func (f *fakeRepo) GetInvitationByToken(context.Context, string) (*orgDomain.Invitation, error) {
	return nil, orgDomain.ErrNotFound
}
func (f *fakeRepo) ListInvitations(context.Context, string) ([]*orgDomain.Invitation, error) {
	return nil, nil
}
func (f *fakeRepo) DeleteInvitation(context.Context, string, string) error { return nil }
func (f *fakeRepo) AcceptInvitation(context.Context, *orgDomain.Membership, string) error {
	return nil
}
