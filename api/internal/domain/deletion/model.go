// Package deletion implements the LGPD account-deletion request lifecycle
// (docs/specs/2026-10-06-account-deletion-ctech-account.md). Every state change
// is a conditional write made before its side effects; a request whose side
// effects are not yet confirmed stays "due" for the worker to retry.
package deletion

import (
	"errors"
	"time"
)

const (
	// GracePeriod is fixed by decision D8 (overview spec §11).
	GracePeriod = 7 * 24 * time.Hour
	// ConfirmWindow is how long the e-mail confirmation link is valid.
	ConfirmWindow = 24 * time.Hour
	// ReminderLead is how long before the lock the reminder e-mail is sent.
	ReminderLead = 24 * time.Hour
	// ConfirmationPhrase must be typed verbatim by the user.
	ConfirmationPhrase = "EXCLUIR MINHA CONTA"
	ScopeAccount       = "account"
)

type State string

const (
	StateAwaitingConfirmation State = "awaiting_confirmation"
	StatePending              State = "pending_deletion"
	StateCancelled            State = "cancelled"
	StateExpired              State = "expired"
	StateLocked               State = "locked"
	StatePurging              State = "purging"
	StateStalled              State = "stalled" // a participant reported a blocker after LOCKED; support resolves
	StateBlocked              State = "blocked" // a local blocker appeared at the end of grace
	StatePurged               State = "purged"
)

// Open reports whether the state holds the user's single open-request marker.
func (s State) Open() bool { return s != StateCancelled && s != StateExpired && s != StatePurged }

var (
	ErrNotFound    = errors.New("deletion: request not found")
	ErrOpenRequest = errors.New("deletion: user already has an open request")
	// ErrEligibilityUnavailable: a participant could not be asked, so the
	// assessment is unknown (never "eligible").
	ErrEligibilityUnavailable = errors.New("deletion: eligibility could not be assessed")
	ErrStateChanged           = errors.New("deletion: request state changed concurrently")
	ErrInvalidToken           = errors.New("deletion: link is invalid or expired")
	ErrNotCancellable         = errors.New("deletion: request can no longer be cancelled")
)

// Workspace is a ctech-account workspace erased with the user, with its kind
// (organization or personal). Participants such as billing need the kind.
type Workspace struct {
	ID   string `dynamodbav:"id"`
	Kind string `dynamodbav:"kind"`
}

// Request is one deletion request (pk REQ#{id}, sk META).
type Request struct {
	PK                string      `dynamodbav:"pk"`
	SK                string      `dynamodbav:"sk"`
	ID                string      `dynamodbav:"id"`
	UserID            string      `dynamodbav:"user_id"`
	Scope             string      `dynamodbav:"scope"`
	State             State       `dynamodbav:"request_state"`
	ConfirmTokenHash  string      `dynamodbav:"confirm_token_hash,omitempty"`
	CancelTokenHashes []string    `dynamodbav:"cancel_token_hashes,omitempty"` // one per e-mail that carried a cancel link
	LockApplied       bool        `dynamodbav:"lock_applied"`
	ScheduledSent     bool        `dynamodbav:"scheduled_sent"` // the first cancel link went out
	Reminded          bool        `dynamodbav:"reminded"`
	BlockedNotified   bool        `dynamodbav:"blocked_notified"`
	Organizations     []string    `dynamodbav:"organizations,omitempty"` // frozen at LOCKED: ids of the single-member workspaces erased with the user (D4)
	Workspaces        []Workspace `dynamodbav:"workspaces,omitempty"`    // the same workspaces with their kind
	Blockers          []string    `dynamodbav:"blockers,omitempty"`      // blocker codes found at the end of grace
	PurgedAt          string      `dynamodbav:"purged_at,omitempty"`
	LegalHold         bool        `dynamodbav:"legal_hold"`
	LegalHoldBy       string      `dynamodbav:"legal_hold_by,omitempty"` // admin who placed it; another must lift it (R13)
	RequestedAt       string      `dynamodbav:"requested_at"`
	ConfirmBy         string      `dynamodbav:"confirm_by"`
	ConfirmedAt       string      `dynamodbav:"confirmed_at,omitempty"`
	GraceUntil        string      `dynamodbav:"grace_until,omitempty"`
	CancelledAt       string      `dynamodbav:"cancelled_at,omitempty"`
	LockedAt          string      `dynamodbav:"locked_at,omitempty"`
	UpdatedAt         string      `dynamodbav:"updated_at"`
	// DueState/NextActionAt feed the sparse gsi_state_due index. Empty means
	// the worker has nothing to do for this request.
	DueState     string `dynamodbav:"due_state,omitempty"`
	NextActionAt string `dynamodbav:"next_action_at,omitempty"`
}

const metaSK = "META"

func BuildPK(id string) string { return "REQ#" + id }

func (r *Request) setDue(at time.Time) { r.DueState, r.NextActionAt = string(r.State), ts(at) }
func (r *Request) clearDue()           { r.DueState, r.NextActionAt = "", "" }

// ts formats in UTC RFC3339 so lexical order equals time order in the index.
func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

const (
	ServiceDispatched = "dispatched"
	ServiceAcked      = "acked"
	ServiceBlocked    = "blocked"
)

// ServiceStatus is one participant's progress on a request
// (pk REQ#{request_id}, sk SVC#{service}).
type ServiceStatus struct {
	PK              string         `dynamodbav:"pk"`
	SK              string         `dynamodbav:"sk"`
	RequestID       string         `dynamodbav:"request_id"`
	Service         string         `dynamodbav:"service"`
	Status          string         `dynamodbav:"service_status"`
	Attempts        int            `dynamodbav:"attempts"`
	FirstDispatchAt string         `dynamodbav:"first_dispatch_at"`
	LastDispatchAt  string         `dynamodbav:"last_dispatch_at"`
	AckedAt         string         `dynamodbav:"acked_at,omitempty"`
	Counts          map[string]int `dynamodbav:"counts,omitempty"`
	Blockers        []string       `dynamodbav:"blockers,omitempty"`
}

func serviceSK(service string) string { return "SVC#" + service }
