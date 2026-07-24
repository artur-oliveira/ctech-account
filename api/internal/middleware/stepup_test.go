package middleware

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/config"
	"gopkg.aoctech.app/account/api/internal/crypto"
)

const stepUpTestIssuer = "http://localhost"

func newTestJWT(t *testing.T) *crypto.JWTService {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := crypto.NewJWTService(&config.Config{
		RSAPrivateKey: key,
		PublicKeyKID:  "test-kid",
		Audience:      stepUpTestIssuer,
		BaseURL:       stepUpTestIssuer,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestRequireRecentMFA(t *testing.T) {
	jwtSvc := newTestJWT(t)
	app := fiber.New()
	app.Get("/p", RequireAuth(jwtSvc), RequireRecentMFA(5*time.Minute), func(c fiber.Ctx) error {
		return c.SendString("ok")
	})

	now := time.Now().Unix()
	cases := []struct {
		name      string
		lastMFAAt int64
		amr       []string
		want      int
	}{
		{"fresh mfa", now - 60, []string{"pwd", "otp"}, 200},
		{"stale mfa", now - 3600, []string{"pwd", "otp"}, 403},
		{"never mfa", 0, []string{"pwd"}, 403},
		{"api key token (no claims)", 0, nil, 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tok, err := jwtSvc.SignAccessToken("u1", "s1", "web", nil, stepUpTestIssuer, []string{stepUpTestIssuer}, now-7200, tc.lastMFAAt, tc.amr, "")
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("GET", "/p", nil)
			req.Header.Set("Authorization", "Bearer "+tok)
			resp, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tc.want {
				t.Errorf("got %d want %d", resp.StatusCode, tc.want)
			}
			if tc.want == 403 {
				body, _ := io.ReadAll(resp.Body)
				var problem struct {
					Type          string `json:"type"`
					MaxAgeSeconds int64  `json:"max_age_seconds"`
				}
				if err := json.Unmarshal(body, &problem); err != nil {
					t.Fatalf("decoding problem: %v", err)
				}
				if problem.MaxAgeSeconds != 300 {
					t.Errorf("max_age_seconds = %d, want 300", problem.MaxAgeSeconds)
				}
				if len(problem.Type) < len("step-up-required") || problem.Type[len(problem.Type)-len("step-up-required"):] != "step-up-required" {
					t.Errorf("problem type = %q, want suffix step-up-required", problem.Type)
				}
			}
			_ = resp.Body.Close()
		})
	}
}
