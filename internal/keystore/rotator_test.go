package keystore

import (
	"context"
	"errors"
	"testing"
	"time"
)

type reloadRecorder struct {
	active   *Key
	previous *Key
	calls    int
}

func (r *reloadRecorder) reload(active, previous *Key) {
	r.active, r.previous = active, previous
	r.calls++
}

func rotatorFixture(t *testing.T, keyAge time.Duration, lockWon bool) (*fakeSSM, *Store, *reloadRecorder, RotatorConfig) {
	t.Helper()
	fake := newFakeSSM()
	store := NewStore(fake, "test")
	now := time.Now()
	active, err := Generate(now.Add(-keyAge))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), active, nil); err != nil {
		t.Fatal(err)
	}
	fake.putOrder = nil // reset write log after seeding

	rec := &reloadRecorder{}
	cfg := RotatorConfig{
		Store:    store,
		Reload:   rec.reload,
		TryLock:  func(context.Context) (bool, error) { return lockWon, nil },
		Interval: time.Hour,
		MaxAge:   KeyMaxAge,
		Now:      func() time.Time { return now },
	}
	return fake, store, rec, cfg
}

func TestTickReloadsWithoutRotationWhenKeyYoung(t *testing.T) {
	fake, store, rec, cfg := rotatorFixture(t, 10*24*time.Hour, true)
	activeBefore, _, _ := store.Load(context.Background())

	if err := tick(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if len(fake.putOrder) != 0 {
		t.Errorf("young key must not rotate; writes: %v", fake.putOrder)
	}
	if rec.calls != 1 || rec.active.KID != activeBefore.KID || rec.previous != nil {
		t.Errorf("reload: calls=%d active=%v previous=%v", rec.calls, rec.active, rec.previous)
	}
}

func TestTickRotatesWhenOldAndLockWon(t *testing.T) {
	fake, store, rec, cfg := rotatorFixture(t, 91*24*time.Hour, true)
	oldActive, _, _ := store.Load(context.Background())

	if err := tick(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if len(fake.putOrder) != 2 {
		t.Fatalf("expected rotation writes, got %v", fake.putOrder)
	}
	active, previous, _ := store.Load(context.Background())
	if active.KID == oldActive.KID || previous == nil || previous.KID != oldActive.KID {
		t.Errorf("rotation state: active=%s previous=%v", active.KID, previous)
	}
	if rec.active.KID != active.KID || rec.previous.KID != oldActive.KID {
		t.Errorf("reload got stale keys: %v / %v", rec.active, rec.previous)
	}
}

func TestTickSkipsRotationWhenLockLost(t *testing.T) {
	fake, _, rec, cfg := rotatorFixture(t, 91*24*time.Hour, false)

	if err := tick(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if len(fake.putOrder) != 0 {
		t.Errorf("lock lost must not write; writes: %v", fake.putOrder)
	}
	if rec.calls != 1 {
		t.Errorf("reload must still run to pick up other instance's rotation; calls=%d", rec.calls)
	}
}

func TestTickSurvivesSSMError(t *testing.T) {
	fake, _, rec, cfg := rotatorFixture(t, time.Hour, true)
	fake.getErr = errors.New("ssm down")

	if err := tick(context.Background(), cfg); err == nil {
		t.Error("expected error from tick")
	}
	if rec.calls != 0 {
		t.Error("reload must not run on load failure (keep last good keys)")
	}
}
