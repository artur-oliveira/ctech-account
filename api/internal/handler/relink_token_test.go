package handler_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/account/api/internal/crypto"
	oauthclient "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	sessionDomain "gopkg.aoctech.app/account/api/internal/domain/session"
	userDomain "gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/legal"
)

type stubRelinker struct {
	err   error
	calls []string
}

func (s *stubRelinker) Relink(_ context.Context, _ *userDomain.User, clientID string) error {
	s.calls = append(s.calls, clientID)
	return s.err
}

// exchange runs authorize + code exchange for a user who left poker.
func exchangeAfterUnlink(t *testing.T, relinker *stubRelinker, unlinked []string) *http.Response {
	t.Helper()
	ta := newOAuthTestAppWithRelinker(t, relinker)
	secretHash, _ := crypto.HashPassword("poker-secret")
	_ = ta.clientRepo.Create(context.Background(), &oauthclient.OAuthClient{
		PK: oauthclient.BuildPK("poker-web"), ClientType: "confidential", ClientSecretHash: secretHash,
		RedirectURIs: []string{"https://poker.example/cb"}, AllowedScopes: []string{"openid"}, FirstParty: true,
	})
	_ = ta.userRepo.Create(context.Background(), &userDomain.User{
		PK: "USER_user-back", Email: "back@example.com", EmailVerified: true, UnlinkedServices: unlinked,
		TOSVersion: legal.CurrentToSVersion, PrivacyVersion: legal.CurrentPrivacyVersion,
	})
	_, ssoToken, err := ta.sessionSvc.Create(context.Background(), "user-back", "Chrome", "1.2.3.4", "UA", []string{sessionDomain.AMRPassword}, sessionDomain.GeoData{})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1.0/authorize?client_id=poker-web&redirect_uri=https://poker.example/cb&response_type=code&scope=openid", nil)
	req.AddCookie(&http.Cookie{Name: "ctech_session", Value: ssoToken})
	resp, _ := ta.app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "https://poker.example/cb?code=") {
		t.Fatalf("authorize: %d %q", resp.StatusCode, loc)
	}
	return ta.postForm("/v1.0/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {strings.TrimPrefix(loc, "https://poker.example/cb?code=")},
		"client_id": {"poker-web"}, "client_secret": {"poker-secret"}, "redirect_uri": {"https://poker.example/cb"},
	})
}

// Review Focus 5.
func TestRelinkFailureReturns503AndMintsNothing(t *testing.T) {
	r := &stubRelinker{err: errors.New("sns down")}
	resp := exchangeAfterUnlink(t, r, []string{"poker"})
	body := bodyString(resp)
	if resp.StatusCode != http.StatusServiceUnavailable || strings.Contains(body, "access_token") {
		t.Fatalf("status %d body %s; want 503 and no token", resp.StatusCode, body)
	}
	if len(r.calls) != 1 || r.calls[0] != "poker-web" {
		t.Fatalf("relink calls = %v", r.calls)
	}
}

func TestExchangeRelinksThenMintsForAUserWhoLeftTheProduct(t *testing.T) {
	r := &stubRelinker{}
	resp := exchangeAfterUnlink(t, r, []string{"poker"})
	if resp.StatusCode != http.StatusOK || len(r.calls) != 1 {
		t.Fatalf("status %d relink calls %v; want 200 after one relink", resp.StatusCode, r.calls)
	}
}

func TestExchangeNeverAsksTheRelinkerOfAUserWhoNeverLeft(t *testing.T) {
	r := &stubRelinker{err: errors.New("must not be called")}
	resp := exchangeAfterUnlink(t, r, nil)
	if resp.StatusCode != http.StatusOK || len(r.calls) != 0 {
		t.Fatalf("status %d relink calls %v; want a plain 200 and no relink work", resp.StatusCode, r.calls)
	}
}
