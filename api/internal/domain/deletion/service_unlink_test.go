package deletion

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/api-commons/erasure"
)

type fakeServiceElig struct {
	blockers []erasure.Blocker
	calls    []string
}

func (f *fakeServiceElig) CheckService(_ context.Context, _, service string) ([]erasure.Blocker, error) {
	f.calls = append(f.calls, service)
	return f.blockers, nil
}

type countingOwnership struct{ calls int }

func (o *countingOwnership) Assess(context.Context, string) ([]Workspace, []erasure.Blocker, error) {
	o.calls++
	return nil, nil, nil
}

func (f *fixture) withServices(elig *fakeServiceElig) {
	f.svc = f.svc.WithServices(func(name string) (ServiceTarget, bool) {
		if name == "poker" || name == "dfe" {
			return ServiceTarget{Service: name, LoginClients: []string{name + "-web"}}, true
		}
		return ServiceTarget{}, false
	}, elig)
}

func TestRequestForAServiceAsksOnlyThatParticipant(t *testing.T) {
	f := newFixture(t)
	elig, own := &fakeServiceElig{}, &countingOwnership{}
	f.svc = f.svc.WithOwnership(own)
	f.withServices(elig)

	r, err := f.svc.RequestService(ctx, "u1", "poker")
	if err != nil {
		t.Fatal(err)
	}
	if r.Scope != ScopeService || r.Service != "poker" || r.State != StateAwaitingConfirmation {
		t.Fatalf("request = %+v", r)
	}
	if len(elig.calls) != 1 || elig.calls[0] != "poker" {
		t.Fatalf("participants asked = %v, want only poker", elig.calls)
	}
	if own.calls != 0 {
		t.Fatal("a service unlink must not run the account ownership assessment (U1, U2)")
	}
}

func TestRequestForAnUnknownServiceIsRefused(t *testing.T) {
	f := newFixture(t)
	f.withServices(&fakeServiceElig{})
	if _, err := f.svc.RequestService(ctx, "u1", "wallet"); !errors.Is(err, ErrUnknownService) {
		t.Fatalf("err = %v, want ErrUnknownService (wallet has no login client)", err)
	}
	if _, err := f.svc.Status(ctx, "u1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("a refused request must not be stored")
	}
}

func TestRequestForAServiceReportsItsBlockers(t *testing.T) {
	f := newFixture(t)
	f.withServices(&fakeServiceElig{blockers: []erasure.Blocker{{Code: "poker.seated_at_table"}}})
	_, err := f.svc.RequestService(ctx, "u1", "poker")
	var blocked *BlockedError
	if !errors.As(err, &blocked) || len(blocked.Blockers) != 1 || blocked.Blockers[0].Code != "poker.seated_at_table" {
		t.Fatalf("err = %v, want BlockedError(poker.seated_at_table)", err)
	}
}

// Review Focus 6: the single open-request marker covers both scopes.
func TestAnOpenServiceRequestBlocksANewRequest(t *testing.T) {
	f := newFixture(t)
	f.withServices(&fakeServiceElig{})
	if _, err := f.svc.RequestService(ctx, "u1", "poker"); err != nil {
		t.Fatal(err)
	}
	if r, err := f.svc.RequestService(ctx, "u1", "dfe"); err != nil {
		// an unconfirmed request is replaced (lost e-mail), same as the account scope
		t.Fatalf("replacing an unconfirmed request: %v", err)
	} else if r.Service != "dfe" {
		t.Fatalf("service = %q", r.Service)
	}
	confirmed, _ := f.svc.Status(ctx, "u1")
	if _, err := f.svc.Confirm(ctx, confirmed.ID, f.mail.confirmToken); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.RequestService(ctx, "u1", "poker"); !errors.Is(err, ErrOpenRequest) {
		t.Fatalf("a second service request: %v, want ErrOpenRequest", err)
	}
	if _, err := f.svc.Request(ctx, "u1"); !errors.Is(err, ErrOpenRequest) {
		t.Fatalf("an account request: %v, want ErrOpenRequest", err)
	}
}

type fakeUnlinker struct{ calls []string }

func (f *fakeUnlinker) Finish(_ context.Context, r *Request) error {
	f.calls = append(f.calls, r.Service)
	return nil
}

