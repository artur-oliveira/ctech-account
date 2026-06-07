package handler_test

import (
	"net/http"
	"testing"
)

func TestGetProfile_Authenticated(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "profile@example.com", "pass1234", "Alice")
	token := app.issueToken(t, u.ID())

	resp := app.doWithToken(http.MethodGet, "/v1/account/profile", nil, token)
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
	resp := app.do(http.MethodGet, "/v1/account/profile", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
	assertProblemJSON(t, resp)
}

func TestUpdateProfile_Success(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "upd@example.com", "pass1234", "Old")
	token := app.issueToken(t, u.ID())

	resp := app.doWithToken(http.MethodPut, "/v1/account/profile", map[string]any{
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

	resp := app.doWithToken(http.MethodPut, "/v1/account/profile", map[string]any{
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

	resp := app.doWithToken(http.MethodPut, "/v1/account/password", map[string]any{
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

	resp := app.doWithToken(http.MethodPut, "/v1/account/password", map[string]any{
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

	resp := app.doWithToken(http.MethodPut, "/v1/account/password", map[string]any{
		"current_password": "pass1234",
		"new_password":     "short",
	}, token)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d", resp.StatusCode)
	}
}
