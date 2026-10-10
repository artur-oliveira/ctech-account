package planlimit

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/account/api/internal/billingclient"
	"gopkg.aoctech.app/account/api/internal/domain/organization"
)

type fakeBilling struct {
	ent       *billingclient.Entitlements
	entErr    error
	reports   []billingclient.LevelReport
	reportErr error
	onReport  func()
}

func (f *fakeBilling) Entitlements(context.Context, string, string) (*billingclient.Entitlements, error) {
	return f.ent, f.entErr
}

func (f *fakeBilling) ReportLevel(_ context.Context, r billingclient.LevelReport) error {
	if f.onReport != nil {
		hook := f.onReport
		f.onReport = nil
		hook()
	}
	if f.reportErr != nil {
		return f.reportErr
	}
	f.reports = append(f.reports, r)
	return nil
}

type fakeCounter struct {
	levels     map[string]organization.Levels
	reconciled []string
}

func (f *fakeCounter) Levels(_ context.Context, owner string) (organization.Levels, error) {
	return f.levels[owner], nil
}
func (f *fakeCounter) ReconcileSpaceCounter(_ context.Context, owner string) error {
	f.reconciled = append(f.reconciled, owner)
	return nil
}

type memQueue struct{ rows map[string]DirtyRow }

func (q *memQueue) MarkNow(_ context.Context, owner string, at time.Time) error {
	q.rows[nowSK(owner)] = DirtyRow{SK: nowSK(owner), OwnerUserID: owner, DueAt: at}
	return nil
}
func (q *memQueue) Schedule(_ context.Context, owner string, at time.Time) error {
	q.rows[atSK(at, owner)] = DirtyRow{SK: atSK(at, owner), OwnerUserID: owner, DueAt: at, Scheduled: true}
	return nil
}
func (q *memQueue) Due(_ context.Context, now time.Time, limit int) ([]DirtyRow, error) {
	out := []DirtyRow{}
	for _, r := range q.rows {
		if !r.Scheduled || !r.DueAt.After(now) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SK < out[j].SK })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (q *memQueue) Done(_ context.Context, row DirtyRow) (bool, error) {
	cur, ok := q.rows[row.SK]
	if !ok || (!row.Scheduled && !cur.DueAt.Equal(row.DueAt)) {
		return false, nil
	}
	delete(q.rows, row.SK)
	return true, nil
}

type harness struct {
	svc     *Service
	billing *fakeBilling
	counter *fakeCounter
	queue   *memQueue
	now     *time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	now := time.Date(2026, 10, 10, 14, 3, 0, 0, time.UTC)
	h := &harness{
		billing: &fakeBilling{},
		counter: &fakeCounter{levels: map[string]organization.Levels{"u1": {Spaces: 2, People: 3}}},
		queue:   &memQueue{rows: map[string]DirtyRow{}},
		now:     &now,
	}
	h.svc = NewService(h.billing, h.queue, func() time.Time { return *h.now }).WithCounter(h.counter)
	return h
}

var _ organization.PlanLimits = (*Service)(nil)

func TestAChangeIsReportedInlineAndTheRowCleared(t *testing.T) {
	h := newHarness(t)
	h.svc.LevelsChanged(context.Background(), "u1")
	if len(h.billing.reports) != 2 {
		t.Fatalf("reports = %+v", h.billing.reports)
	}
	spaces, people := h.billing.reports[0], h.billing.reports[1]
	if spaces.Meter != billingclient.MeterSpaces || spaces.Value != 2 || spaces.CustomerRef != "USER_u1" ||
		people.Meter != billingclient.MeterPeople || people.Value != 3 ||
		spaces.IdempotencyKey != billingclient.LevelKey("u1", billingclient.MeterSpaces, *h.now) {
		t.Fatalf("reports = %+v", h.billing.reports)
	}
	if len(h.queue.rows) != 0 {
		t.Fatalf("rows left: %v", h.queue.rows)
	}
}

