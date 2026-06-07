package handler_test

import (
	"net/http"
	"testing"
)

func TestRegister_Created(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodPost, "/v1/auth/register", map[string]any{
		"email":      "new@example.com",
		"password":   "securepass",
		"first_name": "Alice",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

func TestRegister_DuplicateEmail_409(t *testing.T) {
	app := newTestApp(t)
	body := map[string]any{"email": "dup@example.com", "password": "pass1234", "first_name": "A"}
	app.do(http.MethodPost, "/v1/auth/register", body)
	resp := app.do(http.MethodPost, "/v1/auth/register", body)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("expected 409, got %d", resp.StatusCode)
	}
	assertProblemJSON(t, resp)
}

func TestRegister_ValidationError_422(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodPost, "/v1/auth/register", map[string]any{
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
	resp := app.do(http.MethodPost, "/v1/auth/register", nil)
	if resp.StatusCode != http.StatusUnprocessableEntity && resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 4xx, got %d", resp.StatusCode)
	}
}

func TestLogin_Success(t *testing.T) {
	app := newTestApp(t)
	app.registerUser(t, "login@example.com", "password123", "Bob")

	resp := app.do(http.MethodPost, "/v1/auth/login", map[string]any{
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

	resp := app.do(http.MethodPost, "/v1/auth/login", map[string]any{
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
	resp := app.do(http.MethodPost, "/v1/auth/login", map[string]any{
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
	resp := app.do(http.MethodPost, "/v1/auth/logout", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
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
