package deletion

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/account/api/internal/domain/apikey"
	commoncache "gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/erasure"
	"gopkg.aoctech.app/api-commons/jwtverify"
)

type fakeBlocker struct{ marked, cleared string }

func (f *fakeBlocker) MarkPendingDeletion(_ context.Context, _, rid string) error {
	f.marked = rid
	return nil
}
func (f *fakeBlocker) ClearPendingDeletion(_ context.Context, _, rid string) error {
	f.cleared = rid
	return nil
}

type fakeSessions struct {
	revokedAll int
	clients    [][]string
}

func (f *fakeSessions) RevokeAll(context.Context, string, string) error { f.revokedAll++; return nil }
func (f *fakeSessions) RevokeClients(_ context.Context, _ string, c []string) error {
	f.clients = append(f.clients, c)
	return nil
}

type fakeKeys struct {
	keys    []*apikey.APIKey
	revoked []string
}

func (f *fakeKeys) List(context.Context, string) ([]*apikey.APIKey, error) { return f.keys, nil }
func (f *fakeKeys) Revoke(_ context.Context, _, id string) error {
	f.revoked = append(f.revoked, id)
	return nil
}

type fakeTokens struct{ revoked int }

func (f *fakeTokens) Revoke(context.Context, string, time.Time) error { f.revoked++; return nil }

type fakePub struct{ msgs []erasure.Message }

func (f *fakePub) Publish(_ context.Context, m erasure.Message) error {
	f.msgs = append(f.msgs, m)
	return nil
}

func lockedRequest() *Request {
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	return &Request{ID: "r1", UserID: "u1", ConfirmedAt: ts(at), CancelledAt: ts(at.Add(time.Hour)), LockedAt: ts(at.Add(GracePeriod))}
}

func TestAccountLocker_LockRevokesEverythingAndPublishes(t *testing.T) {
	users, sessions, tokens, pub := &fakeBlocker{}, &fakeSessions{}, &fakeTokens{}, &fakePub{}
	keys := &fakeKeys{keys: []*apikey.APIKey{{PK: apikey.BuildPK("u1"), SK: apikey.BuildSK("k1")}}}
	l := NewAccountLocker(users, sessions, keys, tokens, pub, []string{"wallet"})
	r := lockedRequest()
	if err := l.Lock(context.Background(), r); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if users.marked != "r1" || sessions.revokedAll != 1 || len(keys.revoked) != 1 || keys.revoked[0] != "k1" || tokens.revoked != 1 {
		t.Fatalf("marked=%q sessions=%d keys=%v tokens=%d", users.marked, sessions.revokedAll, keys.revoked, tokens.revoked)
	}
	if len(pub.msgs) != 1 || pub.msgs[0].Type != erasure.TypeLocked || !pub.msgs[0].IssuedAt.Equal(parseTime(r.ConfirmedAt)) || pub.msgs[0].Sub != "u1" {
		t.Fatalf("published %+v", pub.msgs)
	}
}

func TestAccountLocker_UnlockAndErasePublishWithTheirOwnTimestamps(t *testing.T) {
	users, tokens, pub := &fakeBlocker{}, &fakeTokens{}, &fakePub{}
	l := NewAccountLocker(users, &fakeSessions{}, &fakeKeys{}, tokens, pub, []string{"wallet"})
	r := lockedRequest()
	if err := l.Unlock(context.Background(), r); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if err := l.Erase(context.Background(), r, nil); err != nil {
		t.Fatalf("Erase: %v", err)
	}
	if users.cleared != "r1" || tokens.revoked != 0 || len(pub.msgs) != 2 {
		t.Fatalf("cleared=%q revoked=%d msgs=%d", users.cleared, tokens.revoked, len(pub.msgs))
	}
	if pub.msgs[0].Type != erasure.TypeUnlocked || !pub.msgs[0].IssuedAt.Equal(parseTime(r.CancelledAt)) ||
		pub.msgs[1].Type != erasure.TypeErase || !pub.msgs[1].IssuedAt.Equal(parseTime(r.LockedAt)) {
		t.Fatalf("published %+v", pub.msgs)
	}
}

func TestAccountLocker_NoServicesPublishesNothing(t *testing.T) {
	pub := &fakePub{}
	l := NewAccountLocker(&fakeBlocker{}, &fakeSessions{}, &fakeKeys{}, &fakeTokens{}, pub, nil)
	if err := l.Lock(context.Background(), lockedRequest()); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if len(pub.msgs) != 0 {
		t.Fatal("with no participants configured nothing is published")
	}
}

// A late (retried) unlock of an old request must not lift the revocation a
// newer request wrote. New tokens after a cancel are issued after the cutoff,
// so nothing ever needs the entry removed early.
func TestAccountLocker_StaleUnlockKeepsNewerRevocation(t *testing.T) {
	ctx := context.Background()
	backend := commoncache.NewMemoryBackend(16)
	l := NewAccountLocker(&fakeBlocker{}, &fakeSessions{}, &fakeKeys{}, NewJWTRevoker(backend), &fakePub{}, nil)
	newer := &Request{ID: "r2", UserID: "u1", ConfirmedAt: ts(time.Now())}
	if err := l.Lock(ctx, newer); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	stale := &Request{ID: "r1", UserID: "u1", CancelledAt: ts(time.Now().Add(-time.Hour))}
	if err := l.Unlock(ctx, stale); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if err := jwtverify.CheckRevoked(ctx, backend, "u1", time.Now().Add(-time.Minute).Unix()); !errors.Is(err, jwtverify.ErrTokenRevoked) {
		t.Fatalf("pre-lock token must stay revoked, got %v", err)
	}
}

