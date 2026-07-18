package handler_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	sessionDomain "gopkg.aoctech.app/account/internal/domain/session"
)

// Changing the password must revoke every OTHER session (e.g. a stolen refresh
// token) while keeping the caller's current session alive.
func TestChangePassword_RevokesOtherSessions(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	u := app.registerUser(t, "chpw-revoke@example.com", "pass1234", "Rev")

	current, _, _ := app.sessionSvc.Create(ctx, u.ID(), "Chrome", "1.2.3.4", "UA-current", []string{sessionDomain.AMRPassword})
	_, _, _ = app.sessionSvc.Create(ctx, u.ID(), "Firefox", "5.6.7.8", "UA-other", []string{sessionDomain.AMRPassword})

	token, _ := app.jwtSvc.SignAccessToken(u.ID(), current.ID(), "test-client",
		[]string{"openid"}, "http://localhost", []string{"http://localhost"}, time.Now().Unix(), time.Now().Unix(), []string{sessionDomain.AMRPassword, sessionDomain.AMRTOTP}, "")

	resp := app.doWithToken(http.MethodPut, "/v1.0/account/password", map[string]any{
		"current_password": "pass1234",
		"new_password":     "newpass99",
	}, token)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", resp.StatusCode, bodyString(resp))
	}

	remaining, err := app.sessionSvc.List(ctx, u.ID())
	if err != nil {
		t.Fatalf("listing sessions: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected 1 session (current) remaining, got %d", len(remaining))
	}
	if remaining[0].ID() != current.ID() {
		t.Fatalf("expected current session %s to survive, got %s", current.ID(), remaining[0].ID())
	}
}

func TestGetProfile_Authenticated(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "profile@example.com", "pass1234", "Alice")
	token := app.issueToken(t, u.ID())

	resp := app.doWithToken(http.MethodGet, "/v1.0/account/profile", nil, token)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}

	var body map[string]any
	readJSON(t, resp, &body)
	if body["email"] != "profile@example.com" {
		t.Errorf("wrong email: %v", body["email"])
	}
}

func TestGetProfile_Unauthenticated_401(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodGet, "/v1.0/account/profile", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
	assertProblemJSON(t, resp)
}

func TestUpdateProfile_Success(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "upd@example.com", "pass1234", "Old")
	token := app.issueToken(t, u.ID())

	resp := app.doWithToken(http.MethodPut, "/v1.0/account/profile", map[string]any{
		"first_name": "New",
		"last_name":  "Surname",
	}, token)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}

	var body map[string]any
	readJSON(t, resp, &body)
	if body["first_name"] != "New" {
		t.Errorf("first_name not updated: %v", body["first_name"])
	}
}

func TestUpdateProfile_MissingFirstName_422(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "upd2@example.com", "pass1234", "Name")
	token := app.issueToken(t, u.ID())

	resp := app.doWithToken(http.MethodPut, "/v1.0/account/profile", map[string]any{
		"last_name": "Only",
	}, token)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d", resp.StatusCode)
	}
	assertProblemJSON(t, resp)
}

func TestChangePassword_Success(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "chpw@example.com", "oldpass1", "X")
	token := app.issueToken(t, u.ID())

	resp := app.doWithToken(http.MethodPut, "/v1.0/account/password", map[string]any{
		"current_password": "oldpass1",
		"new_password":     "newpass99",
	}, token)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

func TestChangePassword_WrongCurrent_401(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "chpw2@example.com", "correct", "Y")
	token := app.issueToken(t, u.ID())

	resp := app.doWithToken(http.MethodPut, "/v1.0/account/password", map[string]any{
		"current_password": "wrong",
		"new_password":     "newpass99",
	}, token)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
	assertProblemJSON(t, resp)
}

func TestChangePassword_TooShort_422(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "chpw3@example.com", "pass1234", "Z")
	token := app.issueToken(t, u.ID())

	resp := app.doWithToken(http.MethodPut, "/v1.0/account/password", map[string]any{
		"current_password": "pass1234",
		"new_password":     "short",
	}, token)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d", resp.StatusCode)
	}
}
