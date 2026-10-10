package organization

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type fakeLimits struct {
	quotas    map[string]Quotas
	err       error
	calls     int
	changed   []string
	scheduled []time.Time
}

var free = Quotas{Plan: "free", Spaces: 1, PeoplePerSpace: 1}
var basic = Quotas{Plan: "basic", Spaces: 3, PeoplePerSpace: 5}

func (f *fakeLimits) Quotas(_ context.Context, owner string) (Quotas, error) {
	f.calls++
	if f.err != nil {
		return Quotas{}, f.err
	}
	if q, ok := f.quotas[owner]; ok {
		return q, nil
	}
	return free, nil
}
func (f *fakeLimits) LevelsChanged(_ context.Context, owner string) {
	f.changed = append(f.changed, owner)
}
func (f *fakeLimits) ScheduleLevels(_ context.Context, _ string, at time.Time) {
	f.scheduled = append(f.scheduled, at)
}

func limitedService(t *testing.T, quotas map[string]Quotas) (*fakeRepo, *Service, *fakeLimits) {
	t.Helper()
	repo := newFakeRepo()
	limits := &fakeLimits{quotas: quotas}
	svc := NewService(repo, fixedClock).WithPlanLimits(limits, repo)
	return repo, svc, limits
}

func mustCreateSpace(t *testing.T, svc *Service, owner, name string) *Organization {
	t.Helper()
	space, err := svc.CreateOfKind(context.Background(), KindPersonal, owner, "Dono", name)
	if err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}
	return space
}

func planLimit(t *testing.T, err error) *PlanLimitError {
	t.Helper()
	var le *PlanLimitError
	if !errors.As(err, &le) {
		t.Fatalf("err = %v, want a PlanLimitError", err)
	}
	return le
}

// Spec test 1.
func TestFreeAllowsOneSpaceAndRefusesTheSecond(t *testing.T) {
	repo, svc, _ := limitedService(t, nil)
	mustCreateSpace(t, svc, "usr_owner", "Casa")
	_, err := svc.CreateOfKind(context.Background(), KindPersonal, "usr_owner", "Dono", "Viagem")
	le := planLimit(t, err)
	if le.Resource != ResourceSpaces || le.Limit != 1 || le.Used != 1 || le.Plan != "free" || le.Hidden {
		t.Fatalf("refusal = %+v", le)
	}
	if len(repo.orgs) != 1 {
		t.Fatalf("%d workspaces written, want 1", len(repo.orgs))
	}
}

