package dataexport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	wire "gopkg.aoctech.app/api-commons/dataexport"
)

// Finish is a state change of the current export.
type Finish struct {
	State     State
	Error     string
	ReadyAt   time.Time // zero: leave ready_at and expires_at alone
	ExpiresAt time.Time
	DropParts bool
}

// Repository persists the person's current export. Every write after the
// first is conditional on the export id, so a late ack or build for a replaced
// export is dropped (ErrStale) instead of overwriting the new one, and a part
// can be updated without rewriting the row.
type Repository interface {
	Get(ctx context.Context, userID string) (*Export, error)
	// Save writes e if the stored export is still the one with id prev (empty:
	// there must be none); a race is reported as ErrBuilding.
	Save(ctx context.Context, e *Export, prev string) error
	SetPart(ctx context.Context, userID, exportID string, p Part) error
	Finish(ctx context.Context, userID, exportID string, f Finish) error
	// MarkNotified flips the "ready e-mail sent" flag once: false if it was set.
	MarkNotified(ctx context.Context, userID, exportID string) (bool, error)
	Delete(ctx context.Context, userID string) error
}

// Store holds the export files.
type Store interface {
	PutObject(ctx context.Context, key, contentType string, data []byte) error
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
	PresignPut(ctx context.Context, key, contentType string, ttl time.Duration) (string, error)
	Size(ctx context.Context, key string) (int64, error)
	DeleteObject(ctx context.Context, key string) error
	ListKeys(ctx context.Context, prefix string) ([]string, error)
}

// Dispatcher asks a participant for its part (SNS, on the erasure topic).
type Dispatcher interface {
	Publish(ctx context.Context, m wire.Message) error
}

// Builder produces ctech-account's own part: a zip of the person's data.
type Builder interface {
	Build(ctx context.Context, userID string) ([]byte, error)
}

// Users says whether the account may export (active) and where to write.
type Users interface {
	Contact(ctx context.Context, userID string) (email, firstName string, active bool, err error)
}

// Mailer tells the person about their export; it never carries a link (E6).
type Mailer interface {
	SendExportRequested(ctx context.Context, to, firstName string) error
	SendExportReady(ctx context.Context, to, firstName string) error
}

// Auditor records the export events.
type Auditor interface {
	Record(ctx context.Context, userID, event string, meta map[string]string)
}

const (
	EventRequested  = "account.export.requested"
	EventReady      = "account.export.ready"
	EventLinkIssued = "account.export.link_issued"
	EventFailed     = "account.export.failed"
)

// Link is one download link, with its part.
type Link struct {
	Service string
	URL     string
	Size    int64
}

type Service struct {
	participants []string
	dispatcher   Dispatcher
	repo         Repository
	store        Store
	builder      Builder
	users        Users
	mail         Mailer
	audit        Auditor
	now          func() time.Time
	newID        func() string
	// async runs the build; tests replace it with a synchronous call.
	async func(func())
}

func NewService(repo Repository, store Store, builder Builder, users Users, mail Mailer, audit Auditor) *Service {
	return &Service{repo: repo, store: store, builder: builder, users: users, mail: mail, audit: audit,
		now: time.Now, newID: func() string { return uuid.New().String() }, async: func(f func()) { go f() }}
}

// WithParticipants makes every export ask these services for their part.
func (s *Service) WithParticipants(services []string, d Dispatcher) *Service {
	s.participants, s.dispatcher = services, d
	return s
}

func (s *Service) key(userID, id, service string) string {
	return fmt.Sprintf("exports/%s/%s/%s.zip", userID, id, service)
}

// Request starts an export. The build runs in the background; the person sees it
// through Status.
func (s *Service) Request(ctx context.Context, userID string) (*Export, error) {
	email, first, active, err := s.users.Contact(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, ErrLocked
	}
	now := s.now()
	cur, err := s.repo.Get(ctx, userID)
	prev := ""
	switch {
	case errors.Is(err, ErrNotFound):
	case err != nil:
		return nil, err
	default:
		prev = cur.ID
		switch {
		case cur.State == StateBuilding && !cur.Stuck(now):
			return nil, ErrBuilding
		case cur.State != StateFailed && now.Sub(cur.RequestedAt) < MinInterval:
			return nil, ErrTooSoon
		}
		s.deleteParts(ctx, cur) // a replaced export's files go with it
	}
	e := &Export{ID: s.newID(), UserID: userID, State: StateBuilding, RequestedAt: now}
	e.Parts = []Part{{Service: AccountService, Key: s.key(userID, e.ID, AccountService), Status: PartBuilding}}
	if s.dispatcher != nil {
		for _, svc := range s.participants {
			e.Parts = append(e.Parts, Part{Service: svc, Key: s.key(userID, e.ID, svc), Status: PartDispatched, DispatchedAt: now})
		}
	}
	if err := s.repo.Save(ctx, e, prev); err != nil {
		return nil, err
	}
	s.audit.Record(ctx, userID, EventRequested, map[string]string{"export_id": e.ID})
	if err := s.mail.SendExportRequested(ctx, email, first); err != nil {
		slog.ErrorContext(ctx, "dataexport: requested e-mail failed", "export", e.ID, "error", err)
	}
	// The background work mutates its own copy: e goes back to the caller.
	work := *e
	work.Parts = append([]Part(nil), e.Parts...)
	s.async(func() {
		ctx := context.WithoutCancel(ctx)
		for i := range work.Parts {
			if work.Parts[i].Service != AccountService {
				s.dispatch(ctx, &work, &work.Parts[i])
			}
		}
		s.build(ctx, &work)
	})
	return e, nil
}

