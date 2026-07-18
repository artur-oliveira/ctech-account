package handler_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/account/internal/domain/mfa/totp"
	sessionDomain "gopkg.aoctech.app/account/internal/domain/session"
)

// stubTOTPService accepts exactly one code and reports TOTP as enrolled.
type stubTOTPService struct {
	validCode string
}

func (s *stubTOTPService) Get(_ context.Context, _ string) (*totp.TOTPSecret, error) {
	return &totp.TOTPSecret{Verified: true}, nil
}

func (s *stubTOTPService) Validate(_ context.Context, _, code string) (bool, error) {
	return code == s.validCode, nil
}

func (s *stubTOTPService) Generate(_ context.Context, _, _, _ string) (*totp.TOTPSecret, string, error) {
	return nil, "", errors.New("not implemented")
}
func (s *stubTOTPService) Verify(_ context.Context, _, _ string) ([]string, error) {
	return nil, errors.New("not implemented")
}
func (s *stubTOTPService) Remove(_ context.Context, _ string) error {
	return errors.New("not implemented")
}
func (s *stubTOTPService) RegenerateBackupCodes(_ context.Context, _ string) ([]string, error) {
	return nil, errors.New("not implemented")
}

// stepUpToken mints a token bound to a real session so RecordMFA can find it.
func stepUpToken(t *testing.T, ta *testApp, userID string, lastMFAAt int64) (sessionID, token string) {
	t.Helper()
	sess, _, err := ta.sessionSvc.Create(context.Background(), userID, "Chrome", "1.2.3.4", "UA", []string{sessionDomain.AMRPassword})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := ta.jwtSvc.SignAccessToken(userID, sess.ID(), "test-client", []string{"openid"},
		"http://localhost", []string{"http://localhost"}, sess.AuthTime, lastMFAAt, sess.AMR, "")
	if err != nil {
		t.Fatal(err)
	}
	return sess.ID(), tok
}

func TestStepUpTOTPSuccess(t *testing.T) {
	ta := newTestAppWithTOTP(t, &stubTOTPService{validCode: "123456"})
	u := ta.registerUser(t, "stepup@example.com", "Str0ngP4ss!word", "Step")
	sessionID, token := stepUpToken(t, ta, u.ID(), 0)

	resp := ta.doWithToken(http.MethodPost, "/v1.0/auth/step-up", map[string]string{
		"method": "totp", "code": "123456",
	}, token)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("got %d: %s", resp.StatusCode, bodyString(resp))
	}

	sess, err := ta.sessionSvc.Get(context.Background(), u.ID(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if sess.LastMFAAt == 0 {
		t.Error("RecordMFA not applied to session")
	}
	hasOTP := false
	for _, m := range sess.AMR {
		if m == sessionDomain.AMRTOTP {
			hasOTP = true
		}
	}
	if !hasOTP {
		t.Errorf("AMR missing otp: %v", sess.AMR)
	}
}

func TestStepUpWrongCodeIs401(t *testing.T) {
	ta := newTestAppWithTOTP(t, &stubTOTPService{validCode: "123456"})
	u := ta.registerUser(t, "stepup2@example.com", "Str0ngP4ss!word", "Step")
	_, token := stepUpToken(t, ta, u.ID(), 0)

	resp := ta.doWithToken(http.MethodPost, "/v1.0/auth/step-up", map[string]string{
		"method": "totp", "code": "000000",
	}, token)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got %d", resp.StatusCode)
	}
	if body := bodyString(resp); !strings.Contains(body, "invalid-credentials") {
		t.Errorf("expected invalid-credentials problem, got %s", body)
	}
}

func TestStepUpWithoutEnrollmentIs403Enrollment(t *testing.T) {
	// default test app uses noopTOTPService (no TOTP) and no passkeys.
	ta := newTestApp(t)
	u := ta.registerUser(t, "stepup3@example.com", "Str0ngP4ss!word", "Step")
	_, token := stepUpToken(t, ta, u.ID(), 0)

	resp := ta.doWithToken(http.MethodPost, "/v1.0/auth/step-up", map[string]string{
		"method": "totp", "code": "123456",
	}, token)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("got %d", resp.StatusCode)
	}
	if body := bodyString(resp); !strings.Contains(body, "mfa-enrollment-required") {
		t.Errorf("expected mfa-enrollment-required problem, got %s", body)
	}
}

func TestStepUpRequiresAuth(t *testing.T) {
	ta := newTestApp(t)
	resp := ta.do(http.MethodPost, "/v1.0/auth/step-up", map[string]string{
		"method": "totp", "code": "123456",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got %d", resp.StatusCode)
	}
}

func TestSensitiveRouteRejectsStaleMFA(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "stepup4@example.com", "Str0ngP4ss!word", "Step")
	_, token := stepUpToken(t, ta, u.ID(), 0) // no MFA proof at all

	resp := ta.doWithToken(http.MethodDelete, "/v1.0/account/mfa/totp", nil, token)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("got %d", resp.StatusCode)
	}
	if body := bodyString(resp); !strings.Contains(body, "step-up-required") {
		t.Errorf("expected step-up-required problem, got %s", body)
	}
}

func TestSensitiveRouteAllowsFreshMFA(t *testing.T) {
	ta := newTestApp(t)
	u := ta.registerUser(t, "stepup5@example.com", "Str0ngP4ss!word", "Step")
	_, token := stepUpToken(t, ta, u.ID(), time.Now().Unix())

	resp := ta.doWithToken(http.MethodDelete, "/v1.0/account/mfa/totp", nil, token)
	// noopTOTPService errors → 500 from the domain; auth/step-up gates passed.
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("step-up gate wrongly blocked fresh-MFA token: %d", resp.StatusCode)
	}
}
