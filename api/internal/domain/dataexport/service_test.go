package dataexport

import (
	"context"
	"errors"
	"testing"
	"time"

	wire "gopkg.aoctech.app/api-commons/dataexport"
)

type memStore struct {
	objs  map[string][]byte
	sizes map[string]int64 // objects "uploaded" by participants
	puts  int
}

func (m *memStore) PutObject(_ context.Context, k, _ string, d []byte) error {
	m.objs[k] = d
	return nil
}
func (m *memStore) PresignGet(_ context.Context, k string, _ time.Duration) (string, error) {
	return "https://s3/get/" + k, nil
}
func (m *memStore) PresignPut(_ context.Context, k, _ string, _ time.Duration) (string, error) {
	m.puts++
	return "https://s3/put/" + k, nil
}
func (m *memStore) Size(_ context.Context, k string) (int64, error) {
	if n, ok := m.sizes[k]; ok {
		return n, nil
	}
	return 0, errors.New("missing")
}
func (m *memStore) DeleteObject(_ context.Context, k string) error {
	delete(m.objs, k)
	delete(m.sizes, k)
	return nil
}

type fakeDispatcher struct {
	msgs []wire.Message
	err  error
}

func (f *fakeDispatcher) Publish(_ context.Context, m wire.Message) error {
	f.msgs = append(f.msgs, m)
	return f.err
}

type fakeBuilder struct{ err error }

func (f fakeBuilder) Build(context.Context, string) ([]byte, error) { return []byte("zip"), f.err }

type fakeUsers struct{ active bool }

func (f fakeUsers) Contact(context.Context, string) (string, string, bool, error) {
	return "a@b.c", "Ana", f.active, nil
}

type fakeMail struct{ requested, ready int }

func (f *fakeMail) SendExportRequested(context.Context, string, string) error {
	f.requested++
	return nil
}
func (f *fakeMail) SendExportReady(context.Context, string, string) error { f.ready++; return nil }

type fakeAudit struct{ events []string }

func (f *fakeAudit) Record(_ context.Context, _, ev string, _ map[string]string) {
	f.events = append(f.events, ev)
}

func setup(active bool, b fakeBuilder) (*Service, *MemoryRepository, *memStore, *fakeMail, *fakeAudit, *time.Time) {
	repo, store, mail, audit := NewMemoryRepository(), &memStore{objs: map[string][]byte{}, sizes: map[string]int64{}}, &fakeMail{}, &fakeAudit{}
	s := NewService(repo, store, b, fakeUsers{active}, mail, audit)
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	s.async = func(f func()) { f() }
	n := 0
	s.newID = func() string { n++; return string(rune('a' + n)) }
	return s, repo, store, mail, audit, &now
}

func TestRequestBuildsAndLinks(t *testing.T) {
	s, _, store, mail, audit, _ := setup(true, fakeBuilder{})
	if _, err := s.Request(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}
	e, _ := s.Status(context.Background(), "u1")
	if e.State != StateReady || len(store.objs) != 1 || mail.requested != 1 || mail.ready != 1 {
		t.Fatalf("state=%s objs=%d mail=%+v", e.State, len(store.objs), mail)
	}
	links, err := s.Links(context.Background(), e)
	if err != nil || len(links) != 1 || links[0].Service != AccountService {
		t.Fatalf("links=%v err=%v", links, err)
	}
	want := []string{EventRequested, EventReady, EventLinkIssued}
	for i, w := range want {
		if audit.events[i] != w {
			t.Fatalf("events=%v", audit.events)
		}
	}
}

func TestRequestRefusedWhenAccountNotActive(t *testing.T) {
	s, _, _, _, _, _ := setup(false, fakeBuilder{})
	if _, err := s.Request(context.Background(), "u1"); !errors.Is(err, ErrLocked) {
		t.Fatalf("err=%v", err)
	}
}

