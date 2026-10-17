package dataexport

import (
	"context"
	"errors"
	"testing"
	"time"
)

type memRepo struct{ e *Export }

func (m *memRepo) Get(context.Context, string) (*Export, error) {
	if m.e == nil {
		return nil, ErrNotFound
	}
	c := *m.e
	return &c, nil
}
func (m *memRepo) Save(_ context.Context, e *Export, prev string) error {
	if (m.e == nil && prev != "") || (m.e != nil && m.e.ID != prev) {
		return ErrBuilding
	}
	c := *e
	m.e = &c
	return nil
}
func (m *memRepo) Delete(context.Context, string) error { m.e = nil; return nil }

type memStore struct{ objs map[string][]byte }

func (m *memStore) PutObject(_ context.Context, k, _ string, d []byte) error {
	m.objs[k] = d
	return nil
}
func (m *memStore) PresignGet(_ context.Context, k string, _ time.Duration) (string, error) {
	return "https://s3/" + k, nil
}
func (m *memStore) DeleteObject(_ context.Context, k string) error { delete(m.objs, k); return nil }

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

func setup(active bool, b fakeBuilder) (*Service, *memRepo, *memStore, *fakeMail, *fakeAudit, *time.Time) {
	repo, store, mail, audit := &memRepo{}, &memStore{objs: map[string][]byte{}}, &fakeMail{}, &fakeAudit{}
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

func TestDiscard(t *testing.T) {
	s, repo, store, _, _, _ := setup(true, fakeBuilder{})
	ctx := context.Background()
	s.Request(ctx, "u1")
	if err := s.Discard(ctx, "u1"); err != nil || repo.e != nil || len(store.objs) != 0 {
		t.Fatalf("err=%v", err)
	}
}
