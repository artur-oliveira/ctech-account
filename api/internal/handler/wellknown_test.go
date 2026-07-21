package handler_test

import (
	"net/http"
	"testing"
)

func TestOIDCConfiguration(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodGet, "/.well-known/openid-configuration", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}

	var body map[string]any
	readJSON(t, resp, &body)

	requiredFields := []string{
		"issuer", "authorization_endpoint", "token_endpoint",
		"jwks_uri", "userinfo_endpoint", "response_types_supported",
	}
	for _, field := range requiredFields {
		if body[field] == nil {
			t.Errorf("missing field %q in OIDC configuration", field)
		}
	}

	grants, _ := body["grant_types_supported"].([]any)
	got := make(map[string]bool, len(grants))
	for _, g := range grants {
		s, _ := g.(string)
		got[s] = true
	}
	for _, want := range []string{"authorization_code", "refresh_token", "client_credentials", "api_key"} {
		if !got[want] {
			t.Errorf("grant_types_supported missing %q (got %v)", want, grants)
		}
	}
}

func TestJWKS(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodGet, "/.well-known/jwks.json", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}

	var body map[string]any
	readJSON(t, resp, &body)

	keys, ok := body["keys"].([]any)
	if !ok || len(keys) == 0 {
		t.Error("expected at least one key in JWKS response")
	}

	jwk, ok := keys[0].(map[string]any)
	if !ok {
		t.Fatal("key is not a JSON object")
	}
	if jwk["kty"] != "RSA" {
		t.Errorf("expected kty=RSA, got %v", jwk["kty"])
	}
	if jwk["alg"] != "RS256" {
		t.Errorf("expected alg=RS256, got %v", jwk["alg"])
	}
}

func TestUserInfo_Authenticated(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "info@example.com", "pass1234", "Info")
	token := app.issueToken(t, u.ID())

	resp := app.doWithToken(http.MethodGet, "/v1.0/userinfo", nil, token)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}

	var body map[string]any
	readJSON(t, resp, &body)
	if body["email"] != "info@example.com" {
		t.Errorf("wrong email: %v", body["email"])
	}
	if body["sub"] == "" {
		t.Error("missing sub claim")
	}
}

func TestUserInfo_Unauthenticated_401(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodGet, "/v1.0/userinfo", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
	assertProblemJSON(t, resp)
}
