package audit

import (
	"context"
	"log/slog"
	"time"
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
	if err := s.repo.Put(ctx, evt); err != nil {
		slog.Error("audit: failed to record event", "type", e.Type, "error", err)
	}
}

// ListByUser returns the user's events, newest first, with cursor pagination.
func (s *Service) ListByUser(ctx context.Context, userID, cursor string, limit int32) ([]*Event, string, error) {
	return s.repo.QueryByUser(ctx, userID, cursor, limit)
}
