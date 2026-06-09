package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/artur-oliveira/ctech-account/internal/crypto"
	"github.com/google/uuid"
)

var ErrTokenReuse = errors.New("refresh token reuse detected — session revoked")
var ErrSessionExpired = errors.New("session expired")

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Create creates a new session and returns it along with the raw refresh token.
func (s *Service) Create(ctx context.Context, userID, deviceName, ip, userAgent string) (*Session, string, error) {
	rawToken, tokenHash, err := crypto.GenerateRefreshToken()
	if err != nil {
		return nil, "", fmt.Errorf("generating refresh token: %w", err)
	}

	sessionID := uuid.New().String()
	now := time.Now().UTC()

	sess := &Session{
		PK:               BuildPK(userID),
		SK:               BuildSK(sessionID),
		RefreshTokenHash: tokenHash,
		DeviceName:       deviceName,
		IPAddress:        ip,
		UserAgent:        userAgent,
		CreatedAt:        now.Format(time.RFC3339),
		LastUsedAt:       now.Format(time.RFC3339),
		ExpiresAt:        now.Add(SessionTTL).Unix(),
	}

	if err := s.repo.Create(ctx, sess); err != nil {
		return nil, "", fmt.Errorf("persisting session: %w", err)
	}
	return sess, rawToken, nil
}

// Rotate looks up the session by rawToken hash, then atomically replaces it.
// Returns ErrTokenReuse if the token hash is not found (stale token presented).
func (s *Service) Rotate(ctx context.Context, rawToken string) (*Session, string, error) {
	sess, err := s.repo.GetByTokenHash(ctx, crypto.HashToken(rawToken))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, "", ErrTokenReuse
		}
		return nil, "", fmt.Errorf("fetching session: %w", err)
	}

	if sess.IsExpired() {
		_ = s.repo.Delete(ctx, sess.UserID(), sess.ID())
		return nil, "", ErrSessionExpired
	}

	newRaw, newHash, err := crypto.GenerateRefreshToken()
	if err != nil {
		return nil, "", fmt.Errorf("generating new refresh token: %w", err)
	}
	if err := s.repo.UpdateRefreshToken(ctx, sess.UserID(), sess.ID(), newHash); err != nil {
		return nil, "", fmt.Errorf("rotating refresh token: %w", err)
	}
	return sess, newRaw, nil
}

// ReplaceRefreshToken unconditionally issues a new refresh token for an existing session.
// Used on first OAuth code exchange when no prior API refresh token exists for the session.
func (s *Service) ReplaceRefreshToken(ctx context.Context, userID, sessionID string) (string, error) {
	sess, err := s.repo.GetByID(ctx, userID, sessionID)
	if err != nil {
		return "", fmt.Errorf("fetching session: %w", err)
	}
	if sess.IsExpired() {
		_ = s.repo.Delete(ctx, userID, sessionID)
		return "", ErrSessionExpired
	}

	newRaw, newHash, err := crypto.GenerateRefreshToken()
	if err != nil {
		return "", fmt.Errorf("generating refresh token: %w", err)
	}
	if err := s.repo.UpdateRefreshToken(ctx, userID, sessionID, newHash); err != nil {
		return "", fmt.Errorf("updating refresh token: %w", err)
	}
	return newRaw, nil
}

// ValidateToken looks up and validates a session by its raw refresh token.
func (s *Service) ValidateToken(ctx context.Context, rawToken string) (*Session, error) {
	sess, err := s.repo.GetByTokenHash(ctx, crypto.HashToken(rawToken))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, errors.New("session not found")
		}
		return nil, fmt.Errorf("fetching session: %w", err)
	}
	if sess.IsExpired() {
		_ = s.repo.Delete(ctx, sess.UserID(), sess.ID())
		return nil, ErrSessionExpired
	}
	return sess, nil
}

func (s *Service) List(ctx context.Context, userID string) ([]*Session, error) {
	return s.repo.ListByUserID(ctx, userID)
}

func (s *Service) Revoke(ctx context.Context, userID, sessionID string) error {
	return s.repo.Delete(ctx, userID, sessionID)
}

func (s *Service) RevokeAll(ctx context.Context, userID, exceptSessionID string) error {
	sessions, err := s.repo.ListByUserID(ctx, userID)
	if err != nil {
		return err
	}
	for _, sess := range sessions {
		if sess.ID() == exceptSessionID {
			continue
		}
		if err := s.repo.Delete(ctx, userID, sess.ID()); err != nil {
			return err
		}
	}
	return nil
}
