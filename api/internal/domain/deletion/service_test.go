package deletion

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gopkg.aoctech.app/account/api/internal/domain/user"
)

// memRepo mirrors dynamoRepository's conditional semantics.
type memRepo struct {
	mu         sync.Mutex
	reqs       map[string]Request
	markers    map[string]string // userID -> open request id
	beforeSave func()            // runs once, before the next Save's condition check
}

func newMemRepo() *memRepo { return &memRepo{reqs: map[string]Request{}, markers: map[string]string{}} }

func clone(r Request) Request {
	r.CancelTokenHashes = append([]string(nil), r.CancelTokenHashes...)
	return r
}

func (m *memRepo) Create(_ context.Context, r *Request) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.markers[r.UserID]; ok {
		return ErrOpenRequest
	}
	m.markers[r.UserID] = r.ID
	m.reqs[r.ID] = clone(*r)
	return nil
}

func (m *memRepo) Get(_ context.Context, id string) (*Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.reqs[id]
	if !ok {
		return nil, ErrNotFound
	}
	c := clone(r)
	return &c, nil
}

func (m *memRepo) GetOpen(ctx context.Context, userID string) (*Request, error) {
	m.mu.Lock()
	id, ok := m.markers[userID]
	m.mu.Unlock()
	if !ok {
		return nil, ErrNotFound
	}
	return m.Get(ctx, id)
}

