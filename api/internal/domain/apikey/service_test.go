package apikey_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/account/api/internal/domain/apikey"
)

type mockRepo struct {
	keys   map[string]*apikey.APIKey // pk+sk
	byHash map[string]*apikey.APIKey
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		keys:   make(map[string]*apikey.APIKey),
		byHash: make(map[string]*apikey.APIKey),
	}
}

func (m *mockRepo) Create(_ context.Context, k *apikey.APIKey) error {
	m.keys[k.PK+"|"+k.SK] = k
	m.byHash[k.KeyHash] = k
	return nil
}

func (m *mockRepo) GetByID(_ context.Context, userID, keyID string) (*apikey.APIKey, error) {
	k := apikey.BuildPK(userID) + "|" + apikey.BuildSK(keyID)
	a, ok := m.keys[k]
	if !ok {
		return nil, apikey.ErrNotFound
	}
	return a, nil
}

func (m *mockRepo) GetByHash(_ context.Context, hash string) (*apikey.APIKey, error) {
	a, ok := m.byHash[hash]
	if !ok {
		return nil, apikey.ErrNotFound
	}
	return a, nil
}

func (m *mockRepo) ListByUserID(_ context.Context, userID string) ([]*apikey.APIKey, error) {
	pk := apikey.BuildPK(userID)
	var result []*apikey.APIKey
	for key, a := range m.keys {
		if len(key) > len(pk) && key[:len(pk)] == pk {
			result = append(result, a)
		}
	}
	return result, nil
}

func (m *mockRepo) UpdateLastUsed(_ context.Context, _, _ string) error { return nil }

func (m *mockRepo) Delete(_ context.Context, userID, keyID string) error {
	k := apikey.BuildPK(userID) + "|" + apikey.BuildSK(keyID)
	a, ok := m.keys[k]
	if !ok {
		return apikey.ErrNotFound
	}
	delete(m.byHash, a.KeyHash)
	delete(m.keys, k)
	return nil
}

func TestCreate(t *testing.T) {
	svc := apikey.NewService(newMockRepo())
	k, raw, err := svc.Create(context.Background(), "user1", "My Key", []string{"read"}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw == "" {
		t.Error("expected non-empty raw key")
	}
	if k.KeyPrefix == "" {
		t.Error("expected non-empty key prefix")
	}
	if !errors.Is(nil, nil) {
		// Sanity check errors package works.
	}
}

func TestCreate_WithExpiry(t *testing.T) {
	svc := apikey.NewService(newMockRepo())
	k, _, err := svc.Create(context.Background(), "user1", "Expiring Key", []string{"write"}, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if k.ExpiresAt == 0 {
		t.Error("expected non-zero expires_at")
	}
	if k.IsExpired() {
		t.Error("key should not be expired immediately after creation")
	}
}

func TestList_FiltersExpired(t *testing.T) {
	repo := newMockRepo()
	svc := apikey.NewService(repo)

	// Create an active key.
	_, _, _ = svc.Create(context.Background(), "user2", "Active", []string{"read"}, 0)
	// Manually create an expired key by inserting into the repo.
	expiredKey := &apikey.APIKey{
		PK:        apikey.BuildPK("user2"),
		SK:        apikey.BuildSK("expired-id"),
		KeyPrefix: "ctk_xxxx",
		KeyHash:   "fakehash",
		Name:      "Expired",
		Scopes:    []string{"read"},
		ExpiresAt: 1, // Unix epoch 1 = already expired
	}
	_ = repo.Create(context.Background(), expiredKey)

	keys, err := svc.List(context.Background(), "user2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, k := range keys {
		if k.IsExpired() {
			t.Errorf("list returned expired key: %s", k.Name)
		}
	}
}

func TestRevoke(t *testing.T) {
	repo := newMockRepo()
	svc := apikey.NewService(repo)
	k, _, _ := svc.Create(context.Background(), "user3", "ToRevoke", []string{"read"}, 0)

	err := svc.Revoke(context.Background(), "user3", k.ID())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	keys, _ := svc.List(context.Background(), "user3")
	if len(keys) != 0 {
		t.Errorf("expected 0 keys after revoke, got %d", len(keys))
	}
}

func TestAuthenticate_Success(t *testing.T) {
	svc := apikey.NewService(newMockRepo())
	_, raw, _ := svc.Create(context.Background(), "user4", "AuthKey", []string{"read"}, 0)

	k, err := svc.Authenticate(context.Background(), raw)
	if err != nil {
		t.Fatalf("authenticate failed: %v", err)
	}
	if k == nil {
		t.Error("expected non-nil key")
	}
}

func TestAuthenticate_InvalidKey(t *testing.T) {
	svc := apikey.NewService(newMockRepo())
	_, err := svc.Authenticate(context.Background(), "ctk_invalidkey")
	if err == nil {
		t.Error("expected error for invalid key")
	}
}
