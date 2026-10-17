package deletion

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gopkg.aoctech.app/api-commons/erasure"

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
	Erase(ctx context.Context, r *Request, services []string) error
}

type Mailer interface {
	SendDeletionConfirmEmail(ctx context.Context, to, firstName, requestID, token string) error
	SendDeletionScheduledEmail(ctx context.Context, to, firstName, requestID, cancelToken string, graceUntil time.Time) error
	SendDeletionReminderEmail(ctx context.Context, to, firstName, requestID, cancelToken string, graceUntil time.Time) error
	SendDeletionCancelledEmail(ctx context.Context, to, firstName string) error
	SendDeletionBlockedEmail(ctx context.Context, to, firstName, requestID, cancelToken string, blockers []string) error
}

// Ownership finds what stops a user's deletion and which organizations go
// with them (they own it and are its only member).
type Ownership interface {
	Assess(ctx context.Context, userID string) (sole []Workspace, blockers []erasure.Blocker, err error)
}

// Purger erases ctech-account's own data for a request. Idempotent and
// resumable: every step tolerates already-removed data.
type Purger interface {
	Purge(ctx context.Context, r *Request) error
}

// BlockedError lists what the user must resolve before deletion can start.
type BlockedError struct{ Blockers []erasure.Blocker }

func (e *BlockedError) Error() string { return fmt.Sprintf("deletion: %d blocker(s)", len(e.Blockers)) }

type noOwnership struct{}

func (noOwnership) Assess(context.Context, string) ([]Workspace, []erasure.Blocker, error) {
	return nil, nil, nil
}

const dueBatch = 25

// legalHoldRecheck is how often a held request is looked at again.
const legalHoldRecheck = time.Hour

type Service struct {
	repo     Repository
	users    Users
	locker   Locker
	mail     Mailer
	now      func() time.Time
	newID    func() string
	newToken func() (raw, hash string, err error)

	ownership    Ownership
	purger       Purger
	participants []string

	// leavable and serviceElig serve scope service: which products exist, and
	// what blocks leaving one of them.
	leavable    func(name string) (ServiceTarget, bool)
	serviceElig ServiceEligibility
}

// ServiceTarget is a product a person can leave on its own.
type ServiceTarget struct {
	Service      string
	LoginClients []string
}

// ServiceEligibility asks one participant what blocks the person leaving it.
type ServiceEligibility interface {
	CheckService(ctx context.Context, userID, service string) ([]erasure.Blocker, error)
}

// WithServices enables scope service. leavable says which products can be left.
func (s *Service) WithServices(leavable func(name string) (ServiceTarget, bool), e ServiceEligibility) *Service {
	s.leavable, s.serviceElig = leavable, e
	return s
}

func NewService(repo Repository, users Users, locker Locker, mail Mailer) *Service {
	return &Service{
		repo: repo, users: users, locker: locker, mail: mail,
		now:       time.Now,
		ownership: noOwnership{},
		newID:     func() string { return uuid.New().String() },
		newToken:  crypto.GenerateMFAToken,
	}
}

// WithOwnership enables blocker checks and organization freezing.
func (s *Service) WithOwnership(o Ownership) *Service { s.ownership = o; return s }

// WithPurger enables the account purge after the erase is published.
func (s *Service) WithPurger(p Purger) *Service {
	s.purger = p
	return s
}

// WithParticipants makes purging wait for an ack from each of these services
// (saga protocol §7). Empty means no participants: purge right away.
func (s *Service) WithParticipants(services []string) *Service {
	s.participants = services
	return s
}

// Request opens an account-deletion request awaiting e-mail confirmation. An
// earlier unconfirmed request is replaced, so a lost e-mail never blocks the user.
func (s *Service) Request(ctx context.Context, userID string) (*Request, error) {
	return s.open(ctx, userID, ScopeAccount, "")
}

// RequestService opens a request to leave one product, with the same lifecycle.
// Only that participant is asked, and no workspace is involved (spec U1, U2).
func (s *Service) RequestService(ctx context.Context, userID, service string) (*Request, error) {
	return s.open(ctx, userID, ScopeService, service)
}

func (s *Service) open(ctx context.Context, userID, scope, service string) (*Request, error) {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	var blockers []erasure.Blocker
	if scope == ScopeService {
		if s.leavable == nil || s.serviceElig == nil {
			return nil, ErrUnknownService
		}
		if _, ok := s.leavable(service); !ok {
			return nil, ErrUnknownService
		}
		if blockers, err = s.serviceElig.CheckService(ctx, userID, service); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrEligibilityUnavailable, err)
		}
	} else if _, blockers, err = s.ownership.Assess(ctx, userID); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrEligibilityUnavailable, err)
	}
	if len(blockers) > 0 {
		return nil, &BlockedError{Blockers: blockers}
	}
	raw, hash, err := s.newToken()
	if err != nil {
		return nil, err
	}
	now := s.now()
	r := &Request{
		ID: s.newID(), UserID: userID, Scope: scope, Service: service, State: StateAwaitingConfirmation,
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
	r.State, r.ConfirmTokenHash = StatePending, ""
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
	if err := s.sendCancelLink(ctx, r, linkScheduled); err != nil {
		observability.Error(ctx, "deletion: cancel-link e-mail failed, worker will retry", err, "request_id", r.ID)
	}
	return r, nil
}

