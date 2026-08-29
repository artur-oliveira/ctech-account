package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"uuid"

	"gopkg.aoctech.app/account/api/internal/crypto"
	"gopkg.aoctech.app/api-commons/observability"
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
func (s *Service) Create(ctx context.Context, userID, deviceName, ip, userAgent string, amr []string, geoData GeoData) (*Session, string, error) {
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
		GeoCity:          geoData.City,
		GeoRegion:        geoData.Region,
		GeoCountry:       geoData.Country,
		GeoLatitude:      geoData.Latitude,
		GeoLongitude:     geoData.Longitude,
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
		if err := s.repo.Delete(ctx, userID, sessionID); err != nil {
			observability.Warn(ctx, "session: failed to delete expired session", err,
				"user_id", userID, "session_id", sessionID)
		}
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
	hash := crypto.HashToken(rawToken)
	t, err := s.repo.GetRefreshTokenByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, ErrRefreshTokenNotFound) {
			// The presented hash is not the current one. If a consumed-token
			// marker still points this hash at a session, the token was valid and
			// already rotated by someone else — a replay of a captured token. The
			// grant is compromised, so revoke the whole session (OAuth BCP
			// §4.13.2) instead of letting the stolen chain keep working.
			if c, cErr := s.repo.GetConsumedByHash(ctx, hash); cErr == nil && c != nil {
				if revokeErr := s.Revoke(ctx, c.UserID, c.SessionID); revokeErr != nil {
					observability.Error(ctx, "session: failed to revoke session after token reuse", revokeErr,
						"user_id", c.UserID, "session_id", c.SessionID)
				}
			} else if cErr != nil && !errors.Is(cErr, ErrRefreshTokenNotFound) {
				observability.Warn(ctx, "session: failed to check consumed-token marker", cErr)
			}
			return nil, "", nil, ErrTokenReuse
		}
		return nil, "", nil, fmt.Errorf("fetching refresh token: %w", err)
	}

	if t.ClientID != clientID {
		return nil, "", nil, ErrClientMismatch
	}
	if t.IsExpired() {
		if err := s.repo.DeleteRefreshToken(ctx, t.UserID(), t.SessionID, t.ClientID); err != nil {
			observability.Warn(ctx, "session: failed to delete expired refresh token", err,
				"user_id", t.UserID(), "session_id", t.SessionID, "client_id", t.ClientID)
		}
		return nil, "", nil, ErrSessionExpired
	}

	// The refresh token dies with its parent session (logout / revocation).
	sess, err := s.repo.GetByID(ctx, t.UserID(), t.SessionID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			if deleteErr := s.repo.DeleteRefreshToken(ctx, t.UserID(), t.SessionID, t.ClientID); deleteErr != nil {
				observability.Warn(ctx, "session: failed to delete orphan refresh token", deleteErr,
					"user_id", t.UserID(), "session_id", t.SessionID, "client_id", t.ClientID)
			}
			return nil, "", nil, ErrSessionExpired
		}
		return nil, "", nil, fmt.Errorf("fetching session: %w", err)
	}
	if sess.IsExpired() {
		if err := s.repo.Delete(ctx, sess.UserID(), sess.ID()); err != nil {
			observability.Warn(ctx, "session: failed to delete expired parent session", err,
				"user_id", sess.UserID(), "session_id", sess.ID())
		}
		return nil, "", nil, ErrSessionExpired
	}

	newRaw, newHash, err := crypto.GenerateRefreshToken()
	if err != nil {
		return nil, "", nil, fmt.Errorf("generating new refresh token: %w", err)
	}
	oldHash := t.RefreshTokenHash
	now := time.Now().UTC()
	// Create new refresh token with fresh TTL
	newToken := &RefreshToken{
		PK:               BuildPK(t.UserID()),
		SK:               BuildRefreshSK(t.SessionID, t.ClientID),
		RefreshTokenHash: newHash,
		SessionID:        t.SessionID,
		ClientID:         t.ClientID,
		Scopes:           t.Scopes,
		CreatedAt:        now.Format(time.RFC3339),
		LastUsedAt:       now.Format(time.RFC3339),
		ExpiresAt:        now.Add(SessionTTL).Unix(),
	}
	// Put consumed token marker for the old token (best-effort)
	if err := s.repo.PutConsumedToken(ctx, t.UserID(), t.SessionID, t.ClientID, oldHash, sess.ExpiresAt); err != nil {
		observability.Error(ctx, "session: failed to persist consumed-token marker", err,
			"user_id", t.UserID(), "session_id", t.SessionID, "client_id", t.ClientID)
	}
	// Delete the old refresh token record
	if err := s.repo.DeleteRefreshToken(ctx, t.UserID(), t.SessionID, t.ClientID); err != nil {
		return nil, "", nil, fmt.Errorf("deleting old refresh token: %w", err)
	}
	// Put the new refresh token record
	if err := s.repo.PutRefreshToken(ctx, newToken); err != nil {
		return nil, "", nil, fmt.Errorf("persisting new refresh token: %w", err)
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
		if err := s.repo.Delete(ctx, sess.UserID(), sess.ID()); err != nil {
			observability.Warn(ctx, "session: failed to delete expired session", err,
				"user_id", sess.UserID(), "session_id", sess.ID())
		}
		return nil, ErrSessionExpired
	}
	return sess, nil
}

func (s *Service) List(ctx context.Context, userID string) ([]*Session, error) {
	return s.repo.ListByUserID(ctx, userID)
}

// HasSeenDevice reports whether userID has a prior (non-expired) session from
// the same deviceName and country. Called before Create, so the session being
// created is never in the comparison set. Errors are the caller's to decide
// how to treat — the login-notification call sites fail toward "not new"
// (better a missed email than a false alarm).
func (s *Service) HasSeenDevice(ctx context.Context, userID, deviceName, country string) (bool, error) {
	sessions, err := s.repo.ListByUserID(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("listing sessions: %w", err)
	}
	for _, sess := range sessions {
		if sess.DeviceName == deviceName && sess.GeoCountry == country {
			return true, nil
		}
	}
	return false, nil
}

// Revoke deletes a session and every per-client refresh token issued under it.
func (s *Service) Revoke(ctx context.Context, userID, sessionID string) error {
	tokens, err := s.repo.ListRefreshTokensBySession(ctx, userID, sessionID)
	if err != nil {
		return fmt.Errorf("listing session refresh tokens: %w", err)
	}
	for _, t := range tokens {
		if err := s.repo.DeleteRefreshToken(ctx, userID, t.SessionID, t.ClientID); err != nil {
			return fmt.Errorf("deleting client refresh token: %w", err)
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
