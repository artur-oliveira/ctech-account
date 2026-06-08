package user

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/artur-oliveira/ctech-account/internal/crypto"
)

// ErrInvalidCredentials is returned when login credentials are incorrect or the account is disabled.
var ErrInvalidCredentials = errors.New("invalid credentials")
var ErrAccountDisabled = errors.New("account is disabled")
var ErrCurrentPasswordIncorrect = errors.New("current password is incorrect")

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Register(ctx context.Context, email, password, firstName, lastName string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	existing, err := s.repo.GetByEmail(ctx, email)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("checking existing user: %w", err)
	}
	if existing != nil {
		return nil, ErrEmailConflict
	}

	hash, err := crypto.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("hashing password: %w", err)
	}

	u := &User{
		Email:         email,
		PasswordHash:  hash,
		FirstName:     strings.TrimSpace(firstName),
		LastName:      strings.TrimSpace(lastName),
		EmailVerified: false,
		IsEnabled:     true,
	}

	if err := s.repo.Create(ctx, u); err != nil {
		return nil, fmt.Errorf("creating user: %w", err)
	}
	return u, nil
}

func (s *Service) Login(ctx context.Context, email, password string) (*User, error) {
	u, err := s.repo.GetByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("fetching user: %w", err)
	}

	if !u.IsEnabled {
		return nil, ErrAccountDisabled
	}

	ok, err := crypto.VerifyPassword(password, u.PasswordHash)
	if err != nil {
		return nil, fmt.Errorf("verifying password: %w", err)
	}
	if !ok {
		return nil, ErrInvalidCredentials
	}

	return u, nil
}

func (s *Service) GetByID(ctx context.Context, userID string) (*User, error) {
	return s.repo.GetByID(ctx, userID)
}

func (s *Service) GetByEmail(ctx context.Context, email string) (*User, error) {
	return s.repo.GetByEmail(ctx, email)
}

func (s *Service) UpdateProfile(ctx context.Context, userID, firstName, lastName, displayName string) error {
	return s.repo.Update(ctx, userID, map[string]any{
		"first_name":   strings.TrimSpace(firstName),
		"last_name":    strings.TrimSpace(lastName),
		"display_name": strings.TrimSpace(displayName),
	})
}

func (s *Service) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) error {
	u, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("fetching user: %w", err)
	}

	ok, err := crypto.VerifyPassword(currentPassword, u.PasswordHash)
	if err != nil {
		return fmt.Errorf("verifying password: %w", err)
	}
	if !ok {
		return ErrCurrentPasswordIncorrect
	}

	hash, err := crypto.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hashing new password: %w", err)
	}
	return s.repo.Update(ctx, userID, map[string]any{"password_hash": hash})
}

func (s *Service) MarkEmailVerified(ctx context.Context, userID string) error {
	return s.repo.Update(ctx, userID, map[string]any{"email_verified": true})
}

func (s *Service) ForceSetPassword(ctx context.Context, userID, passwordHash string) error {
	return s.repo.Update(ctx, userID, map[string]any{"password_hash": passwordHash})
}

// FindOrCreateByGoogle looks up a user by email (from Google OAuth) or creates one.
func (s *Service) FindOrCreateByGoogle(ctx context.Context, googleSub, email, firstName, lastName, avatarURL string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	u, err := s.repo.GetByEmail(ctx, email)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("looking up user: %w", err)
	}
	if u != nil {
		// Existing user — update avatar if changed.
		if avatarURL != "" && u.AvatarURL != avatarURL {
			_ = s.repo.Update(ctx, u.ID(), map[string]any{"avatar_url": avatarURL})
			u.AvatarURL = avatarURL
		}
		return u, nil
	}

	// New user via Google — no password, already email-verified.
	u = &User{
		Email:         email,
		PasswordHash:  "",
		FirstName:     strings.TrimSpace(firstName),
		LastName:      strings.TrimSpace(lastName),
		AvatarURL:     avatarURL,
		EmailVerified: true,
		IsEnabled:     true,
	}
	if err := s.repo.Create(ctx, u); err != nil {
		return nil, fmt.Errorf("creating user: %w", err)
	}
	return u, nil
}

// RegisterWithHash is used by the migration endpoint — accepts a pre-computed Argon2id hash.
func (s *Service) RegisterWithHash(ctx context.Context, email, passwordHash, firstName, lastName string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	existing, err := s.repo.GetByEmail(ctx, email)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("checking existing user: %w", err)
	}
	if existing != nil {
		return existing, nil // idempotent
	}

	u := &User{
		Email:         email,
		PasswordHash:  passwordHash,
		FirstName:     strings.TrimSpace(firstName),
		LastName:      strings.TrimSpace(lastName),
		EmailVerified: true,
		IsEnabled:     true,
	}
	if err := s.repo.Create(ctx, u); err != nil {
		return nil, fmt.Errorf("creating user: %w", err)
	}
	return u, nil
}
