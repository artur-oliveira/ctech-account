package session_test

import (
	"context"
	"errors"
	"testing"

	"gopkg.aoctech.app/account/api/internal/domain/session"
)

// mockRepo is an in-memory session Repository.
type mockRepo struct {
	sessions map[string]*session.Session      // key: pk|sk
	tokens   map[string]*session.RefreshToken // key: pk|sk
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		sessions: make(map[string]*session.Session),
		tokens:   make(map[string]*session.RefreshToken),
	}
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

func (m *mockRepo) UpdateGeoData(_ context.Context, _, _ string, _, _ string, _, _ float64) error {
	return nil
}

func (m *mockRepo) UpdateMFA(_ context.Context, userID, sessionID string, amr []string, lastMFAAt int64) error {
	k := session.BuildPK(userID) + "|" + session.BuildSK(sessionID)
	sess, ok := m.sessions[k]
	if !ok {
		return session.ErrNotFound
	}
	sess.AMR = amr
	sess.LastMFAAt = lastMFAAt
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

func (m *mockRepo) PutRefreshToken(_ context.Context, t *session.RefreshToken) error {
	m.tokens[t.PK+"|"+t.SK] = t
	return nil
}

func (m *mockRepo) GetRefreshTokenByHash(_ context.Context, tokenHash string) (*session.RefreshToken, error) {
	for _, t := range m.tokens {
		if t.RefreshTokenHash == tokenHash {
			return t, nil
		}
	}
	return nil, session.ErrRefreshTokenNotFound
}

func (m *mockRepo) UpdateRefreshTokenHash(_ context.Context, userID, sessionID, clientID, newHash string) error {
	k := session.BuildPK(userID) + "|" + session.BuildRefreshSK(sessionID, clientID)
	t, ok := m.tokens[k]
	if !ok {
		return session.ErrRefreshTokenNotFound
	}
	t.RefreshTokenHash = newHash
	return nil
}

func (m *mockRepo) ListRefreshTokensBySession(_ context.Context, userID, sessionID string) ([]*session.RefreshToken, error) {
	prefix := session.BuildPK(userID) + "|" + session.BuildRefreshSK(sessionID, "")
	var result []*session.RefreshToken
	for k, t := range m.tokens {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			result = append(result, t)
		}
	}
	return result, nil
}

func (m *mockRepo) DeleteRefreshToken(_ context.Context, userID, sessionID, clientID string) error {
	delete(m.tokens, session.BuildPK(userID)+"|"+session.BuildRefreshSK(sessionID, clientID))
	return nil
}

