package apikey

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/artur-oliveira/ctech-account/internal/crypto"
	"github.com/google/uuid"
)

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Create generates a new API key. Returns the record and the raw token (shown only once).
func (s *Service) Create(ctx context.Context, userID, name string, scopes []string, expiresIn time.Duration) (*APIKey, string, error) {
	rawKey, keyHash, err := crypto.GenerateOpaqueToken()
	if err != nil {
		return nil, "", fmt.Errorf("generating api key: %w", err)
	}

	keyID := uuid.New().String()
	now := time.Now().UTC()

	var expiresAt int64
	if expiresIn > 0 {
		expiresAt = now.Add(expiresIn).Unix()
	}

	k := &APIKey{
		PK:        BuildPK(userID),
		SK:        BuildSK(keyID),
		KeyPrefix: rawKey[:8],
		KeyHash:   keyHash,
		Name:      name,
		Scopes:    scopes,
		ExpiresAt: expiresAt,
		CreatedAt: now.Format(time.RFC3339),
	}

	if err := s.repo.Create(ctx, k); err != nil {
		return nil, "", fmt.Errorf("persisting api key: %w", err)
	}
	return k, rawKey, nil
}

func (s *Service) List(ctx context.Context, userID string) ([]*APIKey, error) {
	keys, err := s.repo.ListByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	// Filter out expired keys without removing them (TTL handles cleanup).
	active := keys[:0]
	for _, k := range keys {
		if !k.IsExpired() {
			active = append(active, k)
		}
	}
	return active, nil
}

func (s *Service) Revoke(ctx context.Context, userID, keyID string) error {
	k, err := s.repo.GetByID(ctx, userID, keyID)
	if err != nil {
		return err
	}
	if k.UserID() != userID {
		return errors.New("forbidden")
	}
	return s.repo.Delete(ctx, userID, keyID)
}

// Authenticate hashes rawKey, looks it up via GSI, checks expiry, and updates last_used_at asynchronously.
func (s *Service) Authenticate(ctx context.Context, rawKey string) (*APIKey, error) {
	hash := crypto.HashToken(rawKey)
	k, err := s.repo.GetByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, errors.New("invalid api key")
		}
		return nil, fmt.Errorf("looking up api key: %w", err)
	}

	if k.IsExpired() {
		return nil, errors.New("api key expired")
	}

	go func() {
		_ = s.repo.UpdateLastUsed(context.Background(), k.UserID(), k.ID())
	}()

	return k, nil
}
