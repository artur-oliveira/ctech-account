package handler_test

import (
	"net/http"
	"testing"
	"time"
)

// Account Resource Server routes rely on their exact account:* grant, not on
// the OAuth client's identity. A downstream token carrying only OIDC scopes
// must therefore be rejected even though its signature and audience are valid.
func TestAccountRoutes_RejectOtherClientWithoutAccountScopes(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "otherclient@example.com", "securepass", "Alice")

	// Same shape as a real downstream-client token after the userinfo audience
	// fix: valid signature, aud includes this service's own audience, but azp
	// is a different OAuth client (not this service's own frontend).
	token, err := app.jwtSvc.SignAccessToken(u.ID(), "sess-other", "dfe",
		[]string{"openid", "profile", "email"}, "http://localhost", []string{"http://localhost", "https://dfe-api.example"},
		time.Now().Unix(), 0, nil, "")
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}

	paths := []string{"/v1.0/account/profile", "/v1.0/account/sessions", "/v1.0/account/api-keys", "/v1.0/account/activity"}
	for _, path := range paths {
		resp := app.doWithToken(http.MethodGet, path, nil, token)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: expected 403 for a different client's token, got %d: %s", path, resp.StatusCode, bodyString(resp))
		}
	}

	stepUpResp := app.doWithToken(http.MethodPost, "/v1.0/auth/step-up", map[string]any{"method": "totp", "code": "123456"}, token)
	if stepUpResp.StatusCode != http.StatusForbidden {
		t.Errorf("/v1.0/auth/step-up: expected 403 for a different client's token, got %d: %s", stepUpResp.StatusCode, bodyString(stepUpResp))
	}
}

// The trusted SPA receives the full Account grant during bootstrap.
func TestAccountRoutes_AcceptSelfClientToken(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "selfclient@example.com", "securepass", "Alice")

	token := app.issueToken(t, u.ID())

	resp := app.doWithToken(http.MethodGet, "/v1.0/account/profile", nil, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for this service's own client token, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

func TestAccountRoutes_AcceptDelegatedClientWithExactScope(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "delegated@example.com", "securepass", "Alice")
	token, err := app.jwtSvc.SignAccessToken(u.ID(), "sess-delegated", "third-party-client",
		[]string{"account:profile:read"}, "http://localhost", []string{"http://localhost"},
		time.Now().Unix(), 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	resp := app.doWithToken(http.MethodGet, "/v1.0/account/profile", nil, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delegated exact scope: status=%d body=%s", resp.StatusCode, bodyString(resp))
	}
}

func TestAccountRoutes_RequireExactResourceScope(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "missingscope@example.com", "securepass", "Alice")
	token := app.issueTokenWithScopes(t, u.ID(), []string{"openid", "profile", "account:sessions:read"})

	resp := app.doWithToken(http.MethodGet, "/v1.0/account/profile", nil, token)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 without account:profile:read, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	if challenge := resp.Header.Get("WWW-Authenticate"); challenge != `Bearer error="insufficient_scope", scope="account:profile:read"` {
		t.Fatalf("WWW-Authenticate = %q", challenge)
	}
	var problem struct {
		Type          string `json:"type"`
		RequiredScope string `json:"required_scope"`
	}
	readJSON(t, resp, &problem)
	if problem.RequiredScope != "account:profile:read" || problem.Type == "" {
		t.Fatalf("unexpected problem: %+v", problem)
	}
}

func TestEveryAccountOperationIsScopeProtected(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "scope-matrix@example.com", "securepass", "Alice")
	token := app.issueTokenWithScopes(t, u.ID(), []string{"openid", "profile", "email"})

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v1.0/account/profile"},
		{http.MethodPut, "/v1.0/account/profile"},
		{http.MethodPut, "/v1.0/account/password"},
		{http.MethodPost, "/v1.0/account/password"},
		{http.MethodDelete, "/v1.0/account/link/google"},
		{http.MethodGet, "/v1.0/account/sessions"},
		{http.MethodDelete, "/v1.0/account/sessions/example"},
		{http.MethodDelete, "/v1.0/account/sessions"},
		{http.MethodGet, "/v1.0/account/activity"},
		{http.MethodGet, "/v1.0/account/api-keys"},
		{http.MethodPost, "/v1.0/account/api-keys"},
		{http.MethodDelete, "/v1.0/account/api-keys/example"},
		{http.MethodGet, "/v1.0/account/oauth-clients"},
		{http.MethodPost, "/v1.0/account/oauth-clients"},
		{http.MethodPut, "/v1.0/account/oauth-clients/example"},
		{http.MethodDelete, "/v1.0/account/oauth-clients/example"},
		{http.MethodPost, "/v1.0/account/oauth-clients/example/regenerate-secret"},
		{http.MethodGet, "/v1.0/account/consents"},
		{http.MethodDelete, "/v1.0/account/consents/example"},
		{http.MethodGet, "/v1.0/account/mfa/totp"},
		{http.MethodGet, "/v1.0/account/mfa/totp/setup"},
		{http.MethodPost, "/v1.0/account/mfa/totp/confirm"},
		{http.MethodDelete, "/v1.0/account/mfa/totp"},
		{http.MethodPost, "/v1.0/account/mfa/totp/backup-codes"},
		{http.MethodGet, "/v1.0/account/mfa/passkeys"},
		{http.MethodPost, "/v1.0/account/mfa/passkeys/register/begin"},
		{http.MethodPost, "/v1.0/account/mfa/passkeys/register/complete"},
		{http.MethodDelete, "/v1.0/account/mfa/passkeys/example"},
		{http.MethodGet, "/v1.0/account/kyc"},
		{http.MethodPost, "/v1.0/account/kyc/basic"},
		{http.MethodPost, "/v1.0/account/kyc/documents"},
		{http.MethodPost, "/v1.0/account/kyc/documents/confirm"},
		{http.MethodPost, "/v1.0/account/kyc/enhanced"},
		{http.MethodPost, "/v1.0/account/terms/accept"},
	}
	for _, tc := range cases {
		resp := app.doWithToken(tc.method, tc.path, nil, token)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s: status=%d, want 403", tc.method, tc.path, resp.StatusCode)
			continue
		}
		var problem struct {
			Type string `json:"type"`
		}
		readJSON(t, resp, &problem)
		if problem.Type != "https://accounts.aoctech.app/problems/insufficient-scope" {
			t.Errorf("%s %s: problem type=%q", tc.method, tc.path, problem.Type)
		}
	}
}
