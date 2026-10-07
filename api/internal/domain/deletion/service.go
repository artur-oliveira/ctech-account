package deletion

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"gopkg.aoctech.app/account/api/internal/crypto"
	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/api-commons/observability"
)

type Users interface {
	GetByID(ctx context.Context, userID string) (*user.User, error)
}

// Locker applies the side effects of a request. Every method is idempotent:
// the worker re-runs them until the request records them as done.
type Locker interface {
	Lock(ctx context.Context, r *Request) error
	Unlock(ctx context.Context, r *Request) error
	Erase(ctx context.Context, r *Request) error
}

type Mailer interface {
	SendDeletionConfirmEmail(ctx context.Context, to, firstName, requestID, token string) error
	SendDeletionScheduledEmail(ctx context.Context, to, firstName, requestID, cancelToken string, graceUntil time.Time) error
	SendDeletionReminderEmail(ctx context.Context, to, firstName, requestID, cancelToken string, graceUntil time.Time) error
	SendDeletionCancelledEmail(ctx context.Context, to, firstName string) error
}

const dueBatch = 25

type Service struct {
	repo     Repository
	users    Users
	locker   Locker
	mail     Mailer
	now      func() time.Time
	newID    func() string
	newToken func() (raw, hash string, err error)
}

func NewService(repo Repository, users Users, locker Locker, mail Mailer) *Service {
	return &Service{
		repo: repo, users: users, locker: locker, mail: mail,
		now:      time.Now,
		newID:    func() string { return uuid.New().String() },
		newToken: crypto.GenerateMFAToken,
	}
}

// Request opens a deletion request awaiting e-mail confirmation. An earlier
// unconfirmed request is replaced, so a lost e-mail never blocks the user.
func (s *Service) Request(ctx context.Context, userID string) (*Request, error) {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	raw, hash, err := s.newToken()
	if err != nil {
		return nil, err
	}
	now := s.now()
	r := &Request{
		ID: s.newID(), UserID: userID, Scope: ScopeAccount, State: StateAwaitingConfirmation,
		ConfirmTokenHash: hash, RequestedAt: ts(now), ConfirmBy: ts(now.Add(ConfirmWindow)), UpdatedAt: ts(now),
	}
	r.PK, r.SK = BuildPK(r.ID), metaSK
	r.setDue(now.Add(ConfirmWindow))

	err = s.repo.Create(ctx, r)
	if errors.Is(err, ErrOpenRequest) {
		if err = s.expireUnconfirmed(ctx, userID); err == nil {
			err = s.repo.Create(ctx, r)
		}
	}
	if err != nil {
		return nil, err
	}
	if err := s.mail.SendDeletionConfirmEmail(ctx, u.Email, u.FirstName, r.ID, raw); err != nil {
		// Without the e-mail the request can never be confirmed: release it now.
		s.expire(r)
		if saveErr := s.repo.Save(ctx, r, StateAwaitingConfirmation); saveErr != nil {
			return nil, errors.Join(err, saveErr)
		}
		return nil, fmt.Errorf("deletion: sending confirmation e-mail: %w", err)
	}
	return r, nil
}

// expireUnconfirmed frees the open marker if it belongs to an unconfirmed
// request; any other open request keeps ErrOpenRequest.
func (s *Service) expireUnconfirmed(ctx context.Context, userID string) error {
	open, err := s.repo.GetOpen(ctx, userID)
	if err != nil {
		return err
	}
	if open.State != StateAwaitingConfirmation {
		return ErrOpenRequest
	}
	s.expire(open)
	return s.repo.Save(ctx, open, StateAwaitingConfirmation)
}

func (s *Service) expire(r *Request) {
	r.State, r.ConfirmTokenHash, r.UpdatedAt = StateExpired, "", ts(s.now())
	r.clearDue()
}

// Confirm turns an e-mail-confirmed request into a pending deletion and locks
// the account. A failed lock is retried by the worker (the request stays due).
func (s *Service) Confirm(ctx context.Context, id, token string) (*Request, error) {
	r, err := s.repo.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	if r.State != StateAwaitingConfirmation || !now.Before(parseTime(r.ConfirmBy)) || !tokenMatches(token, r.ConfirmTokenHash) {
		return nil, ErrInvalidToken
	}
	raw, hash, err := s.newToken()
	if err != nil {
		return nil, err
	}
	r.State, r.ConfirmTokenHash, r.CancelTokenHashes = StatePending, "", []string{hash}
	r.ConfirmedAt, r.GraceUntil, r.UpdatedAt = ts(now), ts(now.Add(GracePeriod)), ts(now)
	r.setDue(now) // lock not applied yet
	if err := s.repo.Save(ctx, r, StateAwaitingConfirmation); err != nil {
		if errors.Is(err, ErrStateChanged) {
			return nil, ErrInvalidToken
		}
		return nil, err
	}
	if err := s.applyLock(ctx, r); err != nil {
		observability.Error(ctx, "deletion: lock failed, worker will retry", err, "request_id", r.ID)
	}
	if u, err := s.users.GetByID(ctx, r.UserID); err == nil {
		if mErr := s.mail.SendDeletionScheduledEmail(ctx, u.Email, u.FirstName, r.ID, raw, parseTime(r.GraceUntil)); mErr != nil {
			observability.Error(ctx, "deletion: scheduled e-mail failed", mErr, "request_id", r.ID)
		}
	}
	return r, nil
}