// dispatch asks a participant for its part with a fresh upload URL. The attempt
// is recorded before publishing, so an ack that races back cannot be
// overwritten by it; a failed publish just waits for the next redispatch.
func (s *Service) dispatch(ctx context.Context, e *Export, p *Part) {
	now := s.now()
	p.Attempts++
	p.DispatchedAt = now
	if err := s.repo.SetPart(ctx, e.UserID, e.ID, *p); err != nil {
		slog.ErrorContext(ctx, "dataexport: recording dispatch failed", "export", e.ID, "service", p.Service, "error", err)
		return
	}
	url, err := s.store.PresignPut(ctx, p.Key, "application/zip", PutTTL)
	if err == nil {
		err = s.dispatcher.Publish(ctx, wire.Message{Version: wire.Version, Type: wire.TypeExport, RequestID: e.ID, Sub: e.UserID,
			Service: p.Service, PutURL: url, ExpiresAt: now.Add(PutTTL), Attempt: p.Attempts, IssuedAt: now})
	}
	if err != nil {
		slog.ErrorContext(ctx, "dataexport: dispatch failed", "export", e.ID, "service", p.Service, "attempt", p.Attempts, "error", err)
	}
}

func (s *Service) build(ctx context.Context, e *Export) {
	p, _ := e.Part(AccountService)
	data, err := s.builder.Build(ctx, e.UserID)
	if err == nil {
		err = s.store.PutObject(ctx, p.Key, "application/zip", data)
	}
	if err == nil {
		now := s.now()
		p.Status, p.Size = PartReady, int64(len(data))
		if err = s.repo.SetPart(ctx, e.UserID, e.ID, *p); err == nil {
			err = s.repo.Finish(ctx, e.UserID, e.ID, Finish{State: StateReady, ReadyAt: now, ExpiresAt: now.Add(ReadyTTL)})
		}
	}
	if err != nil {
		slog.ErrorContext(ctx, "dataexport: build failed", "export", e.ID, "error", err)
		p.Status = PartFailed
		_ = s.repo.SetPart(ctx, e.UserID, e.ID, *p)
		if fErr := s.repo.Finish(ctx, e.UserID, e.ID, Finish{State: StateFailed, Error: "build_failed"}); fErr != nil {
			slog.ErrorContext(ctx, "dataexport: recording the failure failed", "export", e.ID, "error", fErr)
		}
		s.audit.Record(ctx, e.UserID, EventFailed, map[string]string{"export_id": e.ID})
		return
	}
	s.audit.Record(ctx, e.UserID, EventReady, map[string]string{"export_id": e.ID})
	s.notifyIfDone(ctx, e.UserID, e.ID)
}

// notifyIfDone sends the "ready" e-mail once, when the account part is ready and
// no participant is still producing its part.
func (s *Service) notifyIfDone(ctx context.Context, userID, exportID string) {
	e, err := s.repo.Get(ctx, userID)
	if err != nil || e.ID != exportID || e.State != StateReady || e.Pending() {
		return
	}
	if first, err := s.repo.MarkNotified(ctx, userID, exportID); err != nil || !first {
		return
	}
	email, name, _, err := s.users.Contact(ctx, userID)
	if err == nil {
		err = s.mail.SendExportReady(ctx, email, name)
	}
	if err != nil {
		slog.ErrorContext(ctx, "dataexport: ready e-mail failed", "export", exportID, "error", err)
	}
}

// Ack records a participant's answer. A stale ack (the export was replaced,
// expired or discarded) is dropped with ErrStale; the caller answers 2xx so the
// participant stops retrying.
func (s *Service) Ack(ctx context.Context, service string, a wire.Ack) error {
	e, err := s.repo.Get(ctx, a.Sub)
	if errors.Is(err, ErrNotFound) {
		return ErrStale
	}
	if err != nil {
		return err
	}
	if e.ID != a.RequestID || e.State == StateExpired || e.State == StateFailed {
		return ErrStale
	}
	p, ok := e.Part(service)
	if !ok || service == AccountService {
		return ErrUnknownPart
	}
	switch a.Result {
	case wire.ResultDone:
		size, err := s.store.Size(ctx, p.Key)
		if err != nil {
			return ErrNotUploaded
		}
		p.Status, p.Size = PartReady, size
	case wire.ResultFailed:
		p.Status = PartFailed
	default:
		return fmt.Errorf("dataexport: unknown result %q", a.Result)
	}
	if err := s.repo.SetPart(ctx, e.UserID, e.ID, *p); err != nil {
		return err
	}
	s.notifyIfDone(ctx, e.UserID, e.ID)
	return nil
}