// serviceFixture is a confirmed service request over the real AccountLocker,
// so what the person keeps and loses is visible end to end.
type serviceFixture struct {
	*fixture
	pub      *fakePub
	users    *fakeBlocker
	tokens   *fakeTokens
	sessions *fakeSessions
	purger   *fakePurger
	unlinker *fakeUnlinker
	elig     *fakeServiceElig
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	f := newFixture(t)
	sf := &serviceFixture{fixture: f, pub: &fakePub{}, users: &fakeBlocker{}, tokens: &fakeTokens{}, sessions: &fakeSessions{},
		purger: &fakePurger{}, unlinker: &fakeUnlinker{}, elig: &fakeServiceElig{}}
	clients := func(string) []string { return []string{"poker-web"} }
	locker := NewAccountLocker(sf.users, sf.sessions, &fakeKeys{}, sf.tokens, sf.pub, []string{"wallet", "poker"}).
		WithServiceClients(clients, sf.sessions)
	f.svc = NewService(f.repo, fakeUsers{u: f.svc.users.(fakeUsers).u}, locker, f.mail).
		WithOwnership(&countingOwnership{}).WithPurger(sf.purger).WithParticipants([]string{"wallet", "poker"}).
		WithServiceUnlinker(sf.unlinker)
	f.svc.now = func() time.Time { return f.now }
	f.withServices(sf.elig)
	return sf
}

