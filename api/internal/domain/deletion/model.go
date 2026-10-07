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
)

// Open reports whether the state holds the user's single open-request marker.
func (s State) Open() bool { return s != StateCancelled && s != StateExpired }

var (
	ErrNotFound       = errors.New("deletion: request not found")
	ErrOpenRequest    = errors.New("deletion: user already has an open request")
	ErrStateChanged   = errors.New("deletion: request state changed concurrently")
	ErrInvalidToken   = errors.New("deletion: link is invalid or expired")
	ErrNotCancellable = errors.New("deletion: request can no longer be cancelled")
)

// Request is one deletion request (pk REQ#{id}, sk META).
type Request struct {
	PK                string   `dynamodbav:"pk"`
	SK                string   `dynamodbav:"sk"`
	ID                string   `dynamodbav:"id"`
	UserID            string   `dynamodbav:"user_id"`
	Scope             string   `dynamodbav:"scope"`
	State             State    `dynamodbav:"request_state"`
	ConfirmTokenHash  string   `dynamodbav:"confirm_token_hash,omitempty"`
	CancelTokenHashes []string `dynamodbav:"cancel_token_hashes,omitempty"` // one per e-mail that carried a cancel link
	LockApplied       bool     `dynamodbav:"lock_applied"`
	ScheduledSent     bool     `dynamodbav:"scheduled_sent"` // the first cancel link went out
	Reminded          bool     `dynamodbav:"reminded"`
	RequestedAt       string   `dynamodbav:"requested_at"`
	ConfirmBy         string   `dynamodbav:"confirm_by"`
	ConfirmedAt       string   `dynamodbav:"confirmed_at,omitempty"`
	GraceUntil        string   `dynamodbav:"grace_until,omitempty"`
	CancelledAt       string   `dynamodbav:"cancelled_at,omitempty"`
	LockedAt          string   `dynamodbav:"locked_at,omitempty"`
	UpdatedAt         string   `dynamodbav:"updated_at"`
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
