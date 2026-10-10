package billingclient

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	oauthclient "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
)

// ClientID is the OAuth client this service acts as towards billing
// (spec § 5). Registered here with cmd/createclient; on billing's side it is a
// credential of tenant ctech, scoped to owner finance.
const ClientID = "account-billing"

// Scopes is everything plan limits need, and no more.
var Scopes = []string{"billing:entitlements:read", "billing:usage:write"}

// tokenRefreshMargin: the cached token is replaced a minute before it expires.
const tokenRefreshMargin = time.Minute

// ErrClientNotUsable is account-billing missing, public, third-party or short
// of a scope — a deployment mistake, surfaced as 503 and at startup.
var ErrClientNotUsable = errors.New("account-billing client is not usable")

type Signer interface {
	SignAccessToken(userID, sessionID, clientID string, scopes []string, issuer string, audience []string, authTime, lastMFAAt int64, amr []string, kycLevel string) (string, error)
	AccessTokenTTLSeconds() int
}

type ClientLookup interface {
	GetByID(ctx context.Context, clientID string) (*oauthclient.OAuthClient, error)
}

type AudienceResolver interface {
	AudiencesFor(ctx context.Context, scopes []string) ([]string, error)
}

// SelfSigned is the issuer signing its own client-credentials token: no secret
// leaves this process and none is stored for it (spec § 5).
type SelfSigned struct {
	signer       Signer
	clients      ClientLookup
	audiences    AudienceResolver
	issuer       string
	selfAudience string
	now          func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

func NewSelfSigned(signer Signer, clients ClientLookup, audiences AudienceResolver, issuer, selfAudience string, now func() time.Time) *SelfSigned {
	if now == nil {
		now = time.Now
	}
	return &SelfSigned{signer: signer, clients: clients, audiences: audiences, issuer: issuer, selfAudience: selfAudience, now: now}
}

func (s *SelfSigned) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.token != "" && now.Before(s.expires.Add(-tokenRefreshMargin)) {
		return s.token, nil
	}
	client, err := s.clients.GetByID(ctx, ClientID)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrClientNotUsable, err)
	}
	if client.IsPublic() || !client.FirstParty {
		return "", fmt.Errorf("%w: must be a confidential first-party client", ErrClientNotUsable)
	}
	scp := client.FilterScopes(Scopes)
	if len(scp) != len(Scopes) {
		return "", fmt.Errorf("%w: needs scopes %v, has %v", ErrClientNotUsable, Scopes, scp)
	}
	services, err := s.audiences.AudiencesFor(ctx, scp)
	if err != nil {
		return "", fmt.Errorf("resolving billing audience: %w", err)
	}
	audience := append([]string{s.selfAudience}, services...)
	// Same claims as the client_credentials grant (handler/token.go): sub and
	// azp are the client, no session, no step-up, no kyc_level.
	token, err := s.signer.SignAccessToken(ClientID, "", ClientID, scp, s.issuer, audience, 0, 0, nil, "")
	if err != nil {
		return "", fmt.Errorf("signing billing token: %w", err)
	}
	s.token = token
	s.expires = now.Add(time.Duration(s.signer.AccessTokenTTLSeconds()) * time.Second)
	return token, nil
}
