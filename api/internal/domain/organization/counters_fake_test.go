package organization

import (
	"context"
	"time"
)

// runRace runs the pending concurrent request once, inside the guarded write,
// between the service's check and the condition.
func (f *fakeRepo) runRace() {
	if f.beforeGuard != nil {
		hook := f.beforeGuard
		f.beforeGuard = nil
		hook()
	}
}

func counterHolds(cur Counter, g CounterWrite) bool {
	return g.Blind || (cur.Exists == g.Expect.Exists && (!cur.Exists || cur.N == g.Expect.N))
}

func (f *fakeRepo) SpaceCounter(_ context.Context, owner string) (Counter, error) {
	return f.spaceN[owner], nil
}
func (f *fakeRepo) PeopleCounter(_ context.Context, orgID string) (Counter, error) {
	return f.peopleN[orgID], nil
}

func (f *fakeRepo) GetInvitation(_ context.Context, orgID, email string) (*Invitation, error) {
	inv, ok := f.invitations[orgID][NormalizeEmail(email)]
	if !ok {
		return nil, ErrNotFound
	}
	copied := *inv
	return &copied, nil
}

func (f *fakeRepo) CreateWithOwnerGuarded(ctx context.Context, org *Organization, ownerName string, g CounterWrite) error {
	f.runRace()
	if !counterHolds(f.spaceN[org.OwnerUserID], g) {
		return ErrCounterMoved
	}
	if err := f.CreateWithOwner(ctx, org, ownerName); err != nil {
		return err
	}
	f.spaceN[org.OwnerUserID] = Counter{N: g.Next, Exists: true}
	return nil
}

func (f *fakeRepo) PutInvitationGuarded(ctx context.Context, inv *Invitation, g CounterWrite, now time.Time) error {
	f.runRace()
	if cur, ok := f.invitations[inv.OrganizationID][NormalizeEmail(inv.Email)]; ok && InvitationLive(cur, now) {
		return ErrCounterMoved
	}
	if !counterHolds(f.peopleN[inv.OrganizationID], g) {
		return ErrCounterMoved
	}
	if err := f.PutInvitation(ctx, inv); err != nil {
		return err
	}
	f.peopleN[inv.OrganizationID] = Counter{N: g.Next, Exists: true}
	return nil
}

func (f *fakeRepo) TransferOwnershipGuarded(ctx context.Context, orgID, from, to, demoteTo string, now time.Time, g CounterWrite) error {
	f.runRace()
	if !counterHolds(f.spaceN[to], g) {
		return ErrCounterMoved
	}
	if err := f.TransferOwnership(ctx, orgID, from, to, demoteTo, now); err != nil {
		return err
	}
	f.spaceN[to] = Counter{N: g.Next, Exists: true}
	return nil
}

func (f *fakeRepo) decrement(m map[string]Counter, key string) error {
	if f.decrementErr != nil {
		return f.decrementErr
	}
	if c := m[key]; c.Exists && c.N > 0 {
		m[key] = Counter{N: c.N - 1, Exists: true}
	}
	return nil
}

func (f *fakeRepo) DecrementSpaceCounter(_ context.Context, owner string) error {
	return f.decrement(f.spaceN, owner)
}

func (f *fakeRepo) DecrementPeopleCounter(_ context.Context, orgID string) error {
	return f.decrement(f.peopleN, orgID)
}

func (f *fakeRepo) ReconcileSpaceCounter(_ context.Context, owner string, seen Counter, real int64) error {
	if f.spaceN[owner] == seen {
		f.spaceN[owner] = Counter{N: real, Exists: true}
	}
	return nil
}

var _ CounterRepository = (*fakeRepo)(nil)
