// Package dataexport is the personal-data export (LGPD art. 18, V) of
// docs/specs/2026-10-17-data-export.md: a person asks for a copy of their data,
// a part per CTech product is built, and they download it through short-lived
// links. Step 1 of the rollout builds only ctech-account's own part.
package dataexport

import (
	"errors"
	"time"
)

const (
	// MinInterval is the shortest time between two requests of one person (E6).
	MinInterval = 24 * time.Hour
	// ReadyTTL is how long a finished export stays downloadable.
	ReadyTTL = 7 * 24 * time.Hour
	// StuckAfter is how long a build may run before it is declared failed, so a
	// crashed process never blocks the person from asking again.
	StuckAfter = 10 * time.Minute
	// LinkTTL is how long a download link lives. Links are minted on every status
	// read for the signed-in person and are never e-mailed (E6).
	LinkTTL = 15 * time.Minute
	// AccountService names ctech-account's own part.
	AccountService = "account"
)

type State string

const (
	StateBuilding State = "building"
	StateReady    State = "ready"
	StateFailed   State = "failed"
	StateExpired  State = "expired"
)

var (
	ErrNotFound = errors.New("dataexport: no export")
	// ErrBuilding: an export is still being built.
	ErrBuilding = errors.New("dataexport: an export is already being built")
	// ErrTooSoon: the person asked less than MinInterval ago.
	ErrTooSoon  = errors.New("dataexport: an export was requested in the last 24 hours")
	ErrNotReady = errors.New("dataexport: the export is not ready")
	// ErrLocked: a locked (deletion pending) or purged account has no export (E7).
	ErrLocked = errors.New("dataexport: the account is not active")
)

// Part is one product's file of the export.
type Part struct {
	Service string
	Key     string // object key in the exports bucket
	Size    int64
	Status  string // building | ready | failed
}

// Export is a person's current export. One per person: a new request replaces it.
type Export struct {
	ID          string
	UserID      string
	State       State
	RequestedAt time.Time
	ReadyAt     time.Time
	ExpiresAt   time.Time
	Parts       []Part
	Error       string // a short reason when failed, never personal data
}

// ReadyUntil is when a ready export stops being offered.
func (e *Export) Expired(now time.Time) bool {
	return e.State == StateReady && !e.ExpiresAt.IsZero() && now.After(e.ExpiresAt)
}

// Stuck reports a build that has run too long.
func (e *Export) Stuck(now time.Time) bool {
	return e.State == StateBuilding && now.Sub(e.RequestedAt) > StuckAfter
}
