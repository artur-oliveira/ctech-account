package organization

import (
	"context"
	"errors"
	"testing"
	"time"
)

// limitedRepoAndService is a fake repo and a service whose clock the test moves.
func limitedRepoAndService(t *testing.T) (*fakeRepo, *Service, *time.Time) {
	t.Helper()
	repo := newFakeRepo()
	now := fixedClock()
	svc := NewService(repo, func() time.Time { return now })
	return repo, svc, &now
}

func invite(t *testing.T, repo *fakeRepo, orgID, email string, expires time.Time) {
	t.Helper()
	if err := repo.PutInvitation(context.Background(), &Invitation{
		OrganizationID: orgID, Email: email, Role: RoleMember, TokenHash: "h-" + email, ExpiresAt: expires,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInvitationLive(t *testing.T) {
	now := fixedClock()
	if !InvitationLive(&Invitation{ExpiresAt: now}, now) {
		t.Fatal("an invitation is live up to and including its expiry instant, like Accept")
	}
	if InvitationLive(&Invitation{ExpiresAt: now.Add(-time.Second)}, now) {
		t.Fatal("an expired invitation reads as live")
	}
	if !InvitationLive(&Invitation{}, now) {
		t.Fatal("an invitation with no expiry is live")
	}
}

// Spec § 2: Pessoal is not a workspace; organizations and spaces the person is
// only a member of are not theirs.
func TestOnlyOwnedPersonalSpacesAreCounted(t *testing.T) {
	_, svc, _ := limitedRepoAndService(t)
	ctx := context.Background()
	if _, err := svc.CreateOfKind(ctx, KindPersonal, "usr_owner", "Dono", "Casa"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, "usr_owner", "Dono", "CTech"); err != nil {
		t.Fatal(err)
	}
	other, _ := svc.CreateOfKind(ctx, KindPersonal, "usr_other", "Outra", "Dela")
	join(t, svc, other.ID, "usr_owner", RoleMember)

	lv, err := svc.Levels(ctx, "usr_owner")
	if err != nil {
		t.Fatal(err)
	}
	if lv.Spaces != 1 {
		t.Fatalf("spaces = %d, want 1 (only the owned personal space)", lv.Spaces)
	}
}

// Spec § 2 and test 3: owner excluded; an expired invitation is not counted
// even while its row is still in the table (TTL has not reaped it).
func TestPeopleExcludeTheOwnerAndExpiredInvitations(t *testing.T) {
	repo, svc, now := limitedRepoAndService(t)
	ctx := context.Background()
	space, _ := svc.CreateOfKind(ctx, KindPersonal, "usr_owner", "Dono", "Casa")
	join(t, svc, space.ID, "usr_a", RoleMember)
	invite(t, repo, space.ID, "live@example.com", now.Add(time.Hour))
	invite(t, repo, space.ID, "gone@example.com", now.Add(-time.Hour))

	people, pending, err := svc.SpaceCounts(ctx, space.ID)
	if err != nil {
		t.Fatal(err)
	}
	if people != 1 || pending != 1 {
		t.Fatalf("people=%d pending=%d, want 1 and 1", people, pending)
	}
}

// Spec test 9: a person in two of the owner's spaces is one person, and an
// invited address that already belongs to a member is not a second one.
func TestFinancePeopleAreDistinct(t *testing.T) {
	repo, svc, now := limitedRepoAndService(t)
	ctx := context.Background()
	svc.WithEmailOwner(func(_ context.Context, email string) (string, error) {
		if email == "ana@example.com" {
			return "usr_ana", nil
		}
		return "", nil
	})
	casa, _ := svc.CreateOfKind(ctx, KindPersonal, "usr_owner", "Dono", "Casa")
	viagem, _ := svc.CreateOfKind(ctx, KindPersonal, "usr_owner", "Dono", "Viagem")
	join(t, svc, casa.ID, "usr_ana", RoleMember)
	join(t, svc, viagem.ID, "usr_ana", RoleViewer)
	invite(t, repo, viagem.ID, "ANA@example.com ", now.Add(time.Hour)) // already a member
	invite(t, repo, casa.ID, "bia@example.com", now.Add(time.Hour))
	invite(t, repo, viagem.ID, "bia@example.com", now.Add(time.Hour)) // same address twice

	lv, err := svc.Levels(ctx, "usr_owner")
	if err != nil {
		t.Fatal(err)
	}
	if lv.Spaces != 2 || lv.People != 2 {
		t.Fatalf("levels = %+v, want 2 spaces and 2 people (ana, bia)", lv)
	}
}

func TestALookupFailureIsAnError(t *testing.T) {
	repo, svc, now := limitedRepoAndService(t)
	ctx := context.Background()
	boom := errors.New("users table down")
	svc.WithEmailOwner(func(context.Context, string) (string, error) { return "", boom })
	casa, _ := svc.CreateOfKind(ctx, KindPersonal, "usr_owner", "Dono", "Casa")
	invite(t, repo, casa.ID, "x@example.com", now.Add(time.Hour))
	if _, err := svc.Levels(ctx, "usr_owner"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the lookup failure (a wrong level is worse than a late one)", err)
	}
}

func TestPlanLimitErrorSaysWhatWasRefused(t *testing.T) {
	err := error(&PlanLimitError{Resource: ResourcePeople, Limit: 5, Used: 5, Plan: "basic"})
	var target *PlanLimitError
	if !errors.As(err, &target) || target.Resource != ResourcePeople {
		t.Fatalf("not a PlanLimitError: %v", err)
	}
	if !allows(Unlimited, 1_000_000) || allows(0, 0) || !allows(3, 2) || allows(3, 3) {
		t.Fatal("allows() is wrong about -1, 0 or the boundary")
	}
}