// applyLock runs the lock, records it and schedules the reminder. If a cancel
// won the race while the lock was in flight, the lock is undone.
func (s *Service) applyLock(ctx context.Context, r *Request) error {
	if err := s.locker.Lock(ctx, r); err != nil {
		return err
	}
	r.LockApplied, r.UpdatedAt = true, ts(s.now())
	r.setDue(parseTime(r.GraceUntil).Add(-ReminderLead))
	err := s.repo.Save(ctx, r, StatePending)
	if errors.Is(err, ErrStateChanged) {
		if cur, getErr := s.repo.Get(ctx, r.ID); getErr == nil && cur.State == StateCancelled {
			return s.locker.Unlock(ctx, cur)
		}
	}
	return err
}

// Cancel ends a pending deletion. The state change wins first; the unlock is
// retried by the worker if it fails now.
func (s *Service) Cancel(ctx context.Context, id, token string) (*Request, error) {
	r, err := s.repo.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	if !anyTokenMatches(token, r.CancelTokenHashes) {
		return nil, ErrInvalidToken
	}
	if r.State != StatePending {
		return nil, ErrNotCancellable
	}
	now := s.now()
	r.State, r.CancelledAt, r.UpdatedAt = StateCancelled, ts(now), ts(now)
	r.setDue(now) // unlock not applied yet
	if err := s.repo.Save(ctx, r, StatePending); err != nil {
		if errors.Is(err, ErrStateChanged) {
			return nil, ErrNotCancellable
		}
		return nil, err
	}
	if err := s.finishCancel(ctx, r); err != nil {
		observability.Error(ctx, "deletion: unlock failed, worker will retry", err, "request_id", r.ID)
	}
	return r, nil
}

func (s *Service) finishCancel(ctx context.Context, r *Request) error {
	if err := s.locker.Unlock(ctx, r); err != nil {
		return err
	}
	r.UpdatedAt = ts(s.now())
	r.clearDue()
	if err := s.repo.Save(ctx, r, StateCancelled); err != nil {
		return err
	}
	if u, err := s.users.GetByID(ctx, r.UserID); err == nil {
		if mErr := s.mail.SendDeletionCancelledEmail(ctx, u.Email, u.FirstName); mErr != nil {
			observability.Error(ctx, "deletion: cancelled e-mail failed", mErr, "request_id", r.ID)
		}
	}
	return nil
}

// Status returns the user's open request, or ErrNotFound.
func (s *Service) Status(ctx context.Context, userID string) (*Request, error) {
	return s.repo.GetOpen(ctx, userID)
}

// ProcessDue advances every request whose next action is due. A failing
// request never blocks the others; it stays due and is retried next tick.
func (s *Service) ProcessDue(ctx context.Context) {
	now := s.now()
	for _, st := range []State{StateAwaitingConfirmation, StatePending, StateCancelled, StateLocked} {
		ids, err := s.repo.ListDue(ctx, st, now, dueBatch)
		if err != nil {
			observability.Error(ctx, "deletion: listing due requests", err, "state", string(st))
			continue
		}
		for _, id := range ids {
			r, err := s.repo.Get(ctx, id) // the index is eventually consistent
			if err == nil && r.DueState == string(st) && !now.Before(parseTime(r.NextActionAt)) {
				err = s.step(ctx, r, now)
			}
			if err != nil && !errors.Is(err, ErrStateChanged) {
				observability.Error(ctx, "deletion: step failed", err, "request_id", id, "state", string(st))
			}
		}
	}
}

func (s *Service) step(ctx context.Context, r *Request, now time.Time) error {
	switch r.State {
	case StateAwaitingConfirmation: // the confirmation window elapsed
		s.expire(r)
		return s.repo.Save(ctx, r, StateAwaitingConfirmation)
	case StateCancelled:
		return s.finishCancel(ctx, r)
	case StatePending:
		switch {
		case !r.LockApplied:
			return s.applyLock(ctx, r)
		case !r.Reminded && now.Before(parseTime(r.GraceUntil)):
			return s.remind(ctx, r, now)
		case !now.Before(parseTime(r.GraceUntil)):
			r.State, r.LockedAt, r.UpdatedAt = StateLocked, ts(now), ts(now)
			r.setDue(now)
			return s.repo.Save(ctx, r, StatePending)
		}
	case StateLocked:
		// Re-assert the lock: a cancel may have raced the end of grace.
		if err := s.locker.Lock(ctx, r); err != nil {
			return err
		}
		if err := s.locker.Erase(ctx, r); err != nil {
			return err
		}
		r.State, r.UpdatedAt = StatePurging, ts(now)
		r.clearDue() // Phase 2 adds the account's own purge from here
		return s.repo.Save(ctx, r, StateLocked)
	}
	return nil
}

func (s *Service) remind(ctx context.Context, r *Request, now time.Time) error {
	u, err := s.users.GetByID(ctx, r.UserID)
	if err != nil {
		return err
	}
	raw, hash, err := s.newToken()
	if err != nil {
		return err
	}
	r.CancelTokenHashes = append(r.CancelTokenHashes, hash)
	r.Reminded, r.UpdatedAt = true, ts(now)
	r.setDue(parseTime(r.GraceUntil))
	if err := s.repo.Save(ctx, r, StatePending); err != nil {
		return err
	}
	return s.mail.SendDeletionReminderEmail(ctx, u.Email, u.FirstName, r.ID, raw, parseTime(r.GraceUntil))
}

func tokenMatches(raw, hash string) bool {
	return hash != "" && subtle.ConstantTimeCompare([]byte(crypto.HashToken(raw)), []byte(hash)) == 1
}

func anyTokenMatches(raw string, hashes []string) bool {
	for _, h := range hashes {
		if tokenMatches(raw, h) {
			return true
		}
	}
	return false
}
