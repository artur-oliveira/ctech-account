package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/cache"
	"gopkg.aoctech.app/account/api/internal/config"
	"gopkg.aoctech.app/account/api/internal/crypto"
	"gopkg.aoctech.app/account/api/internal/domain/audit"
	"gopkg.aoctech.app/account/api/internal/domain/mfa/totp"
	sessionDomain "gopkg.aoctech.app/account/api/internal/domain/session"
	userDomain "gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/handler"
)

// MFA management routes require Bearer auth. The noopTOTPService always returns errors,
// so we test that the handler propagates those errors correctly.

func TestTOTPSetup_Unauthenticated_401(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodGet, "/v1.0/account/mfa/totp/setup", nil)
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

	resp := app.doWithToken(http.MethodGet, "/v1.0/account/mfa/totp/setup", nil, token)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 (noop service), got %d: %s", resp.StatusCode, bodyString(resp))
	}
	assertProblemJSON(t, resp)
}

func TestTOTPConfirm_Unauthenticated_401(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodPost, "/v1.0/account/mfa/totp/confirm", map[string]any{"code": "123456"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
	assertProblemJSON(t, resp)
}

func TestTOTPConfirm_MissingCode_422(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "mfa2@example.com", "pass1234", "MFA")
	token := app.issueToken(t, u.ID())

	resp := app.doWithToken(http.MethodPost, "/v1.0/account/mfa/totp/confirm", map[string]any{}, token)
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

	resp := app.doWithToken(http.MethodPost, "/v1.0/account/mfa/totp/confirm", map[string]any{
		"code": "123456",
	}, token)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 (noop service), got %d", resp.StatusCode)
	}
	assertProblemJSON(t, resp)
}

func TestTOTPRemove_Unauthenticated_401(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodDelete, "/v1.0/account/mfa/totp", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
	assertProblemJSON(t, resp)
}

func TestTOTPRemove_Authenticated_ServiceFails_500(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "mfa4@example.com", "pass1234", "MFA")
	token := app.issueToken(t, u.ID())

	resp := app.doWithToken(http.MethodDelete, "/v1.0/account/mfa/totp", nil, token)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 (noop service), got %d", resp.StatusCode)
	}
	assertProblemJSON(t, resp)
}

func TestTOTPRegenerateBackupCodes_Unauthenticated_401(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodPost, "/v1.0/account/mfa/totp/backup-codes", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
	assertProblemJSON(t, resp)
}

func TestTOTPRegenerateBackupCodes_Authenticated_ServiceFails_500(t *testing.T) {
	app := newTestApp(t)
	u := app.registerUser(t, "mfa5@example.com", "pass1234", "MFA")
	token := app.issueToken(t, u.ID())

	resp := app.doWithToken(http.MethodPost, "/v1.0/account/mfa/totp/backup-codes", nil, token)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 (noop service), got %d", resp.StatusCode)
	}
	assertProblemJSON(t, resp)
}

func TestMFAChallenge_MissingBody_422(t *testing.T) {
	app := newTestApp(t)
	resp := app.do(http.MethodPost, "/v1.0/auth/mfa/challenge", map[string]any{})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d", resp.StatusCode)
	}
	assertProblemJSON(t, resp)
}

func TestMFAChallenge_InvalidToken_401(t *testing.T) {
	// cache is disabled → invalid token returns 401.
	app := newTestApp(t)
	resp := app.do(http.MethodPost, "/v1.0/auth/mfa/challenge", map[string]any{
		"mfa_token": "mfa_invalid",
		"code":      "123456",
	})
	// Cache is disabled → Get returns ErrNotFound → 401 invalid token.
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	assertProblemJSON(t, resp)
}

// codeTOTPService validates only against a fixed correct code, so tests can
// drive both wrong-code and right-code attempts against the same mfa_token.
type codeTOTPService struct{ correctCode string }

func (s *codeTOTPService) Get(_ context.Context, _ string) (*totp.TOTPSecret, error) {
	return nil, errors.New("not used")
}

func (s *codeTOTPService) Validate(_ context.Context, _, code string) (bool, error) {
	return code == s.correctCode, nil
}

// newMFAChallengeApp wires only the /auth/mfa/challenge route against a real
// (in-memory) cache, so mfa_token issue/consume semantics are exercised for
// real instead of the disabledCache used by newTestApp.
func newMFAChallengeApp(t *testing.T) (*fiber.App, *cache.Client, *userDomain.User) {
	t.Helper()

	memCache := cache.NewInMemory()
	userRepo := newMemUserRepo()
	sessionRepo := newMemSessionRepo()
	userSvc := userDomain.NewService(userRepo)
	sessionSvc := sessionDomain.NewService(sessionRepo)
	auditSvc := audit.NewService(&memAuditRepo{})
	cfg := &config.Config{}

	u, err := userSvc.Register(context.Background(), "mfachallenge@example.com", "pass1234", "MFA", "Challenge")
	if err != nil {
		t.Fatalf("registering user: %v", err)
	}

	app := fiber.New(fiber.Config{
		ErrorHandler: func(c fiber.Ctx, err error) error {
			if problem, ok := err.(*apierror.Problem); ok {
				return problem.Send(c)
			}
			return apierror.ServerError(c.Path()).Send(c)
		},
	})
	app.Use(recover.New())

	v1 := app.Group("/v1.0")
	handler.NewAuthHandler(userSvc, sessionSvc, &codeTOTPService{correctCode: "111111"}, nil, memCache, cfg, nil, auditSvc).Register(v1)

	return app, memCache, u
}

// TestMFAChallenge_WrongCodeThenRightCode_TokenSurvives is a regression test:
// a wrong code used to consume the mfa_token (GetDel before validating),
// so the correct code on the very next attempt failed with "invalid-token"
// and the user was stuck. A wrong code must fail with "unauthorized" while
// leaving the token valid for a subsequent correct attempt.
func TestMFAChallenge_WrongCodeThenRightCode_TokenSurvives(t *testing.T) {
	app, memCache, u := newMFAChallengeApp(t)

	rawToken := "test-mfa-token"
	hashHex := crypto.HashToken(rawToken)
	payload := map[string]string{
		"user_id":     u.ID(),
		"device_name": "Test Device",
		"ip":          "127.0.0.1",
		"user_agent":  "test-agent",
		"primary_amr": "pwd",
	}
	if err := memCache.Set(context.Background(), "mfa_token:"+hashHex, payload, 5*time.Minute); err != nil {
		t.Fatalf("seeding mfa_token: %v", err)
	}

	doChallenge := func(code string) *http.Response {
		body, _ := json.Marshal(map[string]any{"mfa_token": rawToken, "code": code})
		req := httptest.NewRequest(http.MethodPost, "/v1.0/auth/mfa/challenge", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := app.Test(req)
		return resp
	}

	wrong := doChallenge("000000")
	if wrong.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong code: expected 401, got %d: %s", wrong.StatusCode, bodyString(wrong))
	}

	right := doChallenge("111111")
	if right.StatusCode != http.StatusOK {
		t.Fatalf("correct code after wrong attempt: expected 200, got %d: %s", right.StatusCode, bodyString(right))
	}
}
