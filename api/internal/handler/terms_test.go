package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/crypto"
	"gopkg.aoctech.app/account/api/internal/domain/audit"
	oauthclient "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	sessionDomain "gopkg.aoctech.app/account/api/internal/domain/session"
	userDomain "gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/legal"
)

// staleTermsUser downgrades the stored versions so the user looks like someone
// who accepted an earlier revision — exactly what a version bump produces.
func staleTermsUser(t *testing.T, repo *memUserRepo, userID string, updates map[string]any) {
	t.Helper()
	if err := repo.Update(context.Background(), userID, updates); err != nil {
		t.Fatalf("downgrading terms versions: %v", err)
	}
}

// seedAuthorizeClient registers a first-party client that /authorize will accept.
func seedAuthorizeClient(t *testing.T, ta *oauthTestApp) {
	t.Helper()
	if err := ta.clientRepo.Create(context.Background(), &oauthclient.OAuthClient{
		PK:            oauthclient.BuildPK("wallet"),
		ClientType:    "confidential",
		RedirectURIs:  []string{"https://wallet.example/cb"},
		AllowedScopes: []string{"openid"},
		FirstParty:    true,
	}); err != nil {
		t.Fatalf("seeding client: %v", err)
	}
}

func authorizeAsUser(t *testing.T, ta *oauthTestApp, userID string) *http.Response {
	t.Helper()
	_, ssoToken, err := ta.sessionSvc.Create(context.Background(), userID, "Chrome", "1.2.3.4", "UA", []string{sessionDomain.AMRPassword})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet,
		"/v1.0/authorize?client_id=wallet&redirect_uri=https://wallet.example/cb&response_type=code&scope=openid", nil)
	req.AddCookie(&http.Cookie{Name: "ctech_session", Value: ssoToken})
	resp, _ := ta.app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	return resp
}

// A stale acceptance must block the authorization code — every product behind
// this IdP inherits the gate, so no code may leave until the user re-accepts.
func TestAuthorize_StaleTerms_RedirectsToAcceptTermsWithoutIssuingCode(t *testing.T) {
	ta := newOAuthTestApp(t)
	seedAuthorizeClient(t, ta)

	if err := ta.userRepo.Create(context.Background(), &userDomain.User{
		PK: "USER_user-stale", Email: "stale@example.com", EmailVerified: true,
		TOSVersion: "1.0", PrivacyVersion: "1.0",
	}); err != nil {
		t.Fatalf("seeding user: %v", err)
	}

	resp := authorizeAsUser(t, ta, "user-stale")
	loc := resp.Header.Get("Location")

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302, got %d", resp.StatusCode)
	}
	if !strings.HasPrefix(loc, "http://app.localhost/accept-terms?") {
		t.Fatalf("expected redirect to the accept-terms interstitial, got %q", loc)
	}
	if strings.Contains(loc, "wallet.example") || strings.Contains(loc, "code=") {
		t.Fatalf("an authorization code must not be issued to a gated user, got %q", loc)
	}
	// The interstitial has no session cookie to work with on a fresh login, so it
	// must carry a token, plus the cosmetic flags telling it what to display.
	if !strings.Contains(loc, "token=") {
		t.Errorf("expected a single-use token in the redirect, got %q", loc)
	}
	if !strings.Contains(loc, "tos=1") || !strings.Contains(loc, "privacy=1") {
		t.Errorf("expected both documents flagged pending, got %q", loc)
	}
}

// Only the document that actually moved is flagged for display.
func TestAuthorize_StalePrivacyOnly_FlagsOnlyPrivacy(t *testing.T) {
	ta := newOAuthTestApp(t)
	seedAuthorizeClient(t, ta)

	if err := ta.userRepo.Create(context.Background(), &userDomain.User{
		PK: "USER_user-privacy", Email: "privacy@example.com", EmailVerified: true,
		TOSVersion: legal.CurrentToSVersion, PrivacyVersion: "1.0",
	}); err != nil {
		t.Fatalf("seeding user: %v", err)
	}

	loc := authorizeAsUser(t, ta, "user-privacy").Header.Get("Location")
	if !strings.Contains(loc, "privacy=1") {
		t.Errorf("expected privacy flagged pending, got %q", loc)
	}
	if strings.Contains(loc, "tos=1") {
		t.Errorf("expected the ToS not to be flagged, got %q", loc)
	}
}

