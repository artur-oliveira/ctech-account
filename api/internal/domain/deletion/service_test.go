package deletion

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/api-commons/erasure"
)

// memRepo mirrors dynamoRepository's conditional semantics.
type memRepo struct {
	mu         sync.Mutex
	reqs       map[string]Request
	markers    map[string]string // userID -> open request id
	services   map[string]ServiceStatus
	beforeSave func() // runs once, before the next Save's condition check
}

func newMemRepo() *memRepo {
	return &memRepo{reqs: map[string]Request{}, markers: map[string]string{}, services: map[string]ServiceStatus{}}
}

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
	lastErase              []string
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
func (f *fakeLocker) Erase(_ context.Context, _ *Request, services []string) error {
	f.erases++
	f.lastErase = services
	return nil
}

type fakeMailer struct {
	blocked                             int
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

func (f *fakeMailer) SendDeletionBlockedEmail(_ context.Context, _, _, _, token string, _ []string) error {
	f.blocked++
	f.cancelToken = token
	return nil
}

type fakeOwnership struct {
	sole     []Workspace
	blockers []erasure.Blocker
}

func (f *fakeOwnership) Assess(context.Context, string) ([]Workspace, []erasure.Blocker, error) {
	return f.sole, f.blockers, nil
}

type fakePurger struct {
	calls int
	err   error
}

func (f *fakePurger) Purge(context.Context, *Request) error {
	f.calls++
	return f.err
}

func (f *fixture) withPurge(t *testing.T, own *fakeOwnership, p *fakePurger, await bool) {
	t.Helper()
	f.svc = f.svc.WithOwnership(own).WithPurger(p)
	if await {
		f.svc = f.svc.WithParticipants([]string{"wallet"})
	}
}

func TestRequest_RefusedWithBlockers(t *testing.T) {
	f := newFixture(t)
	f.withPurge(t, &fakeOwnership{blockers: []erasure.Blocker{{Code: "account.oauth_client_owned"}}}, &fakePurger{}, false)
	_, err := f.svc.Request(ctx, "u1")
	var blocked *BlockedError
	if !errors.As(err, &blocked) || len(blocked.Blockers) != 1 {
		t.Fatalf("err = %v, want BlockedError with 1 blocker", err)
	}
	if _, err := f.svc.Status(ctx, "u1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("a refused request must not be stored")
	}
}

func TestGraceEnd_BlockedNotifiesAndCanCancel(t *testing.T) {
	f := newFixture(t)
	own := &fakeOwnership{}
	f.withPurge(t, own, &fakePurger{}, false)
	r := f.confirm(t)
	own.blockers = []erasure.Blocker{{Code: "account.organization_shared_owner"}}
	f.now = f.now.Add(GracePeriod)
	f.svc.ProcessDue(ctx)
	s := f.stored(t, r.ID)
	if s.State != StateBlocked || !s.BlockedNotified || s.DueState != "" || len(s.Blockers) != 1 || f.mail.blocked != 1 {
		t.Fatalf("blocked request %+v, blocked mails %d", s, f.mail.blocked)
	}
	if _, err := f.svc.Cancel(ctx, r.ID, f.mail.cancelToken); err != nil {
		t.Fatalf("cancel from blocked: %v", err)
	}
	if f.locker.unlocks != 1 {
		t.Fatalf("unlocks = %d", f.locker.unlocks)
	}
}

func TestGraceEnd_FreezesSoleOrganizationsAndPurges(t *testing.T) {
	f := newFixture(t)
	p := &fakePurger{}
	f.withPurge(t, &fakeOwnership{sole: []Workspace{{ID: "org-1", Kind: "personal"}}}, p, false)
	r := f.confirm(t)
	f.now = f.now.Add(GracePeriod)
	f.svc.ProcessDue(ctx) // pending -> locked -> purging -> purged in one tick
	s := f.stored(t, r.ID)
	if s.State != StatePurged || s.PurgedAt == "" || len(s.Organizations) != 1 || s.Organizations[0] != "org-1" || len(s.Workspaces) != 1 || s.Workspaces[0].Kind != "personal" || p.calls != 1 {
		t.Fatalf("purged request %+v, purge calls %d", s, p.calls)
	}
	if _, err := f.svc.Status(ctx, "u1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("a purged request must release the open marker")
	}
}

func TestPurging_AwaitsParticipants(t *testing.T) {
	f := newFixture(t)
	p := &fakePurger{}
	f.withPurge(t, &fakeOwnership{}, p, true)
	r := f.confirm(t)
	f.now = f.now.Add(GracePeriod)
	f.svc.ProcessDue(ctx)
	if s := f.stored(t, r.ID); s.State != StatePurging || s.DueState != string(StatePurging) || p.calls != 0 {
		t.Fatalf("with participants the account purge waits for acks: %+v calls=%d", s, p.calls)
	}
}

func TestPurging_FailureRetried(t *testing.T) {
	f := newFixture(t)
	p := &fakePurger{err: errors.New("throttled")}
	f.withPurge(t, &fakeOwnership{}, p, false)
	r := f.confirm(t)
	f.now = f.now.Add(GracePeriod)
	f.svc.ProcessDue(ctx)
	if s := f.stored(t, r.ID); s.State != StatePurging || s.DueState != string(StatePurging) {
		t.Fatalf("a failed purge must stay due: %+v", s)
	}
	p.err = nil
	f.svc.ProcessDue(ctx)
	if f.stored(t, r.ID).State != StatePurged || p.calls != 2 {
		t.Fatalf("purge must be retried: calls=%d", p.calls)
	}
}

func (m *memRepo) PutServices(_ context.Context, sts []*ServiceStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, st := range sts {
		k := st.RequestID + "|" + st.Service
		if _, ok := m.services[k]; !ok {
			m.services[k] = *st
		}
	}
	return nil
}

func (m *memRepo) ListServices(_ context.Context, requestID string) ([]*ServiceStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*ServiceStatus
	for _, st := range m.services {
		if st.RequestID == requestID {
			c := st
			out = append(out, &c)
		}
	}
	return out, nil
}

func (m *memRepo) SaveService(_ context.Context, st *ServiceStatus, from string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := st.RequestID + "|" + st.Service
	cur, ok := m.services[k]
	if !ok {
		return ErrNotFound
	}
	if cur.Status != from {
		return ErrStateChanged
	}
	m.services[k] = *st
	return nil
}

func participantFixture(t *testing.T, services ...string) (*fixture, *fakePurger) {
	t.Helper()
	f := newFixture(t)
	p := &fakePurger{}
	f.svc = f.svc.WithOwnership(&fakeOwnership{}).WithPurger(p).WithParticipants(services)
	return f, p
}

func (f *fixture) toPurging(t *testing.T) *Request {
	t.Helper()
	r := f.confirm(t)
	f.now = f.now.Add(GracePeriod)
	f.svc.ProcessDue(ctx)
	if s := f.stored(t, r.ID); s.State != StatePurging {
		t.Fatalf("state %s, want purging", s.State)
	}
	return r
}

func TestPurging_PurgesOnlyAfterEveryAck(t *testing.T) {
	f, p := participantFixture(t, "wallet", "dfe")
	r := f.toPurging(t)
	if err := f.svc.Ack(ctx, "wallet", erasure.Ack{RequestID: r.ID, Result: erasure.ResultDone}); err != nil {
		t.Fatalf("Ack wallet: %v", err)
	}
	f.svc.ProcessDue(ctx)
	if p.calls != 0 {
		t.Fatal("must not purge before every participant acked")
	}
	if err := f.svc.Ack(ctx, "dfe", erasure.Ack{RequestID: r.ID, Result: erasure.ResultDone}); err != nil {
		t.Fatalf("Ack dfe: %v", err)
	}
	f.svc.ProcessDue(ctx)
	if f.stored(t, r.ID).State != StatePurged || p.calls != 1 {
		t.Fatalf("state %s calls %d", f.stored(t, r.ID).State, p.calls)
	}
}

func TestPurging_RepublishesOnlyToSilentParticipantsWithBackoff(t *testing.T) {
	f, _ := participantFixture(t, "wallet", "dfe")
	r := f.toPurging(t)
	_ = f.svc.Ack(ctx, "wallet", erasure.Ack{RequestID: r.ID, Result: erasure.ResultDone})
	base := f.locker.erases // the initial broadcast
	f.now = f.now.Add(10 * time.Minute)
	f.svc.ProcessDue(ctx)
	if f.locker.erases != base {
		t.Fatal("no re-publish before the first backoff (15 min)")
	}
	f.now = f.now.Add(6 * time.Minute)
	f.svc.ProcessDue(ctx)
	if f.locker.erases != base+1 || len(f.locker.lastErase) != 1 || f.locker.lastErase[0] != "dfe" {
		t.Fatalf("re-publish to dfe only: erases=%d last=%v", f.locker.erases, f.locker.lastErase)
	}
}

func TestPurging_BlockedAckStalls(t *testing.T) {
	f, p := participantFixture(t, "wallet")
	r := f.toPurging(t)
	if err := f.svc.Ack(ctx, "wallet", erasure.Ack{RequestID: r.ID, Result: erasure.ResultBlocked,
		Blockers: []erasure.Blocker{{Code: "wallet.balance_nonzero"}}}); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	f.svc.ProcessDue(ctx)
	if s := f.stored(t, r.ID); s.State != StateStalled || s.DueState != "" || p.calls != 0 {
		t.Fatalf("stalled request %+v purge calls %d", s, p.calls)
	}
	if _, err := f.svc.Cancel(ctx, r.ID, f.mail.cancelToken); !errors.Is(err, ErrNotCancellable) {
		t.Fatal("a stalled request is past the point of no return")
	}
}

func TestAck_DuplicateAndPremature(t *testing.T) {
	f, _ := participantFixture(t, "wallet")
	if err := f.svc.Ack(ctx, "wallet", erasure.Ack{RequestID: "nope", Result: erasure.ResultDone}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ack for an unknown request: %v", err)
	}
	r := f.toPurging(t)
	ack := erasure.Ack{RequestID: r.ID, Result: erasure.ResultDone}
	if err := f.svc.Ack(ctx, "wallet", ack); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if err := f.svc.Ack(ctx, "wallet", ack); err != nil {
		t.Fatalf("duplicate ack must be a no-op, got %v", err)
	}
}
