package handler_test

import (
	"net/http"
	"testing"
)

// createTestClient posts a valid confidential client and returns the response body.
func createTestClient(t *testing.T, ta *testApp, token string) map[string]any {
	t.Helper()
	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/oauth-clients", map[string]any{
		"name":           "My App",
		"client_type":    "confidential",
		"redirect_uris":  []string{"https://app.example.com/callback"},
		"allowed_scopes": []string{"openid", "profile", "email", "dfe:nfes:read"},
	}, token)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create client: expected 201, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var body map[string]any
	readJSON(t, resp, &body)
	return body
}

func TestOAuthClients_CreateAndList(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "owner@example.com", "password123", "Owner")
	token := ta.issueToken(t, u.ID())

	created := createTestClient(t, ta, token)
	if created["client_id"] == "" {
		t.Fatal("expected client_id in response")
	}
	secret, _ := created["client_secret"].(string)
	if secret == "" {
		t.Fatal("expected one-time client_secret for confidential client")
	}

	resp := ta.doWithToken(http.MethodGet, "/v1.0/account/oauth-clients", nil, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", resp.StatusCode)
	}
	var list struct {
		OAuthClients []map[string]any `json:"oauth_clients"`
	}
	readJSON(t, resp, &list)
	if len(list.OAuthClients) != 1 {
		t.Fatalf("expected 1 client, got %d", len(list.OAuthClients))
	}
	if _, exposed := list.OAuthClients[0]["client_secret"]; exposed {
		t.Fatal("client_secret must never appear in list responses")
	}
	if _, exposed := list.OAuthClients[0]["client_secret_hash"]; exposed {
		t.Fatal("client_secret_hash must never appear in responses")
	}
}

func TestOAuthClients_RejectsInvalidScopeAndRedirect(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "owner2@example.com", "password123", "Owner")
	token := ta.issueToken(t, u.ID())

	// Malformed scope.
	resp := ta.doWithToken(http.MethodPost, "/v1.0/account/oauth-clients", map[string]any{
		"name":           "Bad Scope",
		"client_type":    "public",
		"redirect_uris":  []string{"https://app.example.com/cb"},
		"allowed_scopes": []string{"Not A Scope!"},
	}, token)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid scope: expected 400, got %d: %s", resp.StatusCode, bodyString(resp))
	}

	// http redirect URI on a non-localhost host.
	resp = ta.doWithToken(http.MethodPost, "/v1.0/account/oauth-clients", map[string]any{
		"name":           "Bad URI",
		"client_type":    "public",
		"redirect_uris":  []string{"http://app.example.com/cb"},
		"allowed_scopes": []string{"openid"},
	}, token)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("http redirect: expected 400, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

func TestOAuthClients_OwnershipEnforced(t *testing.T) {
	ta := newTestApp(t)
	owner := ta.registerUser(t, "owner3@example.com", "password123", "Owner")
	other := ta.registerUser(t, "other@example.com", "password123", "Other")

	created := createTestClient(t, ta, ta.issueToken(t, owner.ID()))
	clientID, _ := created["client_id"].(string)

	otherToken := ta.issueToken(t, other.ID())
	resp := ta.doWithToken(http.MethodDelete, "/v1.0/account/oauth-clients/"+clientID, nil, otherToken)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign delete: expected 403, got %d", resp.StatusCode)
	}
	resp = ta.doWithToken(http.MethodPost, "/v1.0/account/oauth-clients/"+clientID+"/regenerate-secret", nil, otherToken)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign regenerate: expected 403, got %d", resp.StatusCode)
	}
}

func TestOAuthClients_UpdateAndRegenerate(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "owner4@example.com", "password123", "Owner")
	token := ta.issueToken(t, u.ID())
	created := createTestClient(t, ta, token)
	clientID, _ := created["client_id"].(string)

	resp := ta.doWithToken(http.MethodPut, "/v1.0/account/oauth-clients/"+clientID, map[string]any{
		"name":           "Renamed",
		"redirect_uris":  []string{"https://new.example.com/cb"},
		"allowed_scopes": []string{"openid", "dfe:nfes:write"},
	}, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update: expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var updated map[string]any
	readJSON(t, resp, &updated)
	if updated["name"] != "Renamed" {
		t.Fatalf("expected renamed client, got %v", updated["name"])
	}

	resp = ta.doWithToken(http.MethodPost, "/v1.0/account/oauth-clients/"+clientID+"/regenerate-secret", nil, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("regenerate: expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var regen map[string]any
	readJSON(t, resp, &regen)
	if s, _ := regen["client_secret"].(string); s == "" {
		t.Fatal("expected new client_secret")
	}

	resp = ta.doWithToken(http.MethodDelete, "/v1.0/account/oauth-clients/"+clientID, nil, token)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d", resp.StatusCode)
	}
}
