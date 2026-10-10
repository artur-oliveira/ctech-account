package billingclient

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	oauthclient "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
)

type fakeSigner struct {
	calls    int
	sub, azp string
	scopes   []string
	aud      []string
}

func (f *fakeSigner) SignAccessToken(userID, sessionID, clientID string, scopes []string, issuer string, audience []string, _, _ int64, _ []string, _ string) (string, error) {
	f.calls++
	f.sub, f.azp, f.scopes, f.aud = userID, clientID, scopes, audience
	if sessionID != "" || issuer != "https://accounts.example" {
		return "", errors.New("wrong sid or issuer")
	}
	return "tok", nil
}
func (f *fakeSigner) AccessTokenTTLSeconds() int { return 900 }

type fakeClients struct{ client *oauthclient.OAuthClient }

func (f fakeClients) GetByID(context.Context, string) (*oauthclient.OAuthClient, error) {
	if f.client == nil {
		return nil, oauthclient.ErrNotFound
	}
	return f.client, nil
}

type fakeAudiences struct{}

func (fakeAudiences) AudiencesFor(context.Context, []string) ([]string, error) {
	return []string{"https://billing.aoctech.app"}, nil
}

func registered() *oauthclient.OAuthClient {
	return &oauthclient.OAuthClient{
		PK: oauthclient.BuildPK(ClientID), ClientType: "confidential", FirstParty: true,
		AllowedScopes: []string{"billing:entitlements:read", "billing:usage:write"},
	}
}

func TestTheTokenIsCachedUntilAMinuteBeforeItExpires(t *testing.T) {
	signer := &fakeSigner{}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	src := NewSelfSigned(signer, fakeClients{registered()}, fakeAudiences{}, "https://accounts.example", "https://accounts-api.example", func() time.Time { return now })
	ctx := context.Background()

	if tok, err := src.Token(ctx); err != nil || tok != "tok" {
		t.Fatalf("Token = %q, %v", tok, err)
	}
	if signer.sub != ClientID || signer.azp != ClientID || !slices.Equal(signer.scopes, Scopes) {
		t.Fatalf("claims sub=%s azp=%s scopes=%v", signer.sub, signer.azp, signer.scopes)
	}
	if !slices.Equal(signer.aud, []string{"https://accounts-api.example", "https://billing.aoctech.app"}) {
		t.Fatalf("aud = %v", signer.aud)
	}
	now = now.Add(13*time.Minute + 59*time.Second)
	_, _ = src.Token(ctx)
	if signer.calls != 1 {
		t.Fatalf("re-signed inside the window: %d calls", signer.calls)
	}
	now = now.Add(2 * time.Second) // past exp − 1 min
	_, _ = src.Token(ctx)
	if signer.calls != 2 {
		t.Fatalf("not re-signed a minute before expiry: %d calls", signer.calls)
	}
}

// Review Focus 5.
func TestAnUnusableClientIsRefused(t *testing.T) {
	ctx := context.Background()
	narrow := registered()
	narrow.AllowedScopes = []string{"billing:entitlements:read"}
	thirdParty := registered()
	thirdParty.FirstParty = false
	for name, client := range map[string]*oauthclient.OAuthClient{"missing": nil, "narrow": narrow, "third-party": thirdParty} {
		src := NewSelfSigned(&fakeSigner{}, fakeClients{client}, fakeAudiences{}, "https://accounts.example", "a", time.Now)
		if _, err := src.Token(ctx); err == nil {
			t.Fatalf("%s: a token was signed", name)
		}
	}
}
