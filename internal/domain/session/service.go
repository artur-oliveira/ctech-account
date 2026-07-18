package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gopkg.aoctech.app/account/internal/crypto"
)

var ErrTokenReuse = errors.New("refresh token reuse detected — session revoked")
var ErrSessionExpired = errors.New("session expired")

// ErrClientMismatch is returned when a refresh token is presented by an OAuth
// client other than the one it was issued to (stolen-token replay across clients).
var ErrClientMismatch = errors.New("refresh token was not issued to this client")

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Create creates a new session and returns it along with the raw refresh token.
// amr lists the authentication methods used at login (AMRPassword, AMRTOTP, ...);
// when it contains an MFA method the session starts with a fresh MFA proof.
func (s *Service) Create(ctx context.Context, userID, deviceName, ip, userAgent string, amr []string) (*Session, string, error) {
	rawToken, tokenHash, err := crypto.GenerateRefreshToken()
	if err != nil {
		return nil, "", fmt.Errorf("generating refresh token: %w", err)
	}

	sessionID := uuid.New().String()
	now := time.Now().UTC()

	var lastMFA int64
	for _, m := range amr {
		if IsMFAMethod(m) {
			lastMFA = now.Unix()
			break
		}
	}

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
		AuthTime:         now.Unix(),
		AMR:              amr,
		LastMFAAt:        lastMFA,
	}

	if err := s.repo.Create(ctx, sess); err != nil {
		return nil, "", fmt.Errorf("persisting session: %w", err)
	}
	return sess, rawToken, nil
}

// Get returns a session by owner and id.
func (s *Service) Get(ctx context.Context, userID, sessionID string) (*Session, error) {
	return s.repo.GetByID(ctx, userID, sessionID)
}

// RecordMFA marks a successful MFA proof (login gate or step-up challenge) on
// the session so freshly issued tokens carry an up-to-date last_mfa_at claim.
func (s *Service) RecordMFA(ctx context.Context, userID, sessionID, method string) error {
	sess, err := s.repo.GetByID(ctx, userID, sessionID)
	if err != nil {
		return fmt.Errorf("fetching session: %w", err)
	}
	amr := sess.AMR
	found := false
	for _, m := range amr {
		if m == method {
			found = true
			break
		}
	}
	if !found {
		amr = append(amr, method)
	}
	return s.repo.UpdateMFA(ctx, userID, sessionID, amr, time.Now().UTC().Unix())
}

// IssueClientToken issues (or replaces) the refresh token for one OAuth client
// within an existing session. Used on OAuth code exchange. The SSO session token
// is untouched, so issuing a token to one client never logs out the browser or
// invalidates another client's refresh chain.
func (s *Service) IssueClientToken(ctx context.Context, userID, sessionID, clientID string, scopes []string) (string, error) {
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

	now := time.Now().UTC()
	t := &RefreshToken{
		PK:               BuildPK(userID),
		SK:               BuildRefreshSK(sessionID, clientID),
		RefreshTokenHash: newHash,
		SessionID:        sessionID,
		ClientID:         clientID,
		Scopes:           scopes,
		CreatedAt:        now.Format(time.RFC3339),
		LastUsedAt:       now.Format(time.RFC3339),
		ExpiresAt:        sess.ExpiresAt,
	}
	if err := s.repo.PutRefreshToken(ctx, t); err != nil {
		return "", fmt.Errorf("persisting refresh token: %w", err)
	}
	return newRaw, nil
}

// RotateClientToken validates a presented per-client refresh token and atomically
// replaces it. Returns ErrTokenReuse when the hash is unknown (stale token),
// ErrClientMismatch when presented by another client, and ErrSessionExpired when
// the parent session is gone or expired.
func (s *Service) RotateClientToken(ctx context.Context, rawToken, clientID string) (*Session, string, []string, error) {
	t, err := s.repo.GetRefreshTokenByHash(ctx, crypto.HashToken(rawToken))
	if err != nil {
		if errors.Is(err, ErrRefreshTokenNotFound) {
			return nil, "", nil, ErrTokenReuse
		}
		return nil, "", nil, fmt.Errorf("fetching refresh token: %w", err)
	}

	if t.ClientID != clientID {
		return nil, "", nil, ErrClientMismatch
	}
	if t.IsExpired() {
		_ = s.repo.DeleteRefreshToken(ctx, t.UserID(), t.SessionID, t.ClientID)
		return nil, "", nil, ErrSessionExpired
	}

	// The refresh token dies with its parent session (logout / revocation).
	sess, err := s.repo.GetByID(ctx, t.UserID(), t.SessionID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			_ = s.repo.DeleteRefreshToken(ctx, t.UserID(), t.SessionID, t.ClientID)
			return nil, "", nil, ErrSessionExpired
		}
		return nil, "", nil, fmt.Errorf("fetching session: %w", err)
	}
	if sess.IsExpired() {
		_ = s.repo.Delete(ctx, sess.UserID(), sess.ID())
		return nil, "", nil, ErrSessionExpired
	}

	newRaw, newHash, err := crypto.GenerateRefreshToken()
	if err != nil {
		return nil, "", nil, fmt.Errorf("generating new refresh token: %w", err)
	}
	if err := s.repo.UpdateRefreshTokenHash(ctx, t.UserID(), t.SessionID, t.ClientID, newHash); err != nil {
		return nil, "", nil, fmt.Errorf("rotating refresh token: %w", err)
	}
	return sess, newRaw, t.Scopes, nil
}

// RevokeClientToken deletes the refresh token matching rawToken, if any.
// Used by the RFC 7009 revocation endpoint.
func (s *Service) RevokeClientToken(ctx context.Context, rawToken string) error {
	t, err := s.repo.GetRefreshTokenByHash(ctx, crypto.HashToken(rawToken))
	if err != nil {
		return err
	}
	return s.repo.DeleteRefreshToken(ctx, t.UserID(), t.SessionID, t.ClientID)
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

// UpdateGeoData writes geo-location fields onto an existing session.
func (s *Service) UpdateGeoData(ctx context.Context, userID, sessionID, city, region string, lat, lon float64) error {
	return s.repo.UpdateGeoData(ctx, userID, sessionID, city, region, lat, lon)
}

func (s *Service) List(ctx context.Context, userID string) ([]*Session, error) {
	return s.repo.ListByUserID(ctx, userID)
}

// Revoke deletes a session and every per-client refresh token issued under it.
func (s *Service) Revoke(ctx context.Context, userID, sessionID string) error {
	if tokens, err := s.repo.ListRefreshTokensBySession(ctx, userID, sessionID); err == nil {
		for _, t := range tokens {
			_ = s.repo.DeleteRefreshToken(ctx, userID, t.SessionID, t.ClientID)
		}
	}
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
		if err := s.Revoke(ctx, userID, sess.ID()); err != nil {
			return err
		}
	}
	return nil
}
