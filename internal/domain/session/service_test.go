package session_test

import (
	"context"
	"errors"
	"testing"

	"github.com/artur-oliveira/ctech-account/internal/domain/session"
)

// mockRepo is an in-memory session Repository.
type mockRepo struct {
	sessions map[string]*session.Session // key: pk+sk
}

func newMockRepo() *mockRepo {
	return &mockRepo{sessions: make(map[string]*session.Session)}
}

func key(userID, sessionID string) string {
	return "USER_" + userID + "|SESSION_" + sessionID
}

func (m *mockRepo) Create(_ context.Context, s *session.Session) error {
	m.sessions[s.PK+"|"+s.SK] = s
	return nil
}

func (m *mockRepo) GetByID(_ context.Context, userID, sessionID string) (*session.Session, error) {
	k := session.BuildPK(userID) + "|" + session.BuildSK(sessionID)
	s, ok := m.sessions[k]
	if !ok {
		return nil, session.ErrNotFound
	}
	return s, nil
}

func (m *mockRepo) UpdateRefreshToken(_ context.Context, userID, sessionID, newHash string) error {
	k := session.BuildPK(userID) + "|" + session.BuildSK(sessionID)
	s, ok := m.sessions[k]
	if !ok {
		return session.ErrNotFound
	}
	s.RefreshTokenHash = newHash
	return nil
}

func (m *mockRepo) Delete(_ context.Context, userID, sessionID string) error {
	k := session.BuildPK(userID) + "|" + session.BuildSK(sessionID)
	delete(m.sessions, k)
	return nil
}

func (m *mockRepo) GetByTokenHash(_ context.Context, tokenHash string) (*session.Session, error) {
	for _, s := range m.sessions {
		if s.RefreshTokenHash == tokenHash {
			return s, nil
		}
	}
	return nil, session.ErrNotFound
}

func (m *mockRepo) ListByUserID(_ context.Context, userID string) ([]*session.Session, error) {
	pk := session.BuildPK(userID)
	var result []*session.Session
	for k, s := range m.sessions {
		if len(k) > len(pk) && k[:len(pk)] == pk {
			result = append(result, s)
		}
	}
	return result, nil
}

func TestCreate(t *testing.T) {
	svc := session.NewService(newMockRepo())
	sess, raw, err := svc.Create(context.Background(), "user1", "Chrome", "1.2.3.4", "UA")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sess == nil || raw == "" {
		t.Fatal("expected non-nil session and raw token")
	}
	if sess.UserID() != "user1" {
		t.Errorf("wrong userID: %s", sess.UserID())
	}
}

func TestRotate_Success(t *testing.T) {
	svc := session.NewService(newMockRepo())
	_, raw, _ := svc.Create(context.Background(), "user2", "Chrome", "1.2.3.4", "UA")

	_, newRaw, err := svc.Rotate(context.Background(), raw)
	if err != nil {
		t.Fatalf("rotate failed: %v", err)
	}
	if newRaw == "" || newRaw == raw {
		t.Errorf("expected new different token")
	}
}

func TestRotate_TokenReuse(t *testing.T) {
	svc := session.NewService(newMockRepo())
	_, raw, _ := svc.Create(context.Background(), "user3", "Chrome", "1.2.3.4", "UA")
	// First rotate succeeds.
	_, _, _ = svc.Rotate(context.Background(), raw)
	// Reusing the old raw token should detect reuse.
	_, _, err := svc.Rotate(context.Background(), raw)
	if !errors.Is(err, session.ErrTokenReuse) {
		t.Errorf("expected ErrTokenReuse, got %v", err)
	}
}

func TestValidateToken_Success(t *testing.T) {
	svc := session.NewService(newMockRepo())
	sess, raw, _ := svc.Create(context.Background(), "user4", "Chrome", "1.2.3.4", "UA")

	validated, err := svc.ValidateToken(context.Background(), raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if validated.ID() != sess.ID() {
		t.Errorf("wrong session ID returned")
	}
}

func TestValidateToken_WrongToken(t *testing.T) {
	svc := session.NewService(newMockRepo())
	_, _, _ = svc.Create(context.Background(), "user5", "Chrome", "1.2.3.4", "UA")

	_, err := svc.ValidateToken(context.Background(), "wrongtoken")
	if err == nil {
		t.Error("expected error for wrong token")
	}
}

func TestRevoke(t *testing.T) {
	repo := newMockRepo()
	svc := session.NewService(repo)
	sess, _, _ := svc.Create(context.Background(), "user6", "Chrome", "1.2.3.4", "UA")

	err := svc.Revoke(context.Background(), "user6", sess.ID())
	if err != nil {
		t.Fatalf("revoke failed: %v", err)
	}
	_, err = svc.List(context.Background(), "user6")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
}

func TestRevokeAll_ExceptCurrent(t *testing.T) {
	svc := session.NewService(newMockRepo())
	s1, _, _ := svc.Create(context.Background(), "user7", "Chrome", "1.2.3.4", "UA")
	s2, _, _ := svc.Create(context.Background(), "user7", "Firefox", "1.2.3.4", "UA")

	err := svc.RevokeAll(context.Background(), "user7", s1.ID())
	if err != nil {
		t.Fatalf("revoke all failed: %v", err)
	}

	sessions, _ := svc.List(context.Background(), "user7")
	if len(sessions) != 1 {
		t.Errorf("expected 1 session remaining, got %d", len(sessions))
	}
	if sessions[0].ID() == s2.ID() {
		t.Errorf("wrong session kept; s2 should have been removed")
	}
	_ = s2
}

func TestReplaceRefreshToken(t *testing.T) {
	svc := session.NewService(newMockRepo())
	sess, oldRaw, _ := svc.Create(context.Background(), "user8", "Chrome", "1.2.3.4", "UA")

	newRaw, err := svc.ReplaceRefreshToken(context.Background(), "user8", sess.ID())
	if err != nil {
		t.Fatalf("replace failed: %v", err)
	}
	if newRaw == oldRaw {
		t.Error("expected a different token after replace")
	}

	// Old token should no longer be valid.
	_, err = svc.ValidateToken(context.Background(), oldRaw)
	if err == nil {
		t.Error("old token should be invalid after replace")
	}
}
