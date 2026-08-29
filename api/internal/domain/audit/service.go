package audit

import (
	"context"
	"time"

	"gopkg.aoctech.app/api-commons/observability"
)

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Entry describes an event to record. Exactly one of UserID/AnonIP must be set.
type Entry struct {
	UserID    string
	AnonIP    string
	Type      string
	IP        string
	UserAgent string
	Metadata  map[string]string
}

// Record persists a security event. It never fails the caller: repository
// errors are logged and swallowed — losing one audit row must never break
// a login or a password change.
func (s *Service) Record(ctx context.Context, e Entry) {
	if err := s.RecordStrict(ctx, e); err != nil {
		observability.Error(ctx, "audit: failed to record event", err, "type", e.Type)
	}
}

// RecordStrict persists an event and returns repository failures. Use it when
// the protected action must fail closed if its audit trail cannot be written
// (for example issuing links to identity documents).
func (s *Service) RecordStrict(ctx context.Context, e Entry) error {
	now := time.Now().UTC()
	pk := BuildPK(e.UserID)
	if e.UserID == "" {
		pk = AnonPK(e.AnonIP)
	}
	evt := &Event{
		PK:        pk,
		SK:        BuildSK(now),
		EventType: e.Type,
		IP:        e.IP,
		UserAgent: e.UserAgent,
		Metadata:  e.Metadata,
		CreatedAt: now.Format(time.RFC3339),
		ExpiresAt: now.Add(EventTTL).Unix(),
	}
	return s.repo.Put(ctx, evt)
}

// ListByUser returns the user's events, newest first, with cursor pagination.
func (s *Service) ListByUser(ctx context.Context, userID, cursor string, limit int32) ([]*Event, string, error) {
	return s.repo.QueryByUser(ctx, userID, cursor, limit)
}