// Free included: billing must already know the level the day the person moves
// to Sob demanda (spec § 4). The service never looks at the plan to report.
func TestAReportNeverAsksForThePlan(t *testing.T) {
	h := newHarness(t)
	h.billing.entErr = errors.New("must not be called")
	h.svc.LevelsChanged(context.Background(), "u1")
	if len(h.billing.reports) != 2 {
		t.Fatal("the report depended on the entitlement")
	}
}

// Spec test 8: a failed inline report is delivered by the worker.
func TestAFailedInlineReportIsDeliveredByTheWorker(t *testing.T) {
	h := newHarness(t)
	h.billing.reportErr = errors.New("billing down")
	h.svc.LevelsChanged(context.Background(), "u1")
	if len(h.queue.rows) != 1 {
		t.Fatalf("rows = %v, want the NOW row kept", h.queue.rows)
	}
	h.billing.reportErr = nil
	h.counter.levels["u1"] = organization.Levels{Spaces: 3, People: 3} // changed since: recount at send time
	*h.now = h.now.Add(time.Minute)
	h.svc.ProcessDue(context.Background())
	if len(h.billing.reports) != 2 || h.billing.reports[0].Value != 3 {
		t.Fatalf("reports = %+v", h.billing.reports)
	}
	if len(h.queue.rows) != 0 {
		t.Fatalf("rows left: %v", h.queue.rows)
	}
	if len(h.counter.reconciled) != 1 {
		t.Fatalf("a settled row was not reconciled: %v", h.counter.reconciled)
	}
}

// Spec test 8: a newer change is not deleted by an older success.
func TestANewerChangeIsNotDeletedByAnOlderSuccess(t *testing.T) {
	h := newHarness(t)
	newer := h.now.Add(time.Second)
	h.billing.onReport = func() { _ = h.queue.MarkNow(context.Background(), "u1", newer) }
	h.svc.LevelsChanged(context.Background(), "u1")
	row, ok := h.queue.rows[nowSK("u1")]
	if !ok || !row.DueAt.Equal(newer) {
		t.Fatalf("the newer change was lost: %v", h.queue.rows)
	}
}

// Spec test 8: an invitation's expiry produces a report at expires_at.
func TestAnInvitationExpiryIsReportedWhenDue(t *testing.T) {
	h := newHarness(t)
	at := h.now.Add(7 * 24 * time.Hour)
	h.svc.ScheduleLevels(context.Background(), "u1", at)
	h.svc.ProcessDue(context.Background())
	if len(h.billing.reports) != 0 {
		t.Fatal("reported before the expiry")
	}
	*h.now = at
	h.svc.ProcessDue(context.Background())
	if len(h.billing.reports) != 2 || len(h.queue.rows) != 0 {
		t.Fatalf("reports=%d rows=%v", len(h.billing.reports), h.queue.rows)
	}
}

func TestQuotasWrapBillingFailures(t *testing.T) {
	h := newHarness(t)
	h.billing.entErr = billingclient.ErrUnavailable
	if _, err := h.svc.Quotas(context.Background(), "u1"); !errors.Is(err, organization.ErrPlanUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

// Review Focus 5.
func TestTheStartupCheckLogsWithoutFailing(t *testing.T) {
	logs := captureLogs(t)
	h := newHarness(t)
	h.billing.entErr = billingclient.ErrUnavailable
	if err := h.svc.CheckBilling(context.Background()); err == nil {
		t.Fatal("no error returned")
	}
	if !strings.Contains(logs.String(), "owner_key") {
		t.Fatalf("not logged loudly: %s", logs)
	}
	h.billing.entErr = nil
	h.billing.ent = &billingclient.Entitlements{Default: &billingclient.EntitlementDefault{Plan: "free"}}
	if err := h.svc.CheckBilling(context.Background()); err != nil {
		t.Fatalf("a ready billing failed the check: %v", err)
	}
}