// Status is the person's current export. A build that outlived StuckAfter is
// declared failed, and a ready export past ReadyTTL is expired and its files
// deleted, so neither blocks nor lingers.
func (s *Service) Status(ctx context.Context, userID string) (*Export, error) {
	e, err := s.repo.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	switch {
	case e.Stuck(now):
		if err := s.repo.Finish(ctx, userID, e.ID, Finish{State: StateFailed, Error: "build_timeout"}); err != nil {
			return nil, err
		}
		e.State, e.Error = StateFailed, "build_timeout"
	case e.Expired(now):
		s.deleteParts(ctx, e)
		if err := s.repo.Finish(ctx, userID, e.ID, Finish{State: StateExpired, DropParts: true}); err != nil {
			return nil, err
		}
		e.State, e.Parts = StateExpired, nil
	default:
		s.reconcile(ctx, e, now)
	}
	return e, nil
}

// reconcile is the participants' reconciler, run lazily on a status read (the
// page polls while a part is pending), so it needs no timer: a silent
// participant is asked again with a fresh URL, and past PartDeadline its part
// is shown as failed while the rest stays downloadable (E8).
// ponytail: only runs while the person watches the page; a worker like
// deletion.RunWorker if exports ever need to finish unattended.
func (s *Service) reconcile(ctx context.Context, e *Export, now time.Time) {
	if (e.State != StateReady && e.State != StateBuilding) || s.dispatcher == nil {
		return
	}
	changed := false
	for i := range e.Parts {
		p := &e.Parts[i]
		if p.Service == AccountService || p.Status != PartDispatched {
			continue
		}
		switch {
		case now.Sub(e.RequestedAt) > PartDeadline || p.Attempts >= MaxAttempts && now.Sub(p.DispatchedAt) > RedispatchAfter:
			p.Status = PartFailed
			if err := s.repo.SetPart(ctx, e.UserID, e.ID, *p); err != nil {
				slog.ErrorContext(ctx, "dataexport: failing a silent part failed", "export", e.ID, "service", p.Service, "error", err)
			}
			changed = true
		case now.Sub(p.DispatchedAt) > RedispatchAfter:
			s.dispatch(ctx, e, p)
		}
	}
	if changed {
		s.notifyIfDone(ctx, e.UserID, e.ID)
	}
}

// Links mints the download links of a ready export, for the signed-in person
// only. They expire in LinkTTL and are minted on every call, never stored.
func (s *Service) Links(ctx context.Context, e *Export) ([]Link, error) {
	if e.State != StateReady {
		return nil, ErrNotReady
	}
	var out []Link
	for _, p := range e.Parts {
		if p.Status != PartReady || p.Key == "" {
			continue
		}
		url, err := s.store.PresignGet(ctx, p.Key, LinkTTL)
		if err != nil {
			return nil, err
		}
		out = append(out, Link{Service: p.Service, URL: url, Size: p.Size})
	}
	s.audit.Record(ctx, e.UserID, EventLinkIssued, map[string]string{"export_id": e.ID})
	return out, nil
}

// Discard deletes the person's export files. The row stays as an expired
// tombstone keeping RequestedAt, so discarding is not a way around MinInterval.
func (s *Service) Discard(ctx context.Context, userID string) error {
	e, err := s.repo.Get(ctx, userID)
	if err != nil {
		return err
	}
	s.deleteParts(ctx, e)
	return s.repo.Finish(ctx, userID, e.ID, Finish{State: StateExpired, DropParts: true})
}

func (s *Service) deleteParts(ctx context.Context, e *Export) {
	for _, p := range e.Parts {
		if p.Key == "" {
			continue
		}
		if err := s.store.DeleteObject(ctx, p.Key); err != nil {
			slog.ErrorContext(ctx, "dataexport: deleting a part failed", "export", e.ID, "service", p.Service, "error", err)
		}
	}
}

// EraseOwner removes everything of a purged account: every object under the
// person's prefix (found by listing, so a part a participant uploaded after the
// row stopped naming it goes too) and the row itself. It is the deletion
// purge's OwnerEraser; idempotent, so a rerun after a crash finishes the job.
func (s *Service) EraseOwner(ctx context.Context, userID string) error {
	keys, err := s.store.ListKeys(ctx, "exports/"+userID+"/")
	if err != nil {
		return fmt.Errorf("dataexport: listing exports of the purged account: %w", err)
	}
	for _, k := range keys {
		if err := s.store.DeleteObject(ctx, k); err != nil {
			return fmt.Errorf("dataexport: deleting an export file of the purged account: %w", err)
		}
	}
	return s.repo.Delete(ctx, userID)
}
