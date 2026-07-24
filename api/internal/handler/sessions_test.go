package handler_test

import (
	"context"
	"net/http"
	"testing"

	sessionDomain "gopkg.aoctech.app/account/api/internal/domain/session"
)

func TestListSessions_ReturnsCurrentSession(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "sess@example.com", "pass1234", "S")
	sess, _, _ := app.sessionSvc.Create(context.Background(), u.ID(), "Chrome", "1.2.3.4", "UA", []string{sessionDomain.AMRPassword})
	token, _ := app.jwtSvc.SignAccessToken(u.ID(), sess.ID(), "test-client", []string{"openid"}, "http://localhost", []string{"http://localhost"}, 0, 0, nil, "")

	resp := app.doWithToken(http.MethodGet, "/v1.0/account/sessions", nil, token)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}

	var body map[string]any
	readJSON(t, resp, &body)
	sessions, ok := body["sessions"].([]any)
	if !ok || len(sessions) == 0 {
		t.Error("expected at least one session in response")
	}
}

func TestRevokeSession_OtherSession(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "sess2@example.com", "pass1234", "T")
	sess1, _, _ := app.sessionSvc.Create(context.Background(), u.ID(), "Chrome", "1.2.3.4", "UA1", []string{sessionDomain.AMRPassword})
	sess2, _, _ := app.sessionSvc.Create(context.Background(), u.ID(), "Firefox", "1.2.3.4", "UA2", []string{sessionDomain.AMRPassword})
	token, _ := app.jwtSvc.SignAccessToken(u.ID(), sess1.ID(), "test-client", []string{"openid"}, "http://localhost", []string{"http://localhost"}, 0, 0, nil, "")

	resp := app.doWithToken(http.MethodDelete, "/v1.0/account/sessions/"+sess2.ID(), nil, token)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

func TestRevokeSession_CurrentSession_400(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "sess3@example.com", "pass1234", "U")
	sess, _, _ := app.sessionSvc.Create(context.Background(), u.ID(), "Chrome", "1.2.3.4", "UA", []string{sessionDomain.AMRPassword})
	token, _ := app.jwtSvc.SignAccessToken(u.ID(), sess.ID(), "test-client", []string{"openid"}, "http://localhost", []string{"http://localhost"}, 0, 0, nil, "")

	// Revoking the current session via DELETE /sessions/:id should be rejected.
	resp := app.doWithToken(http.MethodDelete, "/v1.0/account/sessions/"+sess.ID(), nil, token)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	assertProblemJSON(t, resp)
}

func TestRevokeAllSessions(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "sess4@example.com", "pass1234", "V")
	sess1, _, _ := app.sessionSvc.Create(context.Background(), u.ID(), "Chrome", "1.2.3.4", "UA", []string{sessionDomain.AMRPassword})
	_, _, _ = app.sessionSvc.Create(context.Background(), u.ID(), "Firefox", "1.2.3.5", "UA2", []string{sessionDomain.AMRPassword})
	token, _ := app.jwtSvc.SignAccessToken(u.ID(), sess1.ID(), "test-client", []string{"openid"}, "http://localhost", []string{"http://localhost"}, 0, 0, nil, "")

	resp := app.doWithToken(http.MethodDelete, "/v1.0/account/sessions", nil, token)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d: %s", resp.StatusCode, bodyString(resp))
	}

	// Only the current session should remain.
	sessions, _ := app.sessionSvc.List(context.Background(), u.ID())
	if len(sessions) != 1 {
		t.Errorf("expected 1 session remaining, got %d", len(sessions))
	}
}
