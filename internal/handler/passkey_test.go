package handler_test

import (
	"net/http"
	"testing"
)

// ── GET /v1/account/mfa/passkeys ─────────────────────────────────────────────

func TestPasskeyList_Unauthenticated_401(t *testing.T) {
	ta := newTestApp(t)
	resp := ta.do(http.MethodGet, "/v1/account/mfa/passkeys", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

func TestPasskeyList_Empty_200(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "pk-list@test.com", "Password1!", "Alice")
	tok := ta.issueToken(t, u.ID())

	resp := ta.doWithToken(http.MethodGet, "/v1/account/mfa/passkeys", nil, tok)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
		return
	}

	var body struct {
		Passkeys []any `json:"passkeys"`
	}
	readJSON(t, resp, &body)
	if len(body.Passkeys) != 0 {
		t.Errorf("expected empty passkeys, got %d", len(body.Passkeys))
	}
}

// ── POST /v1/account/mfa/passkeys/register/begin ─────────────────────────────

func TestPasskeyRegisterBegin_MissingName_422(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "pk-begin@test.com", "Password1!", "Bob")
	tok := ta.issueToken(t, u.ID())

	resp := ta.doWithToken(http.MethodPost, "/v1/account/mfa/passkeys/register/begin", map[string]any{}, tok)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

func TestPasskeyRegisterBegin_Valid_200(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "pk-begin2@test.com", "Password1!", "Carol")
	tok := ta.issueToken(t, u.ID())

	resp := ta.doWithToken(http.MethodPost, "/v1/account/mfa/passkeys/register/begin",
		map[string]any{"name": "My YubiKey"}, tok)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
		return
	}

	var body struct {
		SessionToken string `json:"session_token"`
		Options      string `json:"options"`
	}
	readJSON(t, resp, &body)
	if body.SessionToken == "" {
		t.Error("session_token should not be empty")
	}
	if body.Options == "" {
		t.Error("options should not be empty")
	}
}

func TestPasskeyRegisterBegin_Unauthenticated_401(t *testing.T) {
	ta := newTestApp(t)
	resp := ta.do(http.MethodPost, "/v1/account/mfa/passkeys/register/begin",
		map[string]any{"name": "Key"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

// ── POST /v1/account/mfa/passkeys/register/complete ──────────────────────────

func TestPasskeyRegisterComplete_MissingParams_400(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "pk-complete@test.com", "Password1!", "Dave")
	tok := ta.issueToken(t, u.ID())

	// No session_token or name query params.
	resp := ta.doWithToken(http.MethodPost, "/v1/account/mfa/passkeys/register/complete",
		[]byte(`{}`), tok)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

func TestPasskeyRegisterComplete_ExpiredSession_401(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "pk-complete2@test.com", "Password1!", "Eve")
	tok := ta.issueToken(t, u.ID())

	// session_token and name provided, but cache is disabled → session not found → 401
	resp := ta.doWithToken(http.MethodPost,
		"/v1/account/mfa/passkeys/register/complete?session_token=fake-token&name=MyKey",
		[]byte(`{"id":"dGVzdA","rawId":"dGVzdA","response":{},"type":"public-key"}`), tok)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

// ── DELETE /v1/account/mfa/passkeys/:id ──────────────────────────────────────

func TestPasskeyDelete_InvalidHex_400(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "pk-del@test.com", "Password1!", "Frank")
	tok := ta.issueToken(t, u.ID())

	resp := ta.doWithToken(http.MethodDelete, "/v1/account/mfa/passkeys/not-valid-hex", nil, tok)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

func TestPasskeyDelete_NotFound_500(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "pk-del2@test.com", "Password1!", "Grace")
	tok := ta.issueToken(t, u.ID())

	// Valid hex but no such credential exists — handler returns 500 (no ErrNotFound mapping).
	resp := ta.doWithToken(http.MethodDelete,
		"/v1/account/mfa/passkeys/deadbeefcafe0123", nil, tok)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

// ── POST /v1/auth/passkeys/authenticate/begin ─────────────────────────────────

func TestPasskeyAuthBegin_200(t *testing.T) {
	ta := newTestApp(t)

	resp := ta.do(http.MethodPost, "/v1/auth/passkeys/authenticate/begin", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
		return
	}

	var body struct {
		SessionToken string `json:"session_token"`
		Options      string `json:"options"`
	}
	readJSON(t, resp, &body)
	if body.SessionToken == "" {
		t.Error("session_token should not be empty")
	}
	if body.Options == "" {
		t.Error("options should not be empty")
	}
}

// ── POST /v1/auth/passkeys/authenticate/complete ──────────────────────────────

func TestPasskeyAuthComplete_MissingSessionToken_400(t *testing.T) {
	ta := newTestApp(t)

	resp := ta.do(http.MethodPost, "/v1/auth/passkeys/authenticate/complete",
		[]byte(`{"id":"dGVzdA","rawId":"dGVzdA","response":{},"type":"public-key"}`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

func TestPasskeyAuthComplete_ExpiredSession_401(t *testing.T) {
	ta := newTestApp(t)

	resp := ta.do(http.MethodPost,
		"/v1/auth/passkeys/authenticate/complete?session_token=stale-token",
		[]byte(`{"id":"dGVzdA","rawId":"dGVzdA","response":{},"type":"public-key"}`))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}