func (m *memRepo) Save(_ context.Context, r *Request, from State) error {
	if h := m.beforeSave; h != nil {
		m.beforeSave = nil
		h()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.reqs[r.ID]
	if !ok || cur.State != from {
		return ErrStateChanged
	}
	m.reqs[r.ID] = clone(*r)
	if from.Open() && !r.State.Open() && m.markers[r.UserID] == r.ID {
		delete(m.markers, r.UserID)
	}
	return nil
}

func (m *memRepo) ListDue(_ context.Context, st State, now time.Time, _ int32) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []string
	for id, r := range m.reqs {
		if r.DueState == string(st) && r.NextActionAt <= ts(now) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

type fakeUsers struct{ u *user.User }

func (f fakeUsers) GetByID(context.Context, string) (*user.User, error) { return f.u, nil }

type fakeLocker struct {
	locks, unlocks, erases int
	lockErr, unlockErr     error
	onLock                 func()
}

func (f *fakeLocker) Lock(context.Context, *Request) error {
	f.locks++
	if h := f.onLock; h != nil {
		f.onLock = nil
		h()
	}
	return f.lockErr
}
func (f *fakeLocker) Unlock(context.Context, *Request) error { f.unlocks++; return f.unlockErr }
func (f *fakeLocker) Erase(context.Context, *Request) error  { f.erases++; return nil }

type fakeMailer struct {
	confirmToken, cancelToken           string
	reminders, cancelled                int
	confirmErr, scheduledErr, remindErr error
}

func (f *fakeMailer) SendDeletionConfirmEmail(_ context.Context, _, _, _, token string) error {
	f.confirmToken = token
	return f.confirmErr
}
func (f *fakeMailer) SendDeletionScheduledEmail(_ context.Context, _, _, _, token string, _ time.Time) error {
	if f.scheduledErr != nil {
		return f.scheduledErr
	}
	f.cancelToken = token
	return nil
}
func (f *fakeMailer) SendDeletionReminderEmail(_ context.Context, _, _, _, token string, _ time.Time) error {
	if f.remindErr != nil {
		return f.remindErr
	}
	f.reminders++
	f.cancelToken = token
	return nil
}
func (f *fakeMailer) SendDeletionCancelledEmail(context.Context, string, string) error {
	f.cancelled++
	return nil
}

type fixture struct {
	svc    *Service
	repo   *memRepo
	locker *fakeLocker
	mail   *fakeMailer
	now    time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{repo: newMemRepo(), locker: &fakeLocker{}, mail: &fakeMailer{},
		now: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	f.svc = NewService(f.repo, fakeUsers{u: &user.User{Email: "ana@example.com", FirstName: "Ana"}}, f.locker, f.mail)
	f.svc.now = func() time.Time { return f.now }
	return f
}

var ctx = context.Background()

func (f *fixture) request(t *testing.T) *Request {
	t.Helper()
	r, err := f.svc.Request(ctx, "u1")
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	return r
}

func (f *fixture) confirm(t *testing.T) *Request {
	t.Helper()
	r := f.request(t)
	r, err := f.svc.Confirm(ctx, r.ID, f.mail.confirmToken)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	return r
}

func (f *fixture) stored(t *testing.T, id string) *Request {
	t.Helper()
	r, err := f.repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return r
}

func TestRequest_CreatesAwaitingAndEmailsConfirmLink(t *testing.T) {
	f := newFixture(t)
	r := f.request(t)
	if r.State != StateAwaitingConfirmation || f.mail.confirmToken == "" || r.ConfirmBy != ts(f.now.Add(ConfirmWindow)) {
		t.Fatalf("unexpected request %+v (token %q)", r, f.mail.confirmToken)
	}
	if f.locker.locks != 0 {
		t.Fatal("nothing may be locked before the e-mail confirmation")
	}
}

func TestRequest_ReplacesUnconfirmedRequest(t *testing.T) {
	f := newFixture(t)
	first := f.request(t)
	second := f.request(t)
	if second.ID == first.ID || f.stored(t, first.ID).State != StateExpired {
		t.Fatalf("first request must expire and be replaced: first=%+v", f.stored(t, first.ID))
	}
	if open, err := f.svc.Status(ctx, "u1"); err != nil || open.ID != second.ID {
		t.Fatalf("Status = %v, %v; want the second request", open, err)
	}
}

func TestRequest_RejectsWhilePending(t *testing.T) {
	f := newFixture(t)
	f.confirm(t)
	if _, err := f.svc.Request(ctx, "u1"); !errors.Is(err, ErrOpenRequest) {
		t.Fatalf("err = %v, want ErrOpenRequest", err)
	}
}

func TestRequest_MailFailureReleasesMarker(t *testing.T) {
	f := newFixture(t)
	f.mail.confirmErr = errors.New("ses down")
	if _, err := f.svc.Request(ctx, "u1"); err == nil {
		t.Fatal("a request whose confirmation e-mail failed must fail")
	}
	f.mail.confirmErr = nil
	f.request(t)
}

func TestConfirm_LocksAndSchedulesGrace(t *testing.T) {
	f := newFixture(t)
	r := f.confirm(t)
	s := f.stored(t, r.ID)
	if s.State != StatePending || !s.LockApplied || f.locker.locks != 1 {
		t.Fatalf("stored %+v locks %d", s, f.locker.locks)
	}
	if s.GraceUntil != ts(f.now.Add(GracePeriod)) || s.NextActionAt != ts(f.now.Add(GracePeriod-ReminderLead)) {
		t.Fatalf("grace %s next %s", s.GraceUntil, s.NextActionAt)
	}
	if f.mail.cancelToken == "" {
		t.Fatal("the scheduled e-mail must carry a cancel link")
	}
}

func TestConfirm_RejectsBadTokenExpiredAndSecondClick(t *testing.T) {
	f := newFixture(t)
	r := f.request(t)
	tok := f.mail.confirmToken
	if _, err := f.svc.Confirm(ctx, r.ID, "wrong"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("wrong token: %v", err)
	}
	f.now = f.now.Add(ConfirmWindow)
	if _, err := f.svc.Confirm(ctx, r.ID, tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired link: %v", err)
	}
	f.now = f.now.Add(-ConfirmWindow)
	if _, err := f.svc.Confirm(ctx, r.ID, tok); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if _, err := f.svc.Confirm(ctx, r.ID, tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("second click: %v", err)
	}
	if f.locker.locks != 1 {
		t.Fatalf("locks = %d, want 1", f.locker.locks)
	}
}

func TestConfirm_LockFailureRetriedByWorker(t *testing.T) {
	f := newFixture(t)
	f.locker.lockErr = errors.New("valkey down")
	r := f.confirm(t)
	if f.stored(t, r.ID).LockApplied {
		t.Fatal("a failed lock must not be recorded as applied")
	}
	f.locker.lockErr = nil
	f.svc.ProcessDue(ctx)
	if !f.stored(t, r.ID).LockApplied || f.locker.locks != 2 {
		t.Fatalf("worker must re-apply the lock: locks=%d", f.locker.locks)
	}
}

func TestCancel_UnlocksAndAllowsNewRequest(t *testing.T) {
	f := newFixture(t)
	r := f.confirm(t)
	c, err := f.svc.Cancel(ctx, r.ID, f.mail.cancelToken)
	if err != nil || c.State != StateCancelled {
		t.Fatalf("Cancel = %+v, %v", c, err)
	}
	if f.locker.unlocks != 1 || f.stored(t, r.ID).DueState != "" || f.mail.cancelled != 1 {
		t.Fatalf("unlocks=%d due=%q cancelled-mails=%d", f.locker.unlocks, f.stored(t, r.ID).DueState, f.mail.cancelled)
	}
	f.request(t)
}

func TestCancel_ReminderLinkWorks(t *testing.T) {
	f := newFixture(t)
	r := f.confirm(t)
	f.now = f.now.Add(GracePeriod - ReminderLead)
	f.svc.ProcessDue(ctx)
	if f.mail.reminders != 1 {
		t.Fatalf("reminders = %d", f.mail.reminders)
	}
	if _, err := f.svc.Cancel(ctx, r.ID, f.mail.cancelToken); err != nil {
		t.Fatalf("cancel with reminder link: %v", err)
	}
}

func TestCancel_RejectsWrongTokenAndAfterLock(t *testing.T) {
	f := newFixture(t)
	r := f.confirm(t)
	tok := f.mail.cancelToken
	if _, err := f.svc.Cancel(ctx, r.ID, "wrong"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("wrong token: %v", err)
	}
	f.now = f.now.Add(GracePeriod)
	f.svc.ProcessDue(ctx) // one tick: pending -> locked -> purging
	if f.stored(t, r.ID).State != StatePurging {
		t.Fatalf("state = %s, want purging after grace end", f.stored(t, r.ID).State)
	}
	if _, err := f.svc.Cancel(ctx, r.ID, tok); !errors.Is(err, ErrNotCancellable) {
		t.Fatalf("cancel after lock: %v", err)
	}
}

func TestProcessDue_FullTimeline(t *testing.T) {
	f := newFixture(t)
	r := f.confirm(t)
	f.now = f.now.Add(GracePeriod - ReminderLead)
	f.svc.ProcessDue(ctx)
	if s := f.stored(t, r.ID); !s.Reminded || s.State != StatePending {
		t.Fatalf("after reminder: %+v", s)
	}
	f.now = f.now.Add(ReminderLead)
	f.svc.ProcessDue(ctx) // states are walked in order, so pending -> locked -> purging in one tick
	if s := f.stored(t, r.ID); s.State != StatePurging || s.DueState != "" {
		t.Fatalf("after erase: %+v", s)
	}
	if f.locker.erases != 1 || f.locker.locks != 2 {
		t.Fatalf("erases=%d locks=%d, want 1 and 2 (lock re-asserted before erase)", f.locker.erases, f.locker.locks)
	}
}

func TestProcessDue_ExpiresUnconfirmed(t *testing.T) {
	f := newFixture(t)
	r := f.request(t)
	f.now = f.now.Add(ConfirmWindow)
	f.svc.ProcessDue(ctx)
	if f.stored(t, r.ID).State != StateExpired {
		t.Fatal("an unconfirmed request must expire")
	}
	f.request(t)
}

func TestProcessDue_CancelRetriesUnlock(t *testing.T) {
	f := newFixture(t)
	r := f.confirm(t)
	f.locker.unlockErr = errors.New("valkey down")
	if _, err := f.svc.Cancel(ctx, r.ID, f.mail.cancelToken); err != nil {
		t.Fatalf("Cancel must succeed even if the unlock is deferred: %v", err)
	}
	if f.stored(t, r.ID).DueState != string(StateCancelled) {
		t.Fatal("a failed unlock must stay due")
	}
	f.locker.unlockErr = nil
	f.svc.ProcessDue(ctx)
	if f.stored(t, r.ID).DueState != "" || f.locker.unlocks != 2 {
		t.Fatalf("worker must retry the unlock: unlocks=%d", f.locker.unlocks)
	}
}

func TestApplyLock_CancelRaceCompensates(t *testing.T) {
	f := newFixture(t)
	f.locker.lockErr = errors.New("valkey down")
	r := f.confirm(t)
	f.locker.lockErr = nil
	cancelToken := f.mail.cancelToken
	// While the worker's retried Lock is in flight, the user cancels.
	f.locker.onLock = func() {
		if _, err := f.svc.Cancel(ctx, r.ID, cancelToken); err != nil {
			t.Errorf("Cancel: %v", err)
		}
	}
	f.svc.ProcessDue(ctx)
	if f.stored(t, r.ID).State != StateCancelled || f.locker.unlocks != 2 {
		t.Fatalf("state=%s unlocks=%d, want cancelled and the stale lock undone", f.stored(t, r.ID).State, f.locker.unlocks)
	}
}

// Under ruling R1 the e-mail is the only way to cancel: a failed send of the
// cancel link must be retried, never forgotten.
func TestConfirm_ScheduledEmailFailureRetried(t *testing.T) {
	f := newFixture(t)
	f.mail.scheduledErr = errors.New("ses down")
	r := f.confirm(t)
	if s := f.stored(t, r.ID); s.ScheduledSent || s.NextActionAt != ts(f.now) {
		t.Fatalf("a failed cancel-link e-mail must stay due now: %+v", s)
	}
	f.mail.scheduledErr = nil
	f.svc.ProcessDue(ctx)
	if !f.stored(t, r.ID).ScheduledSent || f.mail.cancelToken == "" {
		t.Fatal("worker must re-send the cancel link")
	}
	if _, err := f.svc.Cancel(ctx, r.ID, f.mail.cancelToken); err != nil {
		t.Fatalf("re-sent cancel link must work: %v", err)
	}
}

func TestRemind_FailureRetried(t *testing.T) {
	f := newFixture(t)
	r := f.confirm(t)
	f.now = f.now.Add(GracePeriod - ReminderLead)
	f.mail.remindErr = errors.New("ses down")
	f.svc.ProcessDue(ctx)
	if f.stored(t, r.ID).Reminded {
		t.Fatal("a failed reminder must not be recorded as sent")
	}
	f.mail.remindErr = nil
	f.svc.ProcessDue(ctx)
	if !f.stored(t, r.ID).Reminded || f.mail.reminders != 1 {
		t.Fatalf("reminder must be retried: reminded=%v sent=%d", f.stored(t, r.ID).Reminded, f.mail.reminders)
	}
}

// If undoing a stale lock fails, the cancelled request must stay due so the
// worker retries the unlock; otherwise the user is locked out for good.
func TestApplyLock_CompensationFailureRetried(t *testing.T) {
	f := newFixture(t)
	f.locker.lockErr = errors.New("valkey down")
	r := f.confirm(t)
	f.locker.lockErr = nil
	cancelToken := f.mail.cancelToken
	f.locker.onLock = func() {
		if _, err := f.svc.Cancel(ctx, r.ID, cancelToken); err != nil {
			t.Errorf("Cancel: %v", err)
		}
		f.locker.unlockErr = errors.New("valkey down") // the compensating unlock fails
	}
	f.svc.ProcessDue(ctx)
	if s := f.stored(t, r.ID); s.State != StateCancelled || s.DueState != string(StateCancelled) {
		t.Fatalf("failed compensation must leave the cancel due: %+v", s)
	}
	f.locker.unlockErr = nil
	f.svc.ProcessDue(ctx)
	if f.stored(t, r.ID).DueState != "" {
		t.Fatal("worker must finish the unlock")
	}
}