func (sf *serviceFixture) confirmed(t *testing.T) *Request {
	t.Helper()
	if _, err := sf.svc.RequestService(ctx, "u1", "poker"); err != nil {
		t.Fatal(err)
	}
	r, _ := sf.svc.Status(ctx, "u1")
	r, err := sf.svc.Confirm(ctx, r.ID, sf.mail.confirmToken)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAServiceRequestDispatchesAndWaitsForOneAck(t *testing.T) {
	sf := newServiceFixture(t)
	r := sf.confirmed(t)
	sf.now = sf.now.Add(GracePeriod)
	sf.svc.ProcessDue(ctx)

	if s := sf.stored(t, r.ID); s.State != StatePurging || len(s.Workspaces) != 0 {
		t.Fatalf("after grace: %+v", s)
	}
	rows, _ := sf.repo.ListServices(ctx, r.ID)
	if len(rows) != 1 || rows[0].Service != "poker" {
		t.Fatalf("status rows = %+v, want only poker (wallet is a participant too, and must not be asked)", rows)
	}
	var erases []erasure.Message
	for _, m := range sf.pub.msgs {
		if m.Type == erasure.TypeErase {
			erases = append(erases, m)
		}
	}
	if len(erases) != 1 || erases[0].Scope != erasure.ScopeService || len(erases[0].Services) != 1 || erases[0].Services[0] != "poker" {
		t.Fatalf("erase messages = %+v", erases)
	}
	if len(sf.unlinker.calls) != 0 {
		t.Fatal("must wait for the ack before finishing")
	}

	if err := sf.svc.Ack(ctx, "poker", erasure.Ack{RequestID: r.ID, Result: erasure.ResultDone}); err != nil {
		t.Fatal(err)
	}
	sf.svc.ProcessDue(ctx)
	if s := sf.stored(t, r.ID); s.State != StatePurged {
		t.Fatalf("state = %s, want purged", s.State)
	}
	if len(sf.unlinker.calls) != 1 || sf.unlinker.calls[0] != "poker" {
		t.Fatalf("unlinker calls = %v", sf.unlinker.calls)
	}
	if sf.purger.calls != 0 {
		t.Fatal("a service unlink must never purge the account's own data")
	}
}

// Review Focus 2.
func TestCancelingAServiceRequestUnlocksOnlyThatService(t *testing.T) {
	sf := newServiceFixture(t)
	r := sf.confirmed(t)
	if _, err := sf.svc.Cancel(ctx, r.ID, sf.mail.cancelToken); err != nil {
		// the scheduled e-mail goes out on the first worker tick
		sf.svc.ProcessDue(ctx)
		if _, err := sf.svc.Cancel(ctx, r.ID, sf.mail.cancelToken); err != nil {
			t.Fatal(err)
		}
	}
	sf.svc.ProcessDue(ctx)

	if sf.users.cleared != "" || sf.users.marked != "" || sf.tokens.revoked != 0 {
		t.Fatalf("the account was touched: marked=%q cleared=%q jwt=%d", sf.users.marked, sf.users.cleared, sf.tokens.revoked)
	}
	var types []erasure.Type
	for _, m := range sf.pub.msgs {
		if m.Scope != erasure.ScopeService || len(m.Services) != 1 || m.Services[0] != "poker" {
			t.Fatalf("message %+v must address poker only", m)
		}
		types = append(types, m.Type)
	}
	if len(types) != 2 || types[0] != erasure.TypeLocked || types[1] != erasure.TypeUnlocked {
		t.Fatalf("messages = %v, want locked then unlocked", types)
	}
}

func TestABlockerAtTheEndOfGraceBlocksAServiceRequestToo(t *testing.T) {
	sf := newServiceFixture(t)
	r := sf.confirmed(t)
	sf.elig.blockers = []erasure.Blocker{{Code: "poker.chips_held"}}
	sf.now = sf.now.Add(GracePeriod)
	sf.svc.ProcessDue(ctx)
	if s := sf.stored(t, r.ID); s.State != StateBlocked || len(s.Blockers) != 1 || s.Blockers[0] != "poker.chips_held" {
		t.Fatalf("request = %+v, want blocked by poker.chips_held", s)
	}
}

// The whole cycle through the real locker, worker and relinker, with every
// outside call recorded: what each product is told, in which order, and that
// the account itself is never touched. Prod cannot be waited on for 7 days.
func TestAServiceUnlinkAndComingBackEndToEnd(t *testing.T) {
	sf := newServiceFixture(t)
	unlinks := &recordingUnlinks{}
	sf.svc = sf.svc.WithServiceUnlinker(NewServiceUnlinker(&recordingConsents{}, sf.sessions, unlinks,
		fakeUsers{u: &user.User{Email: "ana@example.com", FirstName: "Ana"}}, &recordingAudit{}, sf.mail,
		func(string) []string { return []string{"poker-web"} }))
	r := sf.confirmed(t)
	sf.svc.ProcessDue(ctx) // lock applied, cancel link mailed
	sf.now = sf.now.Add(GracePeriod)
	sf.svc.ProcessDue(ctx) // grace over: locked, erase published, purging
	if err := sf.svc.Ack(ctx, "poker", erasure.Ack{RequestID: r.ID, Result: erasure.ResultDone}); err != nil {
		t.Fatal(err)
	}
	sf.svc.ProcessDue(ctx) // purged

	if s := sf.stored(t, r.ID); s.State != StatePurged {
		t.Fatalf("state = %s, want purged", s.State)
	}
	if sf.users.marked != "" || sf.tokens.revoked != 0 || sf.purger.calls != 0 {
		t.Fatal("the account was touched by a service unlink")
	}
	if len(unlinks.added) != 1 || unlinks.added[0] != "poker" {
		t.Fatalf("unlinked = %v", unlinks.added)
	}
	var types []erasure.Type
	var eraseAt time.Time
	for _, m := range sf.pub.msgs {
		if m.Scope != erasure.ScopeService || len(m.Services) != 1 || m.Services[0] != "poker" || len(m.Workspaces) != 0 {
			t.Fatalf("message %+v must be service scope, poker only, no workspaces", m)
		}
		types = append(types, m.Type)
		if m.Type == erasure.TypeErase {
			eraseAt = m.IssuedAt
		}
	}
	if len(types) < 2 || types[0] != erasure.TypeLocked || types[len(types)-1] != erasure.TypeErase {
		t.Fatalf("messages = %v, want locked ... erase", types)
	}

	// The person signs in to poker again, a day later.
	back := NewRelinker(sf.pub, &recordingForget{}, func(string) []string { return []string{"poker-web"} })
	back.now = func() time.Time { return sf.now.Add(24 * time.Hour) }
	if err := back.Relink(ctx, &user.User{PK: "USER_u1", UnlinkedServices: unlinks.added}, "poker-web"); err != nil {
		t.Fatal(err)
	}
	last := sf.pub.msgs[len(sf.pub.msgs)-1]
	if last.Type != erasure.TypeRelinked || last.Services[0] != "poker" || !last.IssuedAt.After(eraseAt) {
		t.Fatalf("last message = %+v; want user.relinked for poker issued after the erase (%s), or the participant ignores it as stale", last, eraseAt)
	}
}

type recordingForget struct{ removed []string }

func (r *recordingForget) RemoveUnlinkedService(_ context.Context, _, service string) error {
	r.removed = append(r.removed, service)
	return nil
}
