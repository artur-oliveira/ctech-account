package handler_test

import (
	"net/http"
	"testing"
	"time"
)

// TestAccountRoutes_RejectOtherClientToken is the regression test for the
// account-takeover bug: RequireAuth alone (valid signature + audience + issuer)
// is not enough to gate /v1.0/account/* and /v1.0/step-up/* — these are
// self-service endpoints with no scope of their own, so a downstream client's
// user-flow access token (e.g. dfe's, whose audience must include this
// service's own audience for GET /v1.0/userinfo to work) must still be
// rejected here. Only this service's own frontend (azp == cfg.SelfClientID)
// may call them.
func TestAccountRoutes_RejectOtherClientToken(t *testing.T) {
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

// TestAccountRoutes_AcceptSelfClientToken verifies the lock doesn't lock out
// this service's own frontend.
func TestAccountRoutes_AcceptSelfClientToken(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "selfclient@example.com", "securepass", "Alice")

	token := app.issueToken(t, u.ID())

	resp := app.doWithToken(http.MethodGet, "/v1.0/account/profile", nil, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for this service's own client token, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}