// Regression: a user in good standing must still get a code.
func TestAuthorize_CurrentTerms_IssuesCode(t *testing.T) {
	ta := newOAuthTestApp(t)
	seedAuthorizeClient(t, ta)

	if err := ta.userRepo.Create(context.Background(), &userDomain.User{
		PK: "USER_user-ok", Email: "ok@example.com", EmailVerified: true,
		TOSVersion: legal.CurrentToSVersion, PrivacyVersion: legal.CurrentPrivacyVersion,
	}); err != nil {
		t.Fatalf("seeding user: %v", err)
	}

	loc := authorizeAsUser(t, ta, "user-ok").Header.Get("Location")
	if !strings.HasPrefix(loc, "https://wallet.example/cb?code=") {
		t.Fatalf("expected a code redirect, got %q", loc)
	}
}

// The re-acceptance path runs for a user who ALREADY holds a session. Issuing a
// second one here would duplicate the device's session for no reason.
func TestAcceptTerms_Reaccept_StampsWithoutIssuingSession(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "reaccept@example.com", "Str0ngPass!", "Re")
	staleTermsUser(t, app.userRepo, u.ID(), map[string]any{"tos_version": "1.0", "privacy_version": "1.0"})

	raw, hashHex, err := crypto.GenerateMFAToken()
	if err != nil {
		t.Fatalf("generating token: %v", err)
	}
	payload := termsTokenPayload{
		UserID:      u.ID(),
		ContinueURL: "/v1.0/authorize?client_id=wallet",
		Reaccept:    true,
	}
	if err := app.socialCache.Set(context.Background(), "terms_token:"+hashHex, payload, 10*time.Minute); err != nil {
		t.Fatalf("seeding token: %v", err)
	}

	resp := app.do(http.MethodPost, "/v1.0/auth/accept-terms", map[string]any{
		"token": raw, "accept_tos": true, "accept_privacy": true,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}

	for _, c := range resp.Cookies() {
		if c.Name == "ctech_session" {
			t.Error("re-acceptance must not issue a second session")
		}
	}

	got, err := app.userSvc.GetByID(context.Background(), u.ID())
	if err != nil {
		t.Fatalf("fetching user: %v", err)
	}
	if legal.PendingFor(got.TOSVersion, got.PrivacyVersion).Any() {
		t.Errorf("expected both documents stamped, got tos=%q privacy=%q", got.TOSVersion, got.PrivacyVersion)
	}

	events, _, _ := app.auditRepo.QueryByUser(context.Background(), u.ID(), "", 10)
	for _, e := range events {
		if e.EventType == audit.EventLoginSuccess {
			t.Error("re-acceptance must not record a login event")
		}
	}
}

// The in-app gate: a session already holding a refreshable token never passes
// through /authorize again, so it clears the bump with its bearer token.
func TestAccountTerms_Accept_ClearsPending(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "inapp@example.com", "Str0ngPass!", "In")
	staleTermsUser(t, app.userRepo, u.ID(), map[string]any{"tos_version": "1.0"})
	token := app.issueToken(t, u.ID())

	// The profile drives the gate, so it must advertise the pending document.
	profile := app.doWithToken(http.MethodGet, "/v1.0/account/profile", nil, token)
	var before map[string]any
	readJSON(t, profile, &before)
	pending, _ := before["terms_pending"].(map[string]any)
	if pending["tos"] != true || pending["privacy"] != false {
		t.Fatalf("expected only the ToS pending, got %v", before["terms_pending"])
	}

	resp := app.doWithToken(http.MethodPost, "/v1.0/account/terms/accept", map[string]any{
		"accept_tos": true,
	}, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}

	var body map[string]any
	readJSON(t, resp, &body)
	cleared, _ := body["terms_pending"].(map[string]any)
	if cleared["tos"] != false || cleared["privacy"] != false {
		t.Errorf("expected nothing pending after acceptance, got %v", body["terms_pending"])
	}

	got, _ := app.userSvc.GetByID(context.Background(), u.ID())
	if got.TOSVersion != legal.CurrentToSVersion {
		t.Errorf("expected ToS stamped at %s, got %q", legal.CurrentToSVersion, got.TOSVersion)
	}
}

// The client's flags are a confirmation, not the source of truth: leaving a
// pending document unconfirmed must fail rather than stamp it anyway.
func TestAccountTerms_UnconfirmedPendingDocument_422(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "partial@example.com", "Str0ngPass!", "Pa")
	staleTermsUser(t, app.userRepo, u.ID(), map[string]any{"tos_version": "1.0", "privacy_version": "1.0"})
	token := app.issueToken(t, u.ID())

	resp := app.doWithToken(http.MethodPost, "/v1.0/account/terms/accept", map[string]any{
		"accept_tos": true, // privacy is pending too, and was not accepted
	}, token)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", resp.StatusCode, bodyString(resp))
	}

	got, _ := app.userSvc.GetByID(context.Background(), u.ID())
	if got.TOSVersion != "1.0" {
		t.Errorf("a rejected request must stamp nothing, got tos=%q", got.TOSVersion)
	}
}
