package handler_test

import (
	"net/http"
	"testing"
)

func TestCreateAPIKey_Success(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "apikey@example.com", "pass1234", "K")
	token := app.issueToken(t, u.ID())

	resp := app.doWithToken(http.MethodPost, "/v1.0/account/api-keys", map[string]any{
		"name":   "My Key",
		"scopes": []string{"read"},
	}, token)
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", resp.StatusCode, bodyString(resp))
	}

	var body map[string]any
	readJSON(t, resp, &body)
	if body["raw_key"] == "" {
		t.Error("expected raw_key in response")
	}
	if body["key_id"] == "" {
		t.Error("expected key_id in response")
	}
}

func TestCreateAPIKey_MissingName_422(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "apikey2@example.com", "pass1234", "L")
	token := app.issueToken(t, u.ID())

	resp := app.doWithToken(http.MethodPost, "/v1.0/account/api-keys", map[string]any{
		"scopes": []string{"read"},
	}, token)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d", resp.StatusCode)
	}
}

func TestListAPIKeys_Empty(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "apikey3@example.com", "pass1234", "M")
	token := app.issueToken(t, u.ID())

	resp := app.doWithToken(http.MethodGet, "/v1.0/account/api-keys", nil, token)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	readJSON(t, resp, &body)
	keys, ok := body["api_keys"].([]any)
	if !ok {
		t.Error("expected api_keys array in response")
	}
	if len(keys) != 0 {
		t.Errorf("expected 0 keys, got %d", len(keys))
	}
}

func TestRevokeAPIKey_Success(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "apikey4@example.com", "pass1234", "N")
	token := app.issueToken(t, u.ID())

	// Create a key.
	createResp := app.doWithToken(http.MethodPost, "/v1.0/account/api-keys", map[string]any{
		"name": "ToRevoke",
	}, token)
	var createBody map[string]any
	readJSON(t, createResp, &createBody)
	keyID, _ := createBody["key_id"].(string)

	// Revoke it.
	resp := app.doWithToken(http.MethodDelete, "/v1.0/account/api-keys/"+keyID, nil, token)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

func TestCreateAPIKey_Unauthenticated_401(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodPost, "/v1.0/account/api-keys", map[string]any{"name": "k"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}
