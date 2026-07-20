package totp

import (
	"context"
	"sync"

	"gopkg.aoctech.app/account/api/internal/crypto"
)

// mockRepository is an in-memory Repository that mirrors the DynamoDB
// conditional-update semantics so concurrency fixes can be tested without AWS.
type mockRepository struct {
	mu   sync.Mutex
	data map[string]*TOTPSecret
}

func newMockRepository() *mockRepository {
	return &mockRepository{data: make(map[string]*TOTPSecret)}
}

func (m *mockRepository) Create(ctx context.Context, s *TOTPSecret) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := *s
	enc, err := crypto.Seal(s.Secret)
	if err != nil {
		return err
	}
	c.EncryptedSecret = enc
	m.data[s.UserID()] = &c
	return nil
}

func (m *mockRepository) Get(ctx context.Context, userID string) (*TOTPSecret, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.data[userID]
	if !ok {
		return nil, ErrNotFound
	}
	plain := s.EncryptedSecret
	if p, err := crypto.Open(s.EncryptedSecret); err == nil {
		plain = p
	}
	out := *s
	out.Secret = plain
	return &out, nil
}

// Confirm mirrors the DynamoDB condition:
// attribute_not_exists(#verified) OR #verified = :f (false).
func (m *mockRepository) Confirm(ctx context.Context, userID string, codes []string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.data[userID]
	if s == nil {
		return false, ErrNotFound
	}
	if s.Verified {
		return false, nil
	}
	s.Verified = true
	s.BackupCodes = codes
	return true, nil
}

// ConsumeBackupCode mirrors the DynamoDB condition:
// attribute_not_exists(#version) OR #version = :cv.
func (m *mockRepository) ConsumeBackupCode(ctx context.Context, userID string, remaining []string, version int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.data[userID]
	if s == nil {
		return false, ErrNotFound
	}
	if s.Version != 0 && s.Version != version {
		return false, nil
	}
	s.BackupCodes = remaining
	if version == 0 {
		s.Version = 1
	} else {
		s.Version = version + 1
	}
	return true, nil
}

func (m *mockRepository) ReplaceBackupCodes(ctx context.Context, userID string, codes []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.data[userID]
	if s == nil {
		return ErrNotFound
	}
	s.BackupCodes = codes
	return nil
}

func (m *mockRepository) Remove(ctx context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, userID)
	return nil
}
