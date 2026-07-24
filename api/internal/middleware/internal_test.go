package middleware

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func TestRequireInternalScope(t *testing.T) {
	jwtSvc := newTestJWT(t)
	app := fiber.New()
	app.Get("/internal", RequireAuth(jwtSvc), RequireInternalScope("internal:account:kyc"), func(c fiber.Ctx) error {
		return c.SendString("ok")
	})

	cases := []struct {
		name      string
		sessionID string
		scopes    []string
		want      int
	}{
		{"client token with scope", "", []string{"internal:account:kyc"}, 200},
		{"user token even with scope", "sess-1", []string{"internal:account:kyc"}, 403},
		{"client token missing scope", "", []string{"openid"}, 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tok, err := jwtSvc.SignAccessToken("wallet", tc.sessionID, "wallet", tc.scopes, stepUpTestIssuer, []string{stepUpTestIssuer}, 0, 0, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("GET", "/internal", nil)
			req.Header.Set("Authorization", "Bearer "+tok)
			resp, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tc.want {
				t.Errorf("got %d want %d", resp.StatusCode, tc.want)
			}
			_ = resp.Body.Close()
		})
	}
}