// Organizations are not counted and never ask billing.
func TestOrganizationsAreNeitherCountedNorChecked(t *testing.T) {
	_, svc, limits := limitedService(t, nil)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := svc.Create(ctx, "usr_owner", "Dono", fmt.Sprintf("Empresa %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	mustCreateSpace(t, svc, "usr_owner", "Casa")
	if limits.calls != 1 {
		t.Fatalf("billing read %d times, want once (the space only)", limits.calls)
	}
}

// Spec test 2, spaces half.
func TestBasicRefusesTheFourthSpace(t *testing.T) {
	_, svc, _ := limitedService(t, map[string]Quotas{"usr_owner": basic})
	for _, name := range []string{"A", "B", "C"} {
		mustCreateSpace(t, svc, "usr_owner", name)
	}
	_, err := svc.CreateOfKind(context.Background(), KindPersonal, "usr_owner", "Dono", "D")
	if le := planLimit(t, err); le.Used != 3 || le.Limit != 3 {
		t.Fatalf("refusal = %+v", le)
	}
}

// Spec test 2, people half: pending invitations count, the owner does not.
func TestTheSixthPersonIsRefusedCountingPendingInvitations(t *testing.T) {
	_, svc, _ := limitedService(t, map[string]Quotas{"usr_owner": basic})
	ctx := context.Background()
	space := mustCreateSpace(t, svc, "usr_owner", "Casa")
	join(t, svc, space.ID, "usr_a", RoleMember)
	join(t, svc, space.ID, "usr_b", RoleViewer)
	for _, e := range []string{"c@example.com", "d@example.com", "e@example.com"} {
		if _, err := svc.Invite(ctx, space.ID, "usr_owner", e, RoleMember, nil); err != nil {
			t.Fatalf("inviting %s: %v", e, err)
		}
	}
	_, err := svc.Invite(ctx, space.ID, "usr_owner", "f@example.com", RoleMember, nil)
	if le := planLimit(t, err); le.Resource != ResourcePeople || le.Used != 5 || le.Limit != 5 {
		t.Fatalf("refusal = %+v", le)
	}
}

// Spec test 3 through the invite path.
func TestAnExpiredInvitationFreesItsPlace(t *testing.T) {
	repo := newFakeRepo()
	now := fixedClock()
	limits := &fakeLimits{}
	svc := NewService(repo, func() time.Time { return now }).WithPlanLimits(limits, repo)
	ctx := context.Background()
	space := mustCreateSpace(t, svc, "usr_owner", "Casa")
	if _, err := svc.Invite(ctx, space.ID, "usr_owner", "a@example.com", RoleMember, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Invite(ctx, space.ID, "usr_owner", "b@example.com", RoleMember, nil); err == nil {
		t.Fatal("Free let a second person in")
	}
	now = now.Add(invitationTTL + time.Second) // a@ expired, row still there
	if _, err := svc.Invite(ctx, space.ID, "usr_owner", "b@example.com", RoleMember, nil); err != nil {
		t.Fatalf("the expired invitation still holds the place: %v", err)
	}
}

// Review Focus 2.
func TestReinvitingAPendingAddressAtTheLimitSucceeds(t *testing.T) {
	repo, svc, _ := limitedService(t, nil)
	ctx := context.Background()
	space := mustCreateSpace(t, svc, "usr_owner", "Casa")
	if _, err := svc.Invite(ctx, space.ID, "usr_owner", "a@example.com", RoleMember, nil); err != nil {
		t.Fatal(err)
	}
	before := repo.peopleN[space.ID]
	if _, err := svc.Invite(ctx, space.ID, "usr_owner", " A@Example.com", RoleViewer, nil); err != nil {
		t.Fatalf("re-inviting the pending address was refused: %v", err)
	}
	if repo.peopleN[space.ID] != before {
		t.Fatalf("people_n moved on a re-invite: %+v → %+v", before, repo.peopleN[space.ID])
	}
}

// Spec test 4.
func TestTwoConcurrentCreationsAtTheLimitLetExactlyOneThrough(t *testing.T) {
	repo, svc, _ := limitedService(t, map[string]Quotas{"usr_owner": basic})
	ctx := context.Background()
	mustCreateSpace(t, svc, "usr_owner", "A")
	mustCreateSpace(t, svc, "usr_owner", "B")
	var concurrentErr error
	repo.beforeGuard = func() {
		_, concurrentErr = svc.CreateOfKind(ctx, KindPersonal, "usr_owner", "Dono", "Outra aba")
	}
	_, err := svc.CreateOfKind(ctx, KindPersonal, "usr_owner", "Dono", "Esta aba")
	if concurrentErr != nil {
		t.Fatalf("the first writer failed: %v", concurrentErr)
	}
	planLimit(t, err)
	if len(repo.orgs) != 3 {
		t.Fatalf("%d spaces, want exactly 3", len(repo.orgs))
	}
}

func TestAGuardThatKeepsMovingAnswersBusy(t *testing.T) {
	repo, svc, _ := limitedService(t, map[string]Quotas{"usr_owner": {Plan: "ondemand", Spaces: 100, PeoplePerSpace: 100}})
	var bump func()
	bump = func() {
		c := repo.spaceN["usr_owner"]
		repo.spaceN["usr_owner"] = Counter{N: c.N + 1, Exists: true}
		repo.beforeGuard = bump
	}
	repo.beforeGuard = bump
	_, err := svc.CreateOfKind(context.Background(), KindPersonal, "usr_owner", "Dono", "Casa")
	if !errors.Is(err, ErrPlanBusy) {
		t.Fatalf("err = %v, want ErrPlanBusy", err)
	}
	repo.beforeGuard = nil
}

// Spec test 5 (domain half): nothing written; organizations unaffected.
func TestBillingDownWritesNothing(t *testing.T) {
	repo, svc, limits := limitedService(t, nil)
	limits.err = fmt.Errorf("%w: timeout", ErrPlanUnavailable)
	ctx := context.Background()
	if _, err := svc.CreateOfKind(ctx, KindPersonal, "usr_owner", "Dono", "Casa"); !errors.Is(err, ErrPlanUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if len(repo.orgs) != 0 || len(limits.changed) != 0 {
		t.Fatal("a space was written or reported with billing down")
	}
	if _, err := svc.Create(ctx, "usr_owner", "Dono", "CTech"); err != nil {
		t.Fatalf("an organization was refused with billing down: %v", err)
	}
}

// Spec test 6 / Review Focus 1: over the limit after a downgrade, everything
// that exists keeps working; only growth is refused.
func TestOverTheLimitNothingIsTakenAway(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, fixedClock)
	ctx := context.Background()
	var spaces []*Organization
	for _, name := range []string{"A", "B", "C"} {
		s, _ := svc.CreateOfKind(ctx, KindPersonal, "usr_owner", "Dono", name)
		spaces = append(spaces, s)
	}
	join(t, svc, spaces[0].ID, "usr_a", RoleMember)
	join(t, svc, spaces[0].ID, "usr_b", RoleMember)
	_, _ = svc.Invite(ctx, spaces[0].ID, "usr_owner", "c@example.com", RoleMember, nil)

	limits := &fakeLimits{} // downgraded to Free
	svc.WithPlanLimits(limits, repo)

	if err := svc.SetRole(ctx, spaces[0].ID, "usr_owner", "usr_a", RoleViewer); err != nil {
		t.Fatalf("set role: %v", err)
	}
	if err := svc.Remove(ctx, spaces[0].ID, "usr_owner", "usr_a"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := svc.Remove(ctx, spaces[0].ID, "usr_b", "usr_b"); err != nil {
		t.Fatalf("leave: %v", err)
	}
	if err := svc.RevokeInvitation(ctx, spaces[0].ID, "usr_owner", "c@example.com"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if limits.calls != 0 {
		t.Fatalf("shrinking read billing %d times", limits.calls)
	}
	if _, err := svc.Invite(ctx, spaces[1].ID, "usr_owner", "d@example.com", RoleMember, nil); err != nil {
		t.Fatalf("Free allows one person per space: %v", err)
	}
	if _, err := svc.CreateOfKind(ctx, KindPersonal, "usr_owner", "Dono", "D"); err == nil {
		t.Fatal("a fourth space was created over the limit")
	}
	if len(repo.orgs) != 3 {
		t.Fatalf("spaces = %d, want the three that existed", len(repo.orgs))
	}
}

// Review Focus 1, billing down: shrinking never waits on billing.
func TestBillingDownNeverBlocksShrinking(t *testing.T) {
	repo, svc, limits := limitedService(t, map[string]Quotas{"usr_owner": basic})
	ctx := context.Background()
	space := mustCreateSpace(t, svc, "usr_owner", "Casa")
	join(t, svc, space.ID, "usr_a", RoleMember)
	token, _ := svc.Invite(ctx, space.ID, "usr_owner", "b@example.com", RoleMember, nil)
	limits.err = fmt.Errorf("%w: down", ErrPlanUnavailable)
	if err := svc.Remove(ctx, space.ID, "usr_owner", "usr_a"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := svc.Accept(ctx, token, "usr_b", "b@example.com", "Bia"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	_ = repo
}

// Spec test 7.
func TestTransferToSomebodyAtTheirLimitIsRefused(t *testing.T) {
	repo, svc, _ := limitedService(t, map[string]Quotas{"usr_owner": basic})
	ctx := context.Background()
	space := mustCreateSpace(t, svc, "usr_owner", "Casa")
	mustCreateSpace(t, svc, "usr_to", "Dele") // usr_to is on Free with one space
	join(t, svc, space.ID, "usr_to", RoleMember)

	le := planLimit(t, svc.Transfer(ctx, space.ID, "usr_owner", "usr_to"))
	if !le.Hidden || le.Resource != ResourceSpaces {
		t.Fatalf("refusal = %+v, want hidden spaces", le)
	}
	if repo.orgs[space.ID].OwnerUserID != "usr_owner" {
		t.Fatal("the space moved")
	}
}

func TestATransferMovesTheCountersAndReportsBoth(t *testing.T) {
	repo, svc, limits := limitedService(t, map[string]Quotas{"usr_owner": basic, "usr_to": basic})
	ctx := context.Background()
	space := mustCreateSpace(t, svc, "usr_owner", "Casa")
	join(t, svc, space.ID, "usr_to", RoleMember)
	limits.changed = nil
	if err := svc.Transfer(ctx, space.ID, "usr_owner", "usr_to"); err != nil {
		t.Fatal(err)
	}
	if repo.spaceN["usr_owner"].N != 0 || repo.spaceN["usr_to"].N != 1 {
		t.Fatalf("counters: from %+v to %+v", repo.spaceN["usr_owner"], repo.spaceN["usr_to"])
	}
	if fmt.Sprint(limits.changed) != "[usr_owner usr_to]" {
		t.Fatalf("reported %v", limits.changed)
	}
}

func TestAFailedDecrementSchedulesAReconcile(t *testing.T) {
	repo, svc, limits := limitedService(t, map[string]Quotas{"usr_owner": basic, "usr_to": basic})
	ctx := context.Background()
	space := mustCreateSpace(t, svc, "usr_owner", "Casa")
	join(t, svc, space.ID, "usr_to", RoleMember)
	repo.decrementErr = errors.New("throttled")
	limits.scheduled = nil
	if err := svc.Transfer(ctx, space.ID, "usr_owner", "usr_to"); err != nil {
		t.Fatalf("a failed decrement failed the transfer: %v", err)
	}
	if len(limits.scheduled) != 1 || !limits.scheduled[0].Equal(fixedClock().Add(counterSettle)) {
		t.Fatalf("scheduled = %v", limits.scheduled)
	}
}

// Spec test 8 (domain half): every change marks the owner.
func TestEveryChangeMarksTheLevels(t *testing.T) {
	_, svc, limits := limitedService(t, map[string]Quotas{"usr_owner": basic})
	ctx := context.Background()
	space := mustCreateSpace(t, svc, "usr_owner", "Casa")
	join(t, svc, space.ID, "usr_a", RoleMember)
	token, _ := svc.Invite(ctx, space.ID, "usr_owner", "b@example.com", RoleMember, nil)
	_, _ = svc.Invite(ctx, space.ID, "usr_owner", "c@example.com", RoleMember, nil)
	_ = svc.RevokeInvitation(ctx, space.ID, "usr_owner", "c@example.com")
	_, _ = svc.Accept(ctx, token, "usr_b", "b@example.com", "Bia")
	_ = svc.Remove(ctx, space.ID, "usr_owner", "usr_a")
	_ = svc.Remove(ctx, space.ID, "usr_b", "usr_b")
	// create, invite, invite, revoke, accept, remove, leave
	if len(limits.changed) != 7 {
		t.Fatalf("changes reported = %d (%v), want 7", len(limits.changed), limits.changed)
	}
	want := fixedClock().Add(invitationTTL + time.Second)
	if len(limits.scheduled) != 2 || !limits.scheduled[0].Equal(want) {
		t.Fatalf("expiry reports = %v, want two at %v", limits.scheduled, want)
	}
}

func TestUnlimitedStillMaintainsTheCounter(t *testing.T) {
	repo, svc, _ := limitedService(t, map[string]Quotas{"usr_owner": {Plan: "ondemand", Spaces: Unlimited, PeoplePerSpace: Unlimited}})
	for i := 0; i < 12; i++ {
		mustCreateSpace(t, svc, "usr_owner", fmt.Sprintf("S%d", i))
	}
	if repo.spaceN["usr_owner"].N != 12 {
		t.Fatalf("counter = %+v", repo.spaceN["usr_owner"])
	}
}

// A counter written before limits (absent) or lower than the truth is set to
// the real count at the next check.
func TestALowCounterIsCorrectedAtTheCheck(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, fixedClock)
	mustCreateSpace(t, svc, "usr_owner", "A")
	mustCreateSpace(t, svc, "usr_owner", "B")
	svc.WithPlanLimits(&fakeLimits{quotas: map[string]Quotas{"usr_owner": basic}}, repo)
	mustCreateSpace(t, svc, "usr_owner", "C")
	if repo.spaceN["usr_owner"].N != 3 {
		t.Fatalf("counter = %+v, want 3", repo.spaceN["usr_owner"])
	}
}

func TestReconcileLowersAHighCounter(t *testing.T) {
	repo, svc, _ := limitedService(t, map[string]Quotas{"usr_owner": basic})
	mustCreateSpace(t, svc, "usr_owner", "A")
	repo.spaceN["usr_owner"] = Counter{N: 3, Exists: true} // a lost decrement
	if err := svc.ReconcileSpaceCounter(context.Background(), "usr_owner"); err != nil {
		t.Fatal(err)
	}
	if repo.spaceN["usr_owner"].N != 1 {
		t.Fatalf("counter = %+v, want 1", repo.spaceN["usr_owner"])
	}
}

func TestSpaceUsageIsTheOwnersCounter(t *testing.T) {
	repo, svc, _ := limitedService(t, map[string]Quotas{"usr_owner": basic})
	ctx := context.Background()
	space := mustCreateSpace(t, svc, "usr_owner", "Casa")
	join(t, svc, space.ID, "usr_a", RoleMember)
	_, _ = svc.Invite(ctx, space.ID, "usr_owner", "b@example.com", RoleMember, nil)
	u, err := svc.SpaceUsage(ctx, space.ID, "usr_owner")
	if err != nil {
		t.Fatal(err)
	}
	if u != (SpaceUsage{People: 1, PendingInvitations: 1, Limit: 5, Plan: "basic"}) {
		t.Fatalf("usage = %+v", u)
	}
	if _, err := svc.SpaceUsage(ctx, space.ID, "usr_a"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a member read the owner's plan: %v", err)
	}
	org, _ := svc.Create(ctx, "usr_owner", "Dono", "CTech")
	if _, err := svc.SpaceUsage(ctx, org.ID, "usr_owner"); !errors.Is(err, ErrNotASpace) {
		t.Fatalf("an organization: %v", err)
	}
	_ = repo
}
