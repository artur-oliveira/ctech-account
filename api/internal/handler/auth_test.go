package handler_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"gopkg.aoctech.app/account/api/internal/domain/audit"
	oauthclientDomain "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	sessionDomain "gopkg.aoctech.app/account/api/internal/domain/session"
)

func TestRegister_Accepted(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodPost, "/v1.0/auth/register", map[string]any{
		"email":        "new@example.com",
		"password":     "securepass",
		"first_name":   "Alice",
		"accept_terms": true,
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("expected 202, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

// Registering without explicitly accepting ToS/Privacy must be rejected — the
// checkbox is not optional.
func TestRegister_WithoutAcceptTerms_422(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodPost, "/v1.0/auth/register", map[string]any{
		"email":      "noterm@example.com",
		"password":   "securepass",
		"first_name": "Alice",
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	assertProblemJSON(t, resp)
}

// A successful registration stamps the current ToS/Privacy versions on the
// account and records an audit event — both are required for compliance.
func TestRegister_StampsTermsAndAudits(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodPost, "/v1.0/auth/register", map[string]any{
		"email":        "termsok@example.com",
		"password":     "securepass",
		"first_name":   "Alice",
		"accept_terms": true,
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", resp.StatusCode, bodyString(resp))
	}

	u, err := app.userSvc.GetByEmail(context.Background(), "termsok@example.com")
	if err != nil {
		t.Fatalf("fetching registered user: %v", err)
	}
	if u.TOSAcceptedAt == "" || u.PrivacyAcceptedAt == "" {
		t.Error("expected ToS/Privacy acceptance to be stamped on the new account")
	}

	events, _, err := app.auditRepo.QueryByUser(context.Background(), u.ID(), "", 10)
	if err != nil {
		t.Fatalf("querying audit: %v", err)
	}
	found := false
	for _, e := range events {
		if e.EventType == audit.EventTermsAccepted {
			found = true
		}
	}
	if !found {
		t.Error("expected auth.terms_accepted audit event")
	}
}

// Registering an already-taken address must be indistinguishable from a fresh
// registration — same status, same body — or the endpoint becomes an email oracle.
func TestRegister_DuplicateEmail_IsIndistinguishable(t *testing.T) {
	app := newTestApp(t)

	fresh := app.do(http.MethodPost, "/v1.0/auth/register",
		map[string]any{"email": "dup@example.com", "password": "pass1234", "first_name": "A", "accept_terms": true})
	freshStatus, freshBody := fresh.StatusCode, bodyString(fresh)

	dup := app.do(http.MethodPost, "/v1.0/auth/register",
		map[string]any{"email": "dup@example.com", "password": "pass1234", "first_name": "A", "accept_terms": true})
	dupStatus, dupBody := dup.StatusCode, bodyString(dup)

	if freshStatus != http.StatusAccepted || dupStatus != http.StatusAccepted {
		t.Fatalf("expected both 202, got fresh=%d dup=%d", freshStatus, dupStatus)
	}
	if freshBody != dupBody {
		t.Errorf("responses differ — enumeration oracle:\n fresh=%s\n dup  =%s", freshBody, dupBody)
	}
}

// Email verification is a hard gate: correct password but unverified email must
// not produce a session.
func TestLogin_UnverifiedEmail_403(t *testing.T) {
	app := newTestApp(t)
	app.registerUnverifiedUser(t, "unverified@example.com", "password123", "Eve")

	resp := app.do(http.MethodPost, "/v1.0/auth/login", map[string]any{
		"email":    "unverified@example.com",
		"password": "password123",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for unverified email, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	assertProblemJSON(t, resp)
}

// A wrong password on an unverified account must look like any other bad
// credential — never reveal that the address exists but is unverified.
func TestLogin_UnverifiedEmail_WrongPassword_401(t *testing.T) {
	app := newTestApp(t)
	app.registerUnverifiedUser(t, "unverified2@example.com", "password123", "Eve")

	resp := app.do(http.MethodPost, "/v1.0/auth/login", map[string]any{
		"email":    "unverified2@example.com",
		"password": "wrongpassword",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

func TestRegister_ValidationError_422(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodPost, "/v1.0/auth/register", map[string]any{
		"email":    "notanemail",
		"password": "short",
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	assertProblemJSON(t, resp)
}

func TestRegister_MissingBody_400(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodPost, "/v1.0/auth/register", nil)
	if resp.StatusCode != http.StatusUnprocessableEntity && resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 4xx, got %d", resp.StatusCode)
	}
}

func TestLogin_Success(t *testing.T) {
	app := newTestApp(t)
	app.registerUser(t, "login@example.com", "password123", "Bob")

	resp := app.do(http.MethodPost, "/v1.0/auth/login", map[string]any{
		"email":    "login@example.com",
		"password": "password123",
	})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}

	var body map[string]any
	readJSON(t, resp, &body)
	if body["user_id"] == "" {
		t.Error("expected user_id in response")
	}
}

func TestLogin_InvalidCredentials_401(t *testing.T) {
	app := newTestApp(t)
	app.registerUser(t, "wrong@example.com", "correct", "C")

	resp := app.do(http.MethodPost, "/v1.0/auth/login", map[string]any{
		"email":    "wrong@example.com",
		"password": "incorrect",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
	assertProblemJSON(t, resp)
}

func TestLogin_UnknownUser_401(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodPost, "/v1.0/auth/login", map[string]any{
		"email":    "nobody@example.com",
		"password": "anything",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
	assertProblemJSON(t, resp)
}

func TestLogout_NoAuth_NoContent(t *testing.T) {
	// Logout without a session should still succeed (idempotent).
	app := newTestApp(t)
	resp := app.do(http.MethodPost, "/v1.0/auth/logout", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
	}
}

// TestLogout_RevokesServerSideSession is the regression test for a logout that
// only cleared cookies: /auth/logout runs without RequireAuth, so it must
// identify the session from the ctech_session cookie and revoke it server-side.
// Otherwise a copied/stolen session token stays valid for the full 90-day TTL.
func TestLogout_RevokesServerSideSession(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "logout@example.com", "securepass", "Alice")
	_, rawToken, err := app.sessionSvc.Create(context.Background(), u.ID(), "Chrome", "1.2.3.4", "UA", []string{sessionDomain.AMRPassword})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	resp := app.do(http.MethodPost, "/v1.0/auth/logout", nil,
		map[string]string{"Cookie": "ctech_session=" + rawToken})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	if _, err := app.sessionSvc.ValidateToken(context.Background(), rawToken); err == nil {
		t.Fatal("expected the SSO session to be revoked by logout, but it still validates")
	}
}

// TestEndSession_RevokesSSOSessionAndRedirects is the regression test for the
// "logout bounces straight back to the dashboard" bug: a downstream RP (dfe)
// that only clears its own tokens leaves ctech_session valid, so /authorize
// silently re-authenticates. RP-initiated logout must revoke the SSO session
// itself, not just redirect.
func TestEndSession_RevokesSSOSessionAndRedirects(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "endsession@example.com", "securepass", "Alice")
	if err := app.clientRepo.Create(context.Background(), &oauthclientDomain.OAuthClient{
		PK:           oauthclientDomain.BuildPK("dfe"),
		RedirectURIs: []string{"https://dfe.example/callback"},
	}); err != nil {
		t.Fatalf("seeding client: %v", err)
	}
	_, rawToken, err := app.sessionSvc.Create(context.Background(), u.ID(), "Chrome", "1.2.3.4", "UA", []string{sessionDomain.AMRPassword})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	resp := app.do(http.MethodGet,
		"/v1.0/auth/end-session?client_id=dfe&post_logout_redirect_uri=https://dfe.example/login",
		nil, map[string]string{"Cookie": "ctech_session=" + rawToken})

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	if loc := resp.Header.Get("Location"); loc != "https://dfe.example/login" {
		t.Fatalf("expected redirect to the validated post_logout_redirect_uri, got %q", loc)
	}

	if _, err := app.sessionSvc.ValidateToken(context.Background(), rawToken); err == nil {
		t.Fatal("expected the SSO session to be revoked by end-session, but it still validates")
	}
}

// TestEndSession_UnregisteredRedirect_FallsBackToDefault verifies an
// unregistered post_logout_redirect_uri cannot be used as an open redirect —
// it must fall back to the default (AppURL + /login) instead.
func TestEndSession_UnregisteredRedirect_FallsBackToDefault(t *testing.T) {
	app := newTestApp(t)
	if err := app.clientRepo.Create(context.Background(), &oauthclientDomain.OAuthClient{
		PK:           oauthclientDomain.BuildPK("dfe"),
		RedirectURIs: []string{"https://dfe.example/callback"},
	}); err != nil {
		t.Fatalf("seeding client: %v", err)
	}

	resp := app.do(http.MethodGet,
		"/v1.0/auth/end-session?client_id=dfe&post_logout_redirect_uri=https://evil.example/steal", nil)

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	if loc := resp.Header.Get("Location"); strings.Contains(loc, "evil.example") {
		t.Fatalf("must never redirect to an unregistered URI, Location=%q", loc)
	}
}

// TestEndSession_NoCookie_StillRedirects verifies end-session is idempotent —
// calling it with no active SSO session must not error.
func TestEndSession_NoCookie_StillRedirects(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodGet, "/v1.0/auth/end-session", nil)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

// assertProblemJSON verifies the response Content-Type is application/problem+json.
func assertProblemJSON(t *testing.T, resp *http.Response) {
	t.Helper()
	ct := resp.Header.Get("Content-Type")
	if ct != "application/problem+json" {
		t.Errorf("expected Content-Type application/problem+json, got %q", ct)
	}
}
