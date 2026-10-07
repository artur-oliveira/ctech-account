package deletion

import (
	"context"
	"testing"
	"time"

	"gopkg.aoctech.app/account/api/internal/domain/apikey"
	"gopkg.aoctech.app/api-commons/erasure"
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

type fakeSessions struct{ revokedAll int }

func (f *fakeSessions) RevokeAll(context.Context, string, string) error { f.revokedAll++; return nil }

type fakeKeys struct {
	keys    []*apikey.APIKey
	revoked []string
}

func (f *fakeKeys) List(context.Context, string) ([]*apikey.APIKey, error) { return f.keys, nil }
func (f *fakeKeys) Revoke(_ context.Context, _, id string) error {
	f.revoked = append(f.revoked, id)
	return nil
}

type fakeTokens struct{ revoked, unrevoked int }

func (f *fakeTokens) Revoke(context.Context, string, time.Time) error { f.revoked++; return nil }
func (f *fakeTokens) Unrevoke(context.Context, string) error          { f.unrevoked++; return nil }

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
	if err := l.Erase(context.Background(), r); err != nil {
		t.Fatalf("Erase: %v", err)
	}
	if users.cleared != "r1" || tokens.unrevoked != 1 || len(pub.msgs) != 2 {
		t.Fatalf("cleared=%q unrevoked=%d msgs=%d", users.cleared, tokens.unrevoked, len(pub.msgs))
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
