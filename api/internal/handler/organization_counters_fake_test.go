package handler_test

import (
	"context"
	"time"

	orgDomain "gopkg.aoctech.app/account/api/internal/domain/organization"
)

// memOrgRepo's guarded half: the same conditions the DynamoDB guard enforces.

func memCounterHolds(cur orgDomain.Counter, g orgDomain.CounterWrite) bool {
	return g.Blind || (cur.Exists == g.Expect.Exists && (!cur.Exists || cur.N == g.Expect.N))
}

func (m *memOrgRepo) SpaceCounter(_ context.Context, owner string) (orgDomain.Counter, error) {
	return m.spaceN[owner], nil
}

func (m *memOrgRepo) PeopleCounter(_ context.Context, orgID string) (orgDomain.Counter, error) {
	return m.peopleN[orgID], nil
}

func (m *memOrgRepo) GetInvitation(_ context.Context, orgID, email string) (*orgDomain.Invitation, error) {
	inv, ok := m.invitations[orgID][orgDomain.NormalizeEmail(email)]
	if !ok {
		return nil, orgDomain.ErrNotFound
	}
	copied := *inv
	return &copied, nil
}

func (m *memOrgRepo) CreateWithOwnerGuarded(ctx context.Context, org *orgDomain.Organization, ownerName string, g orgDomain.CounterWrite) error {
	if !memCounterHolds(m.spaceN[org.OwnerUserID], g) {
		return orgDomain.ErrCounterMoved
	}
	if err := m.CreateWithOwner(ctx, org, ownerName); err != nil {
		return err
	}
	m.spaceN[org.OwnerUserID] = orgDomain.Counter{N: g.Next, Exists: true}
	return nil
}

func (m *memOrgRepo) PutInvitationGuarded(ctx context.Context, inv *orgDomain.Invitation, g orgDomain.CounterWrite, now time.Time) error {
	if cur, ok := m.invitations[inv.OrganizationID][orgDomain.NormalizeEmail(inv.Email)]; ok && orgDomain.InvitationLive(cur, now) {
		return orgDomain.ErrCounterMoved
	}
	if !memCounterHolds(m.peopleN[inv.OrganizationID], g) {
		return orgDomain.ErrCounterMoved
	}
	if err := m.PutInvitation(ctx, inv); err != nil {
		return err
	}
	m.peopleN[inv.OrganizationID] = orgDomain.Counter{N: g.Next, Exists: true}
	return nil
}

func (m *memOrgRepo) TransferOwnershipGuarded(ctx context.Context, orgID, from, to, demoteTo string, now time.Time, g orgDomain.CounterWrite) error {
	if !memCounterHolds(m.spaceN[to], g) {
		return orgDomain.ErrCounterMoved
	}
	if err := m.TransferOwnership(ctx, orgID, from, to, demoteTo, now); err != nil {
		return err
	}
	m.spaceN[to] = orgDomain.Counter{N: g.Next, Exists: true}
	return nil
}

func (m *memOrgRepo) DecrementSpaceCounter(_ context.Context, owner string) error {
	if c := m.spaceN[owner]; c.Exists && c.N > 0 {
		m.spaceN[owner] = orgDomain.Counter{N: c.N - 1, Exists: true}
	}
	return nil
}

func (m *memOrgRepo) DecrementPeopleCounter(_ context.Context, orgID string) error {
	if c := m.peopleN[orgID]; c.Exists && c.N > 0 {
		m.peopleN[orgID] = orgDomain.Counter{N: c.N - 1, Exists: true}
	}
	return nil
}

func (m *memOrgRepo) ReconcileSpaceCounter(_ context.Context, owner string, seen orgDomain.Counter, real int64) error {
	if m.spaceN[owner] == seen {
		m.spaceN[owner] = orgDomain.Counter{N: real, Exists: true}
	}
	return nil
}

var _ orgDomain.CounterRepository = (*memOrgRepo)(nil)
