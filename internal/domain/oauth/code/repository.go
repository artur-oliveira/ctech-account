package code

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/artur-oliveira/ctech-account/internal/cache"
)

var ErrNotFound = errors.New("auth code not found or expired")

const codeTTL = 60 * time.Second
const keyPrefix = "auth_code:"

type Repository struct {
	cache *cache.Client
}

func NewRepository(c *cache.Client) *Repository {
	return &Repository{cache: c}
}

func (r *Repository) Store(ctx context.Context, codeHash string, ac *AuthCode) error {
	key := keyPrefix + codeHash
	if err := r.cache.Set(ctx, key, ac, codeTTL); err != nil {
		return fmt.Errorf("storing auth code: %w", err)
	}
	return nil
}

func (r *Repository) Get(ctx context.Context, codeHash string) (*AuthCode, error) {
	key := keyPrefix + codeHash
	var ac AuthCode
	if err := r.cache.Get(ctx, key, &ac); err != nil {
		if errors.Is(err, cache.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("fetching auth code: %w", err)
	}
	return &ac, nil
}

func (r *Repository) Delete(ctx context.Context, codeHash string) error {
	return r.cache.Delete(ctx, keyPrefix+codeHash)
}
