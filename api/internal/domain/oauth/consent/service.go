package consent

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// HasGrant reports whether the user already approved all requested scopes for clientID.
func (s *Service) HasGrant(ctx context.Context, userID, clientID string, requested []string) (bool, error) {
	g, err := s.repo.Get(ctx, userID, clientID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("fetching grant: %w", err)
	}
	return g.Covers(requested), nil
}

// Grant approves scopes for clientID, merging with any previously granted set
// (incremental consent — approving new scopes never drops old ones).
func (s *Service) Grant(ctx context.Context, userID, clientID string, scopes []string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	previous := []string(nil)
	createdAt := now
	if existing, err := s.repo.Get(ctx, userID, clientID); err == nil {
		previous = existing.Scopes
		createdAt = existing.CreatedAt
	} else if !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("fetching grant: %w", err)
	}

	seen := make(map[string]struct{}, len(previous)+len(scopes))
	merged := make([]string, 0, len(previous)+len(scopes))
	for _, sc := range previous {
		if _, ok := seen[sc]; !ok {
			seen[sc] = struct{}{}
			merged = append(merged, sc)
		}
	}
	for _, sc := range scopes {
		if _, ok := seen[sc]; !ok {
			seen[sc] = struct{}{}
			merged = append(merged, sc)
		}
	}

	return s.repo.Put(ctx, &Grant{
		PK:        BuildPK(userID),
		SK:        BuildSK(clientID),
		Scopes:    merged,
		CreatedAt: createdAt,
		UpdatedAt: now,
	})
}

func (s *Service) Revoke(ctx context.Context, userID, clientID string) error {
	return s.repo.Delete(ctx, userID, clientID)
}

func (s *Service) List(ctx context.Context, userID string) ([]*Grant, error) {
	return s.repo.ListByUser(ctx, userID)
}
