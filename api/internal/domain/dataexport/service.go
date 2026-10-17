package dataexport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// Repository persists the person's current export.
type Repository interface {
	Get(ctx context.Context, userID string) (*Export, error)
	// Save writes e if the stored export is still the one with id prev (empty:
	// there must be none); closure.ErrStateChanged-style conflicts are reported
	// as ErrBuilding so two requests racing do one build.
	Save(ctx context.Context, e *Export, prev string) error
	Delete(ctx context.Context, userID string) error
}

// Store holds the export files.
type Store interface {
	PutObject(ctx context.Context, key, contentType string, data []byte) error
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
	DeleteObject(ctx context.Context, key string) error
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
	repo    Repository
	store   Store
	builder Builder
	users   Users
	mail    Mailer
	audit   Auditor
	now     func() time.Time
	newID   func() string
	// async runs the build; tests replace it with a synchronous call.
	async func(func())
}

func NewService(repo Repository, store Store, builder Builder, users Users, mail Mailer, audit Auditor) *Service {
	return &Service{repo: repo, store: store, builder: builder, users: users, mail: mail, audit: audit,
		now: time.Now, newID: func() string { return uuid.New().String() }, async: func(f func()) { go f() }}
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
	e := &Export{ID: s.newID(), UserID: userID, State: StateBuilding, RequestedAt: now,
		Parts: []Part{{Service: AccountService, Status: string(StateBuilding)}}}
	if err := s.repo.Save(ctx, e, prev); err != nil {
		return nil, err
	}
	s.audit.Record(ctx, userID, EventRequested, map[string]string{"export_id": e.ID})
	if err := s.mail.SendExportRequested(ctx, email, first); err != nil {
		slog.ErrorContext(ctx, "dataexport: requested e-mail failed", "export", e.ID, "error", err)
	}
	s.async(func() { s.build(context.WithoutCancel(ctx), e, email, first) })
	return e, nil
}

func (s *Service) build(ctx context.Context, e *Export, email, first string) {
	data, err := s.builder.Build(ctx, e.UserID)
	if err == nil {
		key := s.key(e.UserID, e.ID, AccountService)
		if err = s.store.PutObject(ctx, key, "application/zip", data); err == nil {
			now := s.now()
			e.State, e.ReadyAt, e.ExpiresAt = StateReady, now, now.Add(ReadyTTL)
			e.Parts = []Part{{Service: AccountService, Key: key, Size: int64(len(data)), Status: string(StateReady)}}
			if err = s.repo.Save(ctx, e, e.ID); err == nil {
				s.audit.Record(ctx, e.UserID, EventReady, map[string]string{"export_id": e.ID})
				if mErr := s.mail.SendExportReady(ctx, email, first); mErr != nil {
					slog.ErrorContext(ctx, "dataexport: ready e-mail failed", "export", e.ID, "error", mErr)
				}
				return
			}
		}
	}
	slog.ErrorContext(ctx, "dataexport: build failed", "export", e.ID, "error", err)
	e.State, e.Error = StateFailed, "build_failed"
	e.Parts = []Part{{Service: AccountService, Status: string(StateFailed)}}
	if sErr := s.repo.Save(ctx, e, e.ID); sErr != nil {
		slog.ErrorContext(ctx, "dataexport: recording the failure failed", "export", e.ID, "error", sErr)
	}
	s.audit.Record(ctx, e.UserID, EventFailed, map[string]string{"export_id": e.ID})
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
		e.State, e.Error = StateFailed, "build_timeout"
		if err := s.repo.Save(ctx, e, e.ID); err != nil {
			return nil, err
		}
	case e.Expired(now):
		s.deleteParts(ctx, e)
		e.State = StateExpired
		e.Parts = nil
		if err := s.repo.Save(ctx, e, e.ID); err != nil {
			return nil, err
		}
	}
	return e, nil
}

// Links mints the download links of a ready export, for the signed-in person
// only. They expire in LinkTTL and are minted on every call, never stored.
func (s *Service) Links(ctx context.Context, e *Export) ([]Link, error) {
	if e.State != StateReady {
		return nil, ErrNotReady
	}
	var out []Link
	for _, p := range e.Parts {
		if p.Status != string(StateReady) || p.Key == "" {
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

// Discard deletes the person's export and its files.
func (s *Service) Discard(ctx context.Context, userID string) error {
	e, err := s.repo.Get(ctx, userID)
	if err != nil {
		return err
	}
	s.deleteParts(ctx, e)
	return s.repo.Delete(ctx, userID)
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