func TestCreate(t *testing.T) {
	svc := session.NewService(newMockRepo())
	sess, raw, err := svc.Create(context.Background(), "user1", "Chrome", "1.2.3.4", "UA", nil)
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

func TestRotateClientToken_Success(t *testing.T) {
	svc := session.NewService(newMockRepo())
	sess, _, _ := svc.Create(context.Background(), "user2", "Chrome", "1.2.3.4", "UA", nil)

	raw, err := svc.IssueClientToken(context.Background(), "user2", sess.ID(), "test-client", nil)
	if err != nil {
		t.Fatalf("issue failed: %v", err)
	}

	_, newRaw, _, err := svc.RotateClientToken(context.Background(), raw, "test-client")
	if err != nil {
		t.Fatalf("rotate failed: %v", err)
	}
	if newRaw == "" || newRaw == raw {
		t.Errorf("expected new different token")
	}
}

func TestRotateClientToken_TokenReuse(t *testing.T) {
	svc := session.NewService(newMockRepo())
	sess, _, _ := svc.Create(context.Background(), "user3", "Chrome", "1.2.3.4", "UA", nil)
	raw, _ := svc.IssueClientToken(context.Background(), "user3", sess.ID(), "test-client", nil)
	// First rotate succeeds.
	_, _, _, _ = svc.RotateClientToken(context.Background(), raw, "test-client")
	// Reusing the old raw token should detect reuse.
	_, _, _, err := svc.RotateClientToken(context.Background(), raw, "test-client")
	if !errors.Is(err, session.ErrTokenReuse) {
		t.Errorf("expected ErrTokenReuse, got %v", err)
	}
}

// A refresh token issued to one client must not be redeemable by another.
func TestRotateClientToken_ClientMismatch(t *testing.T) {
	svc := session.NewService(newMockRepo())
	sess, _, _ := svc.Create(context.Background(), "user9", "Chrome", "1.2.3.4", "UA", nil)

	raw, err := svc.IssueClientToken(context.Background(), "user9", sess.ID(), "app-a", nil)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if _, _, _, err := svc.RotateClientToken(context.Background(), raw, "app-b"); !errors.Is(err, session.ErrClientMismatch) {
		t.Errorf("expected ErrClientMismatch for foreign client, got %v", err)
	}
	// The rightful client still works, and the token was not consumed by the failed attempt.
	if _, _, _, err := svc.RotateClientToken(context.Background(), raw, "app-a"); err != nil {
		t.Errorf("rightful client should rotate, got %v", err)
	}
}

// Two clients hold independent refresh chains within one session: issuing or
// rotating one must not invalidate the other or the SSO session token.
func TestClientTokens_AreIndependent(t *testing.T) {
	svc := session.NewService(newMockRepo())
	sess, ssoRaw, _ := svc.Create(context.Background(), "user10", "Chrome", "1.2.3.4", "UA", nil)

	rawA, _ := svc.IssueClientToken(context.Background(), "user10", sess.ID(), "app-a", nil)
	rawB, _ := svc.IssueClientToken(context.Background(), "user10", sess.ID(), "app-b", nil)

	// Rotating app-b's token leaves app-a's chain and the SSO token untouched.
	if _, _, _, err := svc.RotateClientToken(context.Background(), rawB, "app-b"); err != nil {
		t.Fatalf("rotate app-b: %v", err)
	}
	if _, _, _, err := svc.RotateClientToken(context.Background(), rawA, "app-a"); err != nil {
		t.Errorf("app-a token should survive app-b rotation, got %v", err)
	}
	if _, err := svc.ValidateToken(context.Background(), ssoRaw); err != nil {
		t.Errorf("SSO token should survive client token issuance/rotation, got %v", err)
	}
}

// Revoking the parent session must kill all per-client refresh tokens under it.
func TestRevoke_CascadesToClientTokens(t *testing.T) {
	svc := session.NewService(newMockRepo())
	sess, _, _ := svc.Create(context.Background(), "user11", "Chrome", "1.2.3.4", "UA", nil)
	raw, _ := svc.IssueClientToken(context.Background(), "user11", sess.ID(), "app-a", nil)

	if err := svc.Revoke(context.Background(), "user11", sess.ID()); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, _, _, err := svc.RotateClientToken(context.Background(), raw, "app-a"); err == nil {
		t.Error("client token should be invalid after session revocation")
	}
}

func TestValidateToken_Success(t *testing.T) {
	svc := session.NewService(newMockRepo())
	sess, raw, _ := svc.Create(context.Background(), "user4", "Chrome", "1.2.3.4", "UA", nil)

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
	_, _, _ = svc.Create(context.Background(), "user5", "Chrome", "1.2.3.4", "UA", nil)

	_, err := svc.ValidateToken(context.Background(), "wrongtoken")
	if err == nil {
		t.Error("expected error for wrong token")
	}
}

// A per-client refresh token must never authenticate as an SSO session token.
func TestValidateToken_RejectsClientToken(t *testing.T) {
	svc := session.NewService(newMockRepo())
	sess, _, _ := svc.Create(context.Background(), "user12", "Chrome", "1.2.3.4", "UA", nil)
	raw, _ := svc.IssueClientToken(context.Background(), "user12", sess.ID(), "app-a", nil)

	if _, err := svc.ValidateToken(context.Background(), raw); err == nil {
		t.Error("client refresh token must not validate as SSO session token")
	}
}

func TestRevoke(t *testing.T) {
	repo := newMockRepo()
	svc := session.NewService(repo)
	sess, _, _ := svc.Create(context.Background(), "user6", "Chrome", "1.2.3.4", "UA", nil)

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
	s1, _, _ := svc.Create(context.Background(), "user7", "Chrome", "1.2.3.4", "UA", nil)
	s2, _, _ := svc.Create(context.Background(), "user7", "Firefox", "1.2.3.4", "UA", nil)

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

func TestCreateSetsAuthTimeAndAMR(t *testing.T) {
	svc := session.NewService(newMockRepo())
	sess, _, err := svc.Create(context.Background(), "u1", "dev", "1.1.1.1", "ua", []string{session.AMRPassword})
	if err != nil {
		t.Fatal(err)
	}
	if sess.AuthTime == 0 {
		t.Error("AuthTime not set")
	}
	if len(sess.AMR) != 1 || sess.AMR[0] != session.AMRPassword {
		t.Errorf("AMR = %v", sess.AMR)
	}
	if sess.LastMFAAt != 0 {
		t.Error("pwd-only login must not set LastMFAAt")
	}
}

func TestCreateWithMFAMethodSetsLastMFAAt(t *testing.T) {
	svc := session.NewService(newMockRepo())
	sess, _, _ := svc.Create(context.Background(), "u1", "dev", "1.1.1.1", "ua", []string{session.AMRPassword, session.AMRTOTP})
	if sess.LastMFAAt == 0 {
		t.Error("MFA login must set LastMFAAt")
	}
}

func TestRecordMFAUpdatesSessionOnce(t *testing.T) {
	repo := newMockRepo()
	svc := session.NewService(repo)
	sess, _, _ := svc.Create(context.Background(), "u1", "dev", "1.1.1.1", "ua", []string{session.AMRPassword})

	if err := svc.RecordMFA(context.Background(), "u1", sess.ID(), session.AMRTOTP); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.GetByID(context.Background(), "u1", sess.ID())
	if got.LastMFAAt == 0 {
		t.Error("LastMFAAt not set")
	}
	if len(got.AMR) != 2 {
		t.Errorf("AMR = %v, want [pwd otp]", got.AMR)
	}
	// idempotent append
	_ = svc.RecordMFA(context.Background(), "u1", sess.ID(), session.AMRTOTP)
	got, _ = repo.GetByID(context.Background(), "u1", sess.ID())
	if len(got.AMR) != 2 {
		t.Errorf("AMR grew on repeat method: %v", got.AMR)
	}
}
