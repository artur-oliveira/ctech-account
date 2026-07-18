package audit

import (
	"context"
	"errors"
	"testing"
)

type memRepo struct {
	events []*Event
	err    error
}

func (m *memRepo) Put(_ context.Context, e *Event) error {
	if m.err != nil {
		return m.err
	}
	m.events = append(m.events, e)
	return nil
}

func (m *memRepo) QueryByUser(_ context.Context, userID, _ string, _ int32) ([]*Event, string, error) {
	var out []*Event
	for _, e := range m.events {
		if e.PK == BuildPK(userID) {
			out = append(out, e)
		}
	}
	return out, "", nil
}

func TestRecordPersistsEvent(t *testing.T) {
	repo := &memRepo{}
	svc := NewService(repo)

	svc.Record(context.Background(), Entry{
		UserID: "u1", Type: EventLoginSuccess, IP: "1.2.3.4",
		UserAgent: "ua", Metadata: map[string]string{"client_id": "web"},
	})

	if len(repo.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(repo.events))
	}
	e := repo.events[0]
	if e.PK != "USER_u1" || e.EventType != EventLoginSuccess {
		t.Errorf("unexpected event: %+v", e)
	}
	if e.ExpiresAt == 0 || e.SK == "" || e.CreatedAt == "" {
		t.Errorf("missing derived fields: %+v", e)
	}
}

func TestRecordAnonUsesIPKey(t *testing.T) {
	repo := &memRepo{}
	NewService(repo).Record(context.Background(), Entry{
		AnonIP: "9.9.9.9", Type: EventLoginFailed,
	})
	if repo.events[0].PK != "ANON_9.9.9.9" {
		t.Errorf("expected ANON pk, got %s", repo.events[0].PK)
	}
}

func TestRecordSwallowsRepositoryError(t *testing.T) {
	repo := &memRepo{err: errors.New("dynamo down")}
	// Must not panic and must not surface the error.
	NewService(repo).Record(context.Background(), Entry{UserID: "u1", Type: EventLoginSuccess})
}

func TestListByUserDelegatesToRepository(t *testing.T) {
	repo := &memRepo{}
	svc := NewService(repo)
	svc.Record(context.Background(), Entry{UserID: "u1", Type: EventLoginSuccess})
	svc.Record(context.Background(), Entry{UserID: "u2", Type: EventLoginSuccess})

	events, next, err := svc.ListByUser(context.Background(), "u1", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if next != "" || len(events) != 1 {
		t.Fatalf("expected 1 event for u1, got %d (next=%q)", len(events), next)
	}
}
