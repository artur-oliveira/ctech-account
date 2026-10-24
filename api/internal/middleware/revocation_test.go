package middleware

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/problem"
)

// downBackend is a revocation list that cannot be reached.
type downBackend struct{}

func (downBackend) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, errors.New("valkey down")
}
func (downBackend) Set(context.Context, string, []byte, int) error { return errors.New("valkey down") }
func (downBackend) Delete(context.Context, string) error           { return errors.New("valkey down") }
func (downBackend) DeletePrefix(context.Context, string) error     { return errors.New("valkey down") }
func (downBackend) Ping(context.Context) error                     { return errors.New("valkey down") }

// Revocation list unreachable: reads still authenticate (open), every other
// verb is refused with 503 (closed).
func TestRequireAuth_RevocationDownReadOpenWriteClosed(t *testing.T) {
	jwtSvc := newTestJWT(t)
	jwtSvc.SetRevocation(downBackend{})
	app := fiber.New(fiber.Config{ErrorHandler: func(c fiber.Ctx, err error) error {
		if p, ok := err.(*problem.Problem); ok {
			return p.Send(c)
		}
		return err
	}})
	ok := func(c fiber.Ctx) error { return c.SendString("ok") }
	app.Get("/p", RequireAuth(jwtSvc), ok)
	app.Post("/p", RequireAuth(jwtSvc), ok)

	tok, err := jwtSvc.SignAccessToken("u1", "s1", "web", nil, stepUpTestIssuer, []string{stepUpTestIssuer}, time.Now().Unix()-60, 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for method, want := range map[string]int{"GET": 200, "POST": 503} {
		req := httptest.NewRequest(method, "/p", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("%s: got %d want %d", method, resp.StatusCode, want)
		}
	}
}
