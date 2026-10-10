package planlimit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gopkg.aoctech.app/api-commons/observability"

	"gopkg.aoctech.app/account/api/internal/billingclient"
	"gopkg.aoctech.app/account/api/internal/domain/organization"
)

const (
	WorkerInterval = time.Minute
	// drainBatch keeps one tick (two reports of ≤2 s each per row, usually
	// far less) inside the minute the Valkey lock covers.
	drainBatch = 100
	// inlineBudget bounds the report a request makes after its commit.
	inlineBudget = 2 * time.Second
	// reconcileSettle: rows at least this old have a settled membership index.
	reconcileSettle = 30 * time.Second
	startupTimeout  = 10 * time.Second
	// startupProbeRef is a customer billing never has: with owner_key support
	// the answer is 200 with a default; without it, 404.
	startupProbeRef = "USER_ctech-account-startup-probe"
)

type Billing interface {
	Entitlements(ctx context.Context, customerRef, ownerKey string) (*billingclient.Entitlements, error)
	ReportLevel(ctx context.Context, r billingclient.LevelReport) error
}

type LevelCounter interface {
	Levels(ctx context.Context, ownerUserID string) (organization.Levels, error)
	ReconcileSpaceCounter(ctx context.Context, ownerUserID string) error
}

type Service struct {
	billing Billing
	queue   Queue
	counter LevelCounter
	now     func() time.Time
}

func NewService(b Billing, q Queue, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{billing: b, queue: q, now: now}
}

// WithCounter closes the loop with the organization service, which is built
// first and handed this service as its PlanLimits.
func (s *Service) WithCounter(c LevelCounter) *Service {
	s.counter = c
	return s
}

// Quotas reads the owner's plan live — no cache, so an upgrade lifts the
// limit at once (spec § 3.1).
func (s *Service) Quotas(ctx context.Context, ownerUserID string) (organization.Quotas, error) {
	e, err := s.billing.Entitlements(ctx, billingclient.CustomerRef(ownerUserID), billingclient.OwnerKeyFinance)
	if err != nil {
		return organization.Quotas{}, fmt.Errorf("%w: %v", organization.ErrPlanUnavailable, err)
	}
	return QuotasFrom(ctx, e)
}

// LevelsChanged marks the owner dirty, then tries to report right away. It
// never fails the caller: the write it follows already committed (spec § 4).
//
// The row is kept even when the report succeeds. The count reads the
// membership index, which is eventually consistent: milliseconds after the
// commit it can still miss the space just created (or see a transferred one as
// still a member's). The worker re-reports it once the index has settled
// (reconcileSettle) and only then clears it, so billing never keeps a low level.
func (s *Service) LevelsChanged(ctx context.Context, ownerUserID string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), inlineBudget)
	defer cancel()
	at := s.now().UTC()
	if err := s.queue.MarkNow(ctx, ownerUserID, at); err != nil {
		observability.Error(ctx, "plan levels: marking the owner dirty failed", err, "owner", ownerUserID)
	}
	if err := s.report(ctx, ownerUserID); err != nil {
		observability.Warn(ctx, "plan levels: inline report failed; the worker will retry", err, "owner", ownerUserID)
	}
}

// ScheduleLevels writes a report due later (an invitation's expiry). Detached
// from the request, like LevelsChanged: it runs after the commit and after the
// inline report, by which time the request may be gone.
func (s *Service) ScheduleLevels(ctx context.Context, ownerUserID string, at time.Time) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), inlineBudget)
	defer cancel()
	if err := s.queue.Schedule(ctx, ownerUserID, at.UTC()); err != nil {
		observability.Error(ctx, "plan levels: scheduling a report failed", err, "owner", ownerUserID)
	}
}

// report recounts from the source rows and sends both meters, whatever the
// plan. occurred_at is the instant of the count.
func (s *Service) report(ctx context.Context, ownerUserID string) error {
	lv, err := s.counter.Levels(ctx, ownerUserID)
	if err != nil {
		return err
	}
	at := s.now().UTC()
	for _, m := range []struct {
		meter string
		value int64
	}{{billingclient.MeterSpaces, lv.Spaces}, {billingclient.MeterPeople, lv.People}} {
		if err := s.billing.ReportLevel(ctx, billingclient.LevelReport{
			CustomerRef:    billingclient.CustomerRef(ownerUserID),
			Meter:          m.meter,
			Value:          m.value,
			OccurredAt:     at,
			IdempotencyKey: billingclient.LevelKey(ownerUserID, m.meter, at),
		}); err != nil {
			return err
		}
	}
	return nil
}

// ProcessDue drains due rows: report, reconcile a settled spaces counter,
// clear. A row whose report fails stays for the next tick.
func (s *Service) ProcessDue(ctx context.Context) {
	now := s.now().UTC()
	rows, err := s.queue.Due(ctx, now, drainBatch)
	if err != nil {
		observability.Error(ctx, "plan levels: reading the queue failed", err)
		return
	}
	for _, row := range rows {
		// A change's row waits for the index to settle (see LevelsChanged);
		// a scheduled row is due by definition.
		if !row.Scheduled && now.Sub(row.DueAt) < reconcileSettle {
			continue
		}
		if err := s.report(ctx, row.OwnerUserID); err != nil {
			observability.Warn(ctx, "plan levels: report failed; kept for the next tick", err, "owner", row.OwnerUserID)
			continue
		}
		if err := s.counter.ReconcileSpaceCounter(ctx, row.OwnerUserID); err != nil {
			observability.Warn(ctx, "plan levels: reconciling the spaces counter failed", err, "owner", row.OwnerUserID)
		}
		if _, err := s.queue.Done(ctx, row); err != nil {
			observability.Warn(ctx, "plan levels: clearing a delivered row failed", err, "owner", row.OwnerUserID)
		}
	}
}

var errNoOwnerKeySupport = errors.New("billing answered without a default plan")

// CheckBilling is the startup check of deploy order step 1: logs loudly and
// returns an error, never stops the process — this service is the platform's
// auth backbone, and billing being early or late must not take sign-in down.
func (s *Service) CheckBilling(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	e, err := s.billing.Entitlements(ctx, startupProbeRef, billingclient.OwnerKeyFinance)
	if err == nil && e.Default == nil {
		err = errNoOwnerKeySupport
	}
	if err != nil {
		observability.Error(ctx, "PLAN LIMITS NOT READY: ctech-billing does not answer entitlements with owner_key=finance "+
			"and a default plan (or the account-billing client is not usable). Every create, invite and transfer of a "+
			"personal space will answer 503 until ctech-billing ships its plans step "+
			"(docs/specs/2026-10-10-space-plan-limits.md, Deploy order).", err)
		return err
	}
	return nil
}

// RunWorker drains the queue on whichever instance wins tryLock for the tick
// (deletion.RunWorker's pattern).
func RunWorker(ctx context.Context, svc *Service, tryLock func(context.Context) (bool, error), interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			ok, err := tryLock(ctx)
			if err != nil {
				observability.Error(ctx, "plan levels worker: lock failed", err)
				continue
			}
			if ok {
				svc.ProcessDue(ctx)
			}
		}
	}
}

var _ LevelCounter = (*organization.Service)(nil)