func TestRequestRateLimitedUntilFailedOrOld(t *testing.T) {
	s, _, store, _, _, now := setup(true, fakeBuilder{})
	ctx := context.Background()
	s.Request(ctx, "u1")
	if _, err := s.Request(ctx, "u1"); !errors.Is(err, ErrTooSoon) {
		t.Fatalf("err=%v", err)
	}
	*now = now.Add(MinInterval + time.Second)
	if _, err := s.Request(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if len(store.objs) != 1 {
		t.Fatalf("replaced export's file must go: %d objects", len(store.objs))
	}
}

func TestFailedBuildAllowsImmediateRetry(t *testing.T) {
	s, _, _, mail, audit, _ := setup(true, fakeBuilder{err: errors.New("boom")})
	ctx := context.Background()
	s.Request(ctx, "u1")
	e, _ := s.Status(ctx, "u1")
	if e.State != StateFailed || mail.ready != 0 || audit.events[len(audit.events)-1] != EventFailed {
		t.Fatalf("state=%s", e.State)
	}
	s.builder = fakeBuilder{}
	if _, err := s.Request(ctx, "u1"); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestStuckBuildBecomesFailed(t *testing.T) {
	s, _, _, _, _, now := setup(true, fakeBuilder{})
	s.async = func(func()) {} // the process died mid-build
	ctx := context.Background()
	if _, err := s.Request(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Request(ctx, "u1"); !errors.Is(err, ErrBuilding) {
		t.Fatalf("err=%v", err)
	}
	*now = now.Add(StuckAfter + time.Second)
	e, _ := s.Status(ctx, "u1")
	if e.State != StateFailed {
		t.Fatalf("state=%s", e.State)
	}
}

func TestReadyExportExpiresAndFilesGo(t *testing.T) {
	s, _, store, _, _, now := setup(true, fakeBuilder{})
	ctx := context.Background()
	s.Request(ctx, "u1")
	*now = now.Add(ReadyTTL + time.Second)
	e, _ := s.Status(ctx, "u1")
	if e.State != StateExpired || len(store.objs) != 0 {
		t.Fatalf("state=%s objs=%d", e.State, len(store.objs))
	}
	if _, err := s.Links(ctx, e); !errors.Is(err, ErrNotReady) {
		t.Fatalf("err=%v", err)
	}
}

func TestDiscardKeepsTheRateLimit(t *testing.T) {
	s, repo, store, _, _, _ := setup(true, fakeBuilder{})
	ctx := context.Background()
	s.Request(ctx, "u1")
	e, _ := repo.Get(ctx, "u1")
	if err := s.Discard(ctx, "u1"); err != nil || len(store.objs) != 0 {
		t.Fatalf("err=%v", err)
	}
	if e, _ = repo.Get(ctx, "u1"); e.State != StateExpired {
		t.Fatalf("state=%s", e.State)
	}
	if _, err := s.Request(ctx, "u1"); !errors.Is(err, ErrTooSoon) {
		t.Fatalf("discard must not reset the 24h limit: %v", err)
	}
}

// withPoker adds one participant that has not answered yet.
func withPoker() (*Service, *MemoryRepository, *memStore, *fakeMail, *fakeDispatcher, *time.Time) {
	s, repo, store, mail, _, now := setup(true, fakeBuilder{})
	d := &fakeDispatcher{}
	s.WithParticipants([]string{"poker"}, d)
	return s, repo, store, mail, d, now
}

func TestRequestAsksEachParticipantWithAFreshUploadURL(t *testing.T) {
	s, _, _, mail, d, _ := withPoker()
	ctx := context.Background()
	e, err := s.Request(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.msgs) != 1 || d.msgs[0].Service != "poker" || d.msgs[0].RequestID != e.ID || d.msgs[0].Sub != "u1" ||
		d.msgs[0].PutURL != "https://s3/put/exports/u1/"+e.ID+"/poker.zip" || d.msgs[0].Attempt != 1 {
		t.Fatalf("msgs=%+v", d.msgs)
	}
	// The account part is ready, but a participant is still working: no "ready" e-mail yet.
	got, _ := s.Status(ctx, "u1")
	if got.State != StateReady || !got.Pending() || mail.ready != 0 {
		t.Fatalf("state=%s pending=%v ready-mails=%d", got.State, got.Pending(), mail.ready)
	}
}

func TestAckMakesThePartDownloadableAndSendsTheReadyMailOnce(t *testing.T) {
	s, _, store, mail, d, _ := withPoker()
	ctx := context.Background()
	e, _ := s.Request(ctx, "u1")
	store.sizes[d.msgs[0].PutURL[len("https://s3/put/"):]] = 321
	ack := wire.Ack{RequestID: e.ID, Sub: "u1", Service: "poker", Result: wire.ResultDone}
	for range 2 { // a redelivered ack is harmless
		if err := s.Ack(ctx, "poker", ack); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.Status(ctx, "u1")
	links, _ := s.Links(ctx, got)
	if got.Pending() || len(links) != 2 || mail.ready != 1 {
		t.Fatalf("pending=%v links=%d ready-mails=%d", got.Pending(), len(links), mail.ready)
	}
	if p, _ := got.Part("poker"); p.Size != 321 {
		t.Fatalf("size comes from the bucket: %d", p.Size)
	}
}

func TestAckRejections(t *testing.T) {
	s, _, _, _, _, _ := withPoker()
	ctx := context.Background()
	e, _ := s.Request(ctx, "u1")
	if err := s.Ack(ctx, "poker", wire.Ack{RequestID: e.ID, Sub: "u1", Result: wire.ResultDone}); !errors.Is(err, ErrNotUploaded) {
		t.Fatalf("object missing: %v", err)
	}
	if err := s.Ack(ctx, "billing", wire.Ack{RequestID: e.ID, Sub: "u1", Result: wire.ResultDone}); !errors.Is(err, ErrUnknownPart) {
		t.Fatalf("service not asked: %v", err)
	}
	if err := s.Ack(ctx, "account", wire.Ack{RequestID: e.ID, Sub: "u1", Result: wire.ResultDone}); !errors.Is(err, ErrUnknownPart) {
		t.Fatalf("a participant cannot ack the account part: %v", err)
	}
	if err := s.Ack(ctx, "poker", wire.Ack{RequestID: "old", Sub: "u1", Result: wire.ResultDone}); !errors.Is(err, ErrStale) {
		t.Fatalf("replaced export: %v", err)
	}
	if err := s.Ack(ctx, "poker", wire.Ack{RequestID: e.ID, Sub: "nobody", Result: wire.ResultDone}); !errors.Is(err, ErrStale) {
		t.Fatalf("unknown person: %v", err)
	}
}

func TestSilentParticipantIsAskedAgainThenFailsWithoutBlockingTheRest(t *testing.T) {
	s, _, _, mail, d, now := withPoker()
	ctx := context.Background()
	s.Request(ctx, "u1")
	*now = now.Add(RedispatchAfter + time.Second)
	s.Status(ctx, "u1")
	if len(d.msgs) != 2 || d.msgs[1].Attempt != 2 {
		t.Fatalf("expected a second dispatch: %+v", d.msgs)
	}
	s.Status(ctx, "u1") // not again until RedispatchAfter has passed once more
	if len(d.msgs) != 2 {
		t.Fatalf("dispatched twice in a row: %d", len(d.msgs))
	}
	*now = now.Add(PartDeadline)
	got, _ := s.Status(ctx, "u1")
	links, _ := s.Links(ctx, got)
	if p, _ := got.Part("poker"); p.Status != PartFailed || got.State != StateReady || len(links) != 1 || mail.ready != 1 {
		t.Fatalf("poker=%s state=%s links=%d ready-mails=%d", p.Status, got.State, len(links), mail.ready)
	}
}

func TestAnAckForADiscardedExportIsStale(t *testing.T) {
	s, _, store, _, d, _ := withPoker()
	ctx := context.Background()
	e, _ := s.Request(ctx, "u1")
	s.Discard(ctx, "u1")
	store.sizes[d.msgs[0].PutURL[len("https://s3/put/"):]] = 1
	if err := s.Ack(ctx, "poker", wire.Ack{RequestID: e.ID, Sub: "u1", Result: wire.ResultDone}); !errors.Is(err, ErrStale) {
		t.Fatalf("err=%v", err)
	}
}

func TestAFailedDispatchIsRetriedLater(t *testing.T) {
	s, _, _, _, d, now := withPoker()
	d.err = errors.New("sns down")
	ctx := context.Background()
	s.Request(ctx, "u1")
	d.err = nil
	*now = now.Add(RedispatchAfter + time.Second)
	s.Status(ctx, "u1")
	if len(d.msgs) != 2 {
		t.Fatalf("msgs=%d", len(d.msgs))
	}
}