// nextDue is when the worker must next look at a pending request: now while
// the lock or the first cancel link is still owed, then the reminder, then
// the end of grace.
func (s *Service) nextDue(r *Request) time.Time {
	switch {
	case !r.LockApplied || !r.ScheduledSent:
		return s.now()
	case !r.Reminded:
		return parseTime(r.GraceUntil).Add(-ReminderLead)
	default:
		return parseTime(r.GraceUntil)
	}
}

type linkKind int

const (
	linkScheduled linkKind = iota
	linkReminder
	linkBlocked
)

// sendCancelLink e-mails a fresh cancel link (scheduled notice, reminder, or
// blocked notice) and records it. Under ruling R1 the e-mail is the only way
// to cancel, so the token hash is stored before sending and the "sent" flag
// only after: a failed send stays due and is retried, at worst as a duplicate.
func (s *Service) sendCancelLink(ctx context.Context, r *Request, kind linkKind) error {
	u, err := s.users.GetByID(ctx, r.UserID)
	if err != nil {
		return err
	}
	raw, hash, err := s.newToken()
	if err != nil {
		return err
	}
	from := r.State
	r.CancelTokenHashes = append(r.CancelTokenHashes, hash)
	r.UpdatedAt = ts(s.now())
	if err := s.repo.Save(ctx, r, from); err != nil {
		return err
	}
	switch kind {
	case linkScheduled:
		err = s.mail.SendDeletionScheduledEmail(ctx, u.Email, u.FirstName, r.ID, raw, parseTime(r.GraceUntil))
	case linkReminder:
		err = s.mail.SendDeletionReminderEmail(ctx, u.Email, u.FirstName, r.ID, raw, parseTime(r.GraceUntil))
	case linkBlocked:
		err = s.mail.SendDeletionBlockedEmail(ctx, u.Email, u.FirstName, r.ID, raw, r.Blockers)
	}
	if err != nil {
		return err
	}
	switch kind {
	case linkScheduled:
		r.ScheduledSent = true
	case linkReminder:
		r.Reminded = true
	case linkBlocked:
		r.BlockedNotified = true
	}
	r.UpdatedAt = ts(s.now())
	s.scheduleNext(r)
	return s.repo.Save(ctx, r, from)
}

// scheduleNext sets (or clears) the due marker for a pending or blocked request.
func (s *Service) scheduleNext(r *Request) {
	if r.State == StateBlocked {
		if r.BlockedNotified {
			r.clearDue() // nothing to do until the user cancels
		} else {
			r.setDue(s.now())
		}
		return
	}
	r.setDue(s.nextDue(r))
}

