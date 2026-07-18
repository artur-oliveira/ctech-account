package keystore

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

const (
	// KeyMaxAge is the rotation cadence for the active signing key.
	KeyMaxAge = 90 * 24 * time.Hour
	// CheckInterval is how often instances reload keys from SSM — it is both
	// the rotation check cadence and the ASG-wide propagation delay ceiling.
	CheckInterval = time.Hour
	// LockKey is the Valkey key guarding rotation across instances.
	LockKey = "rotate_jwk_lock"
	// LockTTL bounds how long a crashed rotator can hold the lock.
	LockTTL = time.Hour
)

// RotatorConfig wires the rotation loop to its collaborators.
type RotatorConfig struct {
	Store *Store
	// Reload swaps the live key set (jwtSvc.Reload).
	Reload func(active, previous *Key)
	// TryLock attempts the distributed rotation lock (cache.SetNX wrapper).
	TryLock  func(ctx context.Context) (bool, error)
	Interval time.Duration
	MaxAge   time.Duration
	Now      func() time.Time
}

// tick runs one reload-and-maybe-rotate cycle. On load failure it returns the
// error WITHOUT calling Reload — signing continues on the last good keys.
func tick(ctx context.Context, cfg RotatorConfig) error {
	active, previous, err := cfg.Store.Load(ctx)
	if err != nil {
		return fmt.Errorf("reloading keys: %w", err)
	}

	if cfg.Now().Sub(active.CreatedAt) > cfg.MaxAge {
		won, lockErr := cfg.TryLock(ctx)
		if lockErr != nil {
			slog.Warn("keystore: lock attempt failed, skipping rotation this tick", "error", lockErr)
		} else if won {
			newKey, rotErr := Rotate(ctx, cfg.Store, cfg.Now())
			if rotErr != nil {
				return fmt.Errorf("rotating key: %w", rotErr)
			}
			slog.Info("keystore: rotated signing key", "new_kid", newKey.KID, "old_kid", active.KID)
			active, previous = newKey, active
		}
		// Lock lost: another instance is rotating; the next tick's reload
		// picks up its result.
	}

	cfg.Reload(active, previous)
	return nil
}

// RunRotator reloads keys every Interval and rotates when the active key
// exceeds MaxAge, guarded by the distributed lock. Errors are logged, never
// fatal. Blocks until ctx is cancelled — run in a goroutine.
func RunRotator(ctx context.Context, cfg RotatorConfig) {
	t := time.NewTicker(cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := tick(ctx, cfg); err != nil {
				slog.Error("keystore: rotation tick failed", "error", err)
			}
		}
	}
}