func TestAccountLocker_ErasePublishesFrozenWorkspaces(t *testing.T) {
	pub := &fakePub{}
	l := NewAccountLocker(&fakeBlocker{}, &fakeSessions{}, &fakeKeys{}, &fakeTokens{}, pub, []string{"dfe"})
	r := lockedRequest()
	r.Organizations = []string{"org-1", "space-1"}
	r.Workspaces = []Workspace{{ID: "org-1", Kind: "organization"}, {ID: "space-1", Kind: "personal"}}
	if err := l.Erase(context.Background(), r, nil); err != nil {
		t.Fatalf("Erase: %v", err)
	}
	if len(pub.msgs) != 1 || len(pub.msgs[0].Organizations) != 2 || len(pub.msgs[0].Workspaces) != 2 ||
		pub.msgs[0].Workspaces[1].ID != "space-1" || pub.msgs[0].Workspaces[1].Kind != "personal" {
		t.Fatalf("published %+v", pub.msgs)
	}
	if err := pub.msgs[0].Validate(); err != nil {
		t.Fatalf("published message is invalid: %v", err)
	}
}

func TestAccountLocker_EraseToSubset(t *testing.T) {
	pub := &fakePub{}
	l := NewAccountLocker(&fakeBlocker{}, &fakeSessions{}, &fakeKeys{}, &fakeTokens{}, pub, []string{"wallet", "dfe"})
	if err := l.Erase(context.Background(), lockedRequest(), []string{"dfe"}); err != nil {
		t.Fatalf("Erase: %v", err)
	}
	if len(pub.msgs) != 1 || len(pub.msgs[0].Services) != 1 || pub.msgs[0].Services[0] != "dfe" {
		t.Fatalf("published %+v", pub.msgs)
	}
}

func serviceLockedRequest() *Request {
	r := lockedRequest()
	r.Scope, r.Service = ScopeService, "poker"
	return r
}

// Review Focus 1: leaving one product must not sign the person out of anything else.
func TestServiceLockLeavesTheAccountAndOtherProductsAlone(t *testing.T) {
	users, sessions, tokens, pub := &fakeBlocker{}, &fakeSessions{}, &fakeTokens{}, &fakePub{}
	keys := &fakeKeys{keys: []*apikey.APIKey{
		{PK: apikey.BuildPK("u1"), SK: apikey.BuildSK("poker-key"), Scopes: []string{"poker:tables:read"}},
		{PK: apikey.BuildPK("u1"), SK: apikey.BuildSK("wallet-key"), Scopes: []string{"wallet:balance:read"}},
		{PK: apikey.BuildPK("u1"), SK: apikey.BuildSK("mixed-key"), Scopes: []string{"wallet:balance:read", "internal:poker:x"}},
	}}
	l := NewAccountLocker(users, sessions, keys, tokens, pub, []string{"wallet", "poker"}).
		WithServiceClients(func(s string) []string {
			if s == "poker" {
				return []string{"poker-web"}
			}
			return nil
		}, sessions)

	if err := l.Lock(context.Background(), serviceLockedRequest()); err != nil {
		t.Fatal(err)
	}

	if users.marked != "" || sessions.revokedAll != 0 || tokens.revoked != 0 {
		t.Fatalf("account touched: marked=%q revokedAll=%d jwtRevoked=%d", users.marked, sessions.revokedAll, tokens.revoked)
	}
	if len(sessions.clients) != 1 || len(sessions.clients[0]) != 1 || sessions.clients[0][0] != "poker-web" {
		t.Fatalf("refresh tokens revoked for %v, want only poker-web", sessions.clients)
	}
	if len(keys.revoked) != 2 || keys.revoked[0] != "poker-key" || keys.revoked[1] != "mixed-key" {
		t.Fatalf("api keys revoked = %v, want the ones holding a poker scope", keys.revoked)
	}
	if len(pub.msgs) != 1 {
		t.Fatalf("published %d messages", len(pub.msgs))
	}
	m := pub.msgs[0]
	if m.Type != erasure.TypeLocked || m.Scope != erasure.ScopeService || len(m.Services) != 1 || m.Services[0] != "poker" {
		t.Fatalf("message = %+v, want locked / service / [poker]", m)
	}
}

func TestServiceUnlockAndEraseAddressOnlyThatServiceWithNoWorkspaces(t *testing.T) {
	users, sessions, pub := &fakeBlocker{}, &fakeSessions{}, &fakePub{}
	l := NewAccountLocker(users, sessions, &fakeKeys{}, &fakeTokens{}, pub, []string{"wallet", "poker"})
	r := serviceLockedRequest()
	r.Organizations = []string{"org-1"} // must never leak into a service message
	r.Workspaces = []Workspace{{ID: "org-1", Kind: "organization"}}

	if err := l.Unlock(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if err := l.Erase(context.Background(), r, nil); err != nil {
		t.Fatal(err)
	}
	if users.cleared != "" {
		t.Fatal("unlock must not clear a pending-deletion mark the request never set")
	}
	for _, m := range pub.msgs {
		if m.Scope != erasure.ScopeService || len(m.Services) != 1 || m.Services[0] != "poker" ||
			len(m.Organizations) != 0 || len(m.Workspaces) != 0 {
			t.Fatalf("message = %+v, want service scope, [poker], no workspaces", m)
		}
	}
	if len(pub.msgs) != 2 || pub.msgs[0].Type != erasure.TypeUnlocked || pub.msgs[1].Type != erasure.TypeErase {
		t.Fatalf("messages = %+v", pub.msgs)
	}
}