// applyLock runs the lock, records it and schedules the reminder. If a cancel
// won the race while the lock was in flight, the lock is undone.
func (s *Service) applyLock(ctx context.Context, r *Request) error {
	if err := s.locker.Lock(ctx, r); err != nil {
		return err
	}
	r.LockApplied, r.UpdatedAt = true, ts(s.now())
	s.scheduleNext(r)
	err := s.repo.Save(ctx, r, StatePending)
	if errors.Is(err, ErrStateChanged) {
		if cur, getErr := s.repo.Get(ctx, r.ID); getErr == nil && cur.State == StateCancelled {
			if uErr := s.locker.Unlock(ctx, cur); uErr != nil {
				// Keep the cancel due so the worker retries the unlock.
				cur.UpdatedAt = ts(s.now())
				cur.setDue(s.now())
				return errors.Join(uErr, s.repo.Save(ctx, cur, StateCancelled))
			}
			return nil
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
	return s.cancel(ctx, r)
}

// cancel moves a pending or blocked request to cancelled and unlocks the
// account; shared by the link cancel and the support cancel.
func (s *Service) cancel(ctx context.Context, r *Request) (*Request, error) {
	if r.State != StatePending && r.State != StateBlocked {
		return nil, ErrNotCancellable
	}
	from := r.State
	now := s.now()
	r.State, r.CancelledAt, r.UpdatedAt = StateCancelled, ts(now), ts(now)
	r.setDue(now) // unlock not applied yet
	if err := s.repo.Save(ctx, r, from); err != nil {
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
	for _, st := range []State{StateAwaitingConfirmation, StatePending, StateBlocked, StateCancelled, StateLocked, StatePurging} {
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
		case !r.ScheduledSent:
			return s.sendCancelLink(ctx, r, linkScheduled)
		case !r.Reminded && now.Before(parseTime(r.GraceUntil)):
			return s.sendCancelLink(ctx, r, linkReminder)
		case !now.Before(parseTime(r.GraceUntil)):
			if r.LegalHold {
				r.setDue(now.Add(legalHoldRecheck))
				return s.repo.Save(ctx, r, StatePending)
			}
			sole, blockers, err := s.ownership.Assess(ctx, r.UserID)
			if err != nil {
				return err
			}
			if len(blockers) > 0 {
				r.State, r.UpdatedAt = StateBlocked, ts(now)
				r.Blockers = make([]string, 0, len(blockers))
				for _, b := range blockers {
					r.Blockers = append(r.Blockers, b.Code)
				}
				r.setDue(now) // the blocked notice is owed
				return s.repo.Save(ctx, r, StatePending)
			}
			r.State, r.LockedAt, r.UpdatedAt = StateLocked, ts(now), ts(now)
			r.Workspaces = sole
			r.Organizations = nil
			for _, w := range sole {
				r.Organizations = append(r.Organizations, w.ID)
			}
			r.setDue(now)
			return s.repo.Save(ctx, r, StatePending)
		}
	case StateBlocked:
		return s.sendCancelLink(ctx, r, linkBlocked)
	case StateLocked:
		// Re-assert the lock: a cancel may have raced the end of grace.
		if err := s.locker.Lock(ctx, r); err != nil {
			return err
		}
		if err := s.locker.Erase(ctx, r, nil); err != nil {
			return err
		}
		if len(s.participants) > 0 {
			rows := make([]*ServiceStatus, 0, len(s.participants))
			for _, svc := range s.participants {
				rows = append(rows, &ServiceStatus{PK: BuildPK(r.ID), SK: serviceSK(svc), RequestID: r.ID, Service: svc,
					Status: ServiceDispatched, Attempts: 1, FirstDispatchAt: ts(now), LastDispatchAt: ts(now)})
			}
			if err := s.repo.PutServices(ctx, rows); err != nil {
				return err
			}
		}
		r.State, r.UpdatedAt = StatePurging, ts(now)
		if s.purger != nil || len(s.participants) > 0 {
			r.setDue(now)
		} else {
			r.clearDue() // nothing to wait for and nothing to purge yet
		}
		return s.repo.Save(ctx, r, StateLocked)
	case StatePurging:
		if r.LegalHold {
			r.setDue(now.Add(legalHoldRecheck))
			return s.repo.Save(ctx, r, StatePurging)
		}
		if len(s.participants) > 0 {
			done, err := s.reconcile(ctx, r, now)
			if err != nil || !done {
				return err
			}
		}
		if s.purger == nil {
			r.clearDue()
			return s.repo.Save(ctx, r, StatePurging)
		}
		if err := s.purger.Purge(ctx, r); err != nil {
			return err
		}
		r.State, r.PurgedAt, r.UpdatedAt = StatePurged, ts(now), ts(now)
		r.clearDue()
		return s.repo.Save(ctx, r, StatePurging)
	}
	return nil
}

// Ack records a participant's result for a request. A duplicate is a no-op;
// an ack before the status row exists fails with ErrNotFound, so the
// participant's message is redelivered.
func (s *Service) Ack(ctx context.Context, service string, ack erasure.Ack) error {
	sts, err := s.repo.ListServices(ctx, ack.RequestID)
	if err != nil {
		return err
	}
	for _, st := range sts {
		if st.Service != service {
			continue
		}
		if st.Status != ServiceDispatched {
			return nil // already recorded
		}
		st.AckedAt, st.Counts = ts(s.now()), ack.Counts
		st.Status = ServiceAcked
		if ack.Result == erasure.ResultBlocked {
			st.Status = ServiceBlocked
			for _, b := range ack.Blockers {
				st.Blockers = append(st.Blockers, b.Code)
			}
		}
		if err := s.repo.SaveService(ctx, st, ServiceDispatched); err != nil && !errors.Is(err, ErrStateChanged) {
			return err
		}
		if r, err := s.repo.Get(ctx, ack.RequestID); err == nil && r.State == StatePurging {
			r.setDue(s.now()) // let the worker act on it now
			r.UpdatedAt = ts(s.now())
			_ = s.repo.Save(ctx, r, StatePurging) // a lost race only delays to the next due time
		}
		return nil
	}
	return ErrNotFound
}

// Subject returns the CPF of the user of a purging request (saga protocol
// §4.4). ErrNotFound for any other state, so it cannot be used as a lookup.
func (s *Service) Subject(ctx context.Context, requestID string) (string, error) {
	r, err := s.repo.Get(ctx, requestID)
	if err != nil {
		return "", err
	}
	if r.State != StatePurging {
		return "", ErrNotFound
	}
	u, err := s.users.GetByID(ctx, r.UserID)
	if err != nil {
		return "", err
	}
	return u.CPF, nil
}

var ErrHoldNeedsSecondAdmin = errors.New("deletion: a legal hold is lifted by a different admin")

// SetLegalHold pauses (or resumes) a request before it locks or purges.
func (s *Service) SetLegalHold(ctx context.Context, requestID, adminID string, on bool) error {
	r, err := s.repo.Get(ctx, requestID)
	if err != nil {
		return err
	}
	if !on && r.LegalHold && r.LegalHoldBy == adminID {
		return ErrHoldNeedsSecondAdmin
	}
	from := r.State
	r.LegalHold, r.UpdatedAt = on, ts(s.now())
	r.LegalHoldBy = ""
	if on {
		r.LegalHoldBy = adminID
	}
	if !on && (r.State == StatePending || r.State == StatePurging) {
		r.setDue(s.now()) // resume promptly
	}
	return s.repo.Save(ctx, r, from)
}

// SupportCancel cancels on the user's behalf (R1: support is the fallback
// when the user lost every e-mail). Same rules as a link cancel.
func (s *Service) SupportCancel(ctx context.Context, requestID, _ string) (*Request, error) {
	r, err := s.repo.Get(ctx, requestID)
	if err != nil {
		return nil, err
	}
	return s.cancel(ctx, r)
}

// Redrive puts a stalled request back to purging after support resolved the
// participant's blocker: that participant is dispatched again.
func (s *Service) Redrive(ctx context.Context, requestID, service, _ string) error {
	r, err := s.repo.Get(ctx, requestID)
	if err != nil {
		return err
	}
	sts, err := s.repo.ListServices(ctx, requestID)
	if err != nil {
		return err
	}
	now := s.now()
	for _, st := range sts {
		if st.Service == service && st.Status == ServiceBlocked {
			st.Status, st.Blockers, st.AckedAt = ServiceDispatched, nil, ""
			st.Attempts, st.FirstDispatchAt, st.LastDispatchAt = 0, ts(now), ""
			if err := s.repo.SaveService(ctx, st, ServiceBlocked); err != nil {
				return err
			}
		}
	}
	if r.State == StateStalled {
		r.State, r.UpdatedAt = StatePurging, ts(now)
		r.setDue(now)
		return s.repo.Save(ctx, r, StateStalled)
	}
	return nil
}

var ackBackoff = []time.Duration{15 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour}

const ackOverdueAfter = 48 * time.Hour

func nextDispatch(st *ServiceStatus) time.Time {
	if st.Attempts == 0 {
		return time.Time{} // redriven: due immediately
	}
	i := min(st.Attempts-1, len(ackBackoff)-1)
	return parseTime(st.LastDispatchAt).Add(ackBackoff[i])
}

// reconcile re-publishes to silent participants on their backoff and reports
// whether every participant acked. A blocked ack stalls the request.
func (s *Service) reconcile(ctx context.Context, r *Request, now time.Time) (bool, error) {
	sts, err := s.repo.ListServices(ctx, r.ID)
	if err != nil {
		return false, err
	}
	var silent []*ServiceStatus
	next := time.Time{}
	for _, st := range sts {
		switch st.Status {
		case ServiceAcked:
		case ServiceBlocked:
			r.State, r.UpdatedAt = StateStalled, ts(now)
			r.clearDue()
			return false, s.repo.Save(ctx, r, StatePurging)
		default:
			if now.Sub(parseTime(st.FirstDispatchAt)) > ackOverdueAfter {
				observability.Error(ctx, "deletion: participant ack overdue", errors.New("no ack after 48h"),
					"request_id", r.ID, "service", st.Service)
			}
			if n := nextDispatch(st); !now.Before(n) {
				silent = append(silent, st)
			} else if next.IsZero() || n.Before(next) {
				next = n
			}
		}
	}
	if len(silent) == 0 && next.IsZero() {
		return true, nil
	}
	if len(silent) > 0 {
		names := make([]string, 0, len(silent))
		for _, st := range silent {
			names = append(names, st.Service)
		}
		if err := s.locker.Erase(ctx, r, names); err != nil {
			return false, err
		}
		for _, st := range silent {
			st.Attempts++
			st.LastDispatchAt = ts(now)
			if err := s.repo.SaveService(ctx, st, ServiceDispatched); err != nil && !errors.Is(err, ErrStateChanged) {
				return false, err
			}
			if n := nextDispatch(st); next.IsZero() || n.Before(next) {
				next = n
			}
		}
	}
	r.UpdatedAt = ts(now)
	r.setDue(next)
	return false, s.repo.Save(ctx, r, StatePurging)
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
