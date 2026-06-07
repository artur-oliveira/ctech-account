package handler_test

import (
	"net/http"
	"testing"
)

// MFA management routes require Bearer auth. The noopTOTPService always returns errors,
// so we test that the handler propagates those errors correctly.

func TestTOTPSetup_Unauthenticated_401(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodGet, "/v1/account/mfa/totp/setup", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
	assertProblemJSON(t, resp)
}

func TestTOTPSetup_AuthenticatedButServiceFails_500(t *testing.T) {
	// noopTOTPService.Generate returns an error → expect 500 from the server error handler.
	app := newTestApp(t)
	u := app.registerUser(t, "mfa1@example.com", "pass1234", "MFA")
	token := app.issueToken(t, u.ID())

	resp := app.doWithToken(http.MethodGet, "/v1/account/mfa/totp/setup", nil, token)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 (noop service), got %d: %s", resp.StatusCode, bodyString(resp))
	}
	assertProblemJSON(t, resp)
}

func TestTOTPConfirm_Unauthenticated_401(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodPost, "/v1/account/mfa/totp/confirm", map[string]any{"code": "123456"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
	assertProblemJSON(t, resp)
}

func TestTOTPConfirm_MissingCode_422(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "mfa2@example.com", "pass1234", "MFA")
	token := app.issueToken(t, u.ID())

	resp := app.doWithToken(http.MethodPost, "/v1/account/mfa/totp/confirm", map[string]any{}, token)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d", resp.StatusCode)
	}
	assertProblemJSON(t, resp)
}

func TestTOTPConfirm_InvalidCode_ServiceFails(t *testing.T) {
	// noopTOTPService.Verify returns an error → 500.
	app := newTestApp(t)
	u := app.registerUser(t, "mfa3@example.com", "pass1234", "MFA")
	token := app.issueToken(t, u.ID())

	resp := app.doWithToken(http.MethodPost, "/v1/account/mfa/totp/confirm", map[string]any{
		"code": "123456",
	}, token)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 (noop service), got %d", resp.StatusCode)
	}
	assertProblemJSON(t, resp)
}

func TestTOTPRemove_Unauthenticated_401(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodDelete, "/v1/account/mfa/totp", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
	assertProblemJSON(t, resp)
}

func TestTOTPRemove_Authenticated_ServiceFails_500(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "mfa4@example.com", "pass1234", "MFA")
	token := app.issueToken(t, u.ID())

	resp := app.doWithToken(http.MethodDelete, "/v1/account/mfa/totp", nil, token)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 (noop service), got %d", resp.StatusCode)
	}
	assertProblemJSON(t, resp)
}

func TestTOTPRegenerateBackupCodes_Unauthenticated_401(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodPost, "/v1/account/mfa/totp/backup-codes", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
	assertProblemJSON(t, resp)
}

func TestTOTPRegenerateBackupCodes_Authenticated_ServiceFails_500(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "mfa5@example.com", "pass1234", "MFA")
	token := app.issueToken(t, u.ID())

	resp := app.doWithToken(http.MethodPost, "/v1/account/mfa/totp/backup-codes", nil, token)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 (noop service), got %d", resp.StatusCode)
	}
	assertProblemJSON(t, resp)
}

func TestMFAChallenge_MissingBody_422(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodPost, "/v1/auth/mfa/challenge", map[string]any{})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d", resp.StatusCode)
	}
	assertProblemJSON(t, resp)
}

func TestMFAChallenge_InvalidToken_401(t *testing.T) {
	// cache is disabled → invalid token returns 401.
	app := newTestApp(t)
	resp := app.do(http.MethodPost, "/v1/auth/mfa/challenge", map[string]any{
		"mfa_token": "mfa_invalid",
		"code":      "123456",
	})
	// Cache is disabled → Get returns ErrNotFound → 401 invalid token.
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	assertProblemJSON(t, resp)
}
