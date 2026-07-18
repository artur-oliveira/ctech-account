package handler_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"gopkg.aoctech.app/account/internal/crypto"
	"gopkg.aoctech.app/account/internal/domain/audit"
)

// termsTokenPayload mirrors the unexported struct in internal/handler/terms.go —
// tests seed the cache directly since neither the Google token-exchange round
// trip nor the /authorize gate can be driven end to end from here.
type termsTokenPayload struct {
	UserID      string `json:"user_id"`
	DeviceName  string `json:"device_name"`
	IP          string `json:"ip"`
	UserAgent   string `json:"user_agent"`
	ContinueURL string `json:"continue_url"`
	Reaccept    bool   `json:"reaccept"`
}

// seedAcceptTermsToken creates a brand-new Google account (created=true, no ToS
// acceptance yet) and seeds the cache token exactly as redirectToAcceptTerms
// would after a real callback, returning the raw token to present to the endpoint.
func seedAcceptTermsToken(t *testing.T, app *testApp, email, continueURL string) (userID, rawToken string) {
	t.Helper()
	u, created, err := app.userSvc.FindOrCreateByGoogle(context.Background(), "gsub-"+email, email, "G", "User", "")
	if err != nil {
		t.Fatalf("google create: %v", err)
	}
	if !created {
		t.Fatal("expected a brand-new account")
	}

	raw, hashHex, err := crypto.GenerateMFAToken()
	if err != nil {
		t.Fatalf("generating token: %v", err)
	}
	payload := termsTokenPayload{UserID: u.ID(), DeviceName: "test", IP: "127.0.0.1", UserAgent: "go-test", ContinueURL: continueURL}
	if err := app.socialCache.Set(context.Background(), "terms_token:"+hashHex, payload, 10*time.Minute); err != nil {
		t.Fatalf("seeding token: %v", err)
	}
	return u.ID(), raw
}

func TestAcceptTerms_IssuesSessionAndStampsAcceptance(t *testing.T) {
	app := newTestApp(t)
	userID, rawToken := seedAcceptTermsToken(t, app, "newgoogle@example.com", "/account")

	resp := app.do(http.MethodPost, "/v1.0/auth/accept-terms", map[string]any{
		"token":          rawToken,
		"accept_tos":     true,
		"accept_privacy": true,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}

	var body map[string]any
	readJSON(t, resp, &body)
	if body["redirect"] != "/account" {
		t.Errorf("expected redirect=/account, got %v", body["redirect"])
	}

	found := false
	for _, c := range resp.Cookies() {
		if c.Name == "ctech_session" {
			found = true
		}
	}
	if !found {
		t.Error("expected ctech_session cookie to be set")
	}

	u, err := app.userSvc.GetByID(context.Background(), userID)
	if err != nil {
		t.Fatalf("fetching user: %v", err)
	}
	if u.TOSAcceptedAt == "" || u.PrivacyAcceptedAt == "" {
		t.Error("expected ToS/Privacy acceptance stamped after accept-terms")
	}

	events, _, _ := app.auditRepo.QueryByUser(context.Background(), userID, "", 10)
	var sawTerms, sawLogin bool
	for _, e := range events {
		if e.EventType == audit.EventTermsAccepted {
			sawTerms = true
		}
		if e.EventType == audit.EventLoginSuccess {
			sawLogin = true
		}
	}
	if !sawTerms || !sawLogin {
		t.Errorf("expected both terms_accepted and login_success audit events, got terms=%v login=%v", sawTerms, sawLogin)
	}
}

func TestAcceptTerms_InvalidToken_401(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodPost, "/v1.0/auth/accept-terms", map[string]any{
		"token":          "bogus-token",
		"accept_tos":     true,
		"accept_privacy": true,
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

// The token is single-use — replaying it must fail, not silently re-issue a session.
func TestAcceptTerms_TokenIsSingleUse(t *testing.T) {
	app := newTestApp(t)
	_, rawToken := seedAcceptTermsToken(t, app, "replay@example.com", "/account")

	first := app.do(http.MethodPost, "/v1.0/auth/accept-terms", map[string]any{
		"token": rawToken, "accept_tos": true, "accept_privacy": true,
	})
	if first.StatusCode != http.StatusOK {
		t.Fatalf("expected first call to succeed, got %d: %s", first.StatusCode, bodyString(first))
	}

	second := app.do(http.MethodPost, "/v1.0/auth/accept-terms", map[string]any{
		"token": rawToken, "accept_tos": true, "accept_privacy": true,
	})
	if second.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected replay to be rejected with 401, got %d", second.StatusCode)
	}
}

func TestAcceptTerms_WithoutAcceptTerms_422(t *testing.T) {
	app := newTestApp(t)
	_, rawToken := seedAcceptTermsToken(t, app, "noaccept@example.com", "/account")

	resp := app.do(http.MethodPost, "/v1.0/auth/accept-terms", map[string]any{
		"token": rawToken,
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}
