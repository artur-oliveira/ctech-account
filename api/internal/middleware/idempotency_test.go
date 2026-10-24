package middleware

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/api-commons/idempotency"
)

func idemApp(calls *int) *fiber.App {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error { c.Locals(LocalUserID, c.Get("X-User")); return c.Next() })
	app.Post("/x", Idempotency(&idempotency.MemoryStore{}, nil), func(c fiber.Ctx) error {
		*calls++
		return c.Status(201).SendString("created")
	})
	return app
}

func idemPost(t *testing.T, app *fiber.App, user, key, body string) (int, string, string) {
	t.Helper()
	req := httptest.NewRequest("POST", "/x", strings.NewReader(body))
	req.Header.Set("X-User", user)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header.Get("Idempotent-Replay")
}

func TestIdempotency(t *testing.T) {
	calls := 0
	app := idemApp(&calls)

	if c, _, _ := idemPost(t, app, "u1", "", "a"); c != 201 || calls != 1 {
		t.Fatalf("no key must run the handler (optional): %d calls=%d", c, calls)
	}
	if c, _, r := idemPost(t, app, "u1", "k1", "a"); c != 201 || r != "" || calls != 2 {
		t.Fatalf("first keyed call runs: %d replay=%q calls=%d", c, r, calls)
	}
	if c, b, r := idemPost(t, app, "u1", "k1", "a"); c != 201 || b != "created" || r != "true" || calls != 2 {
		t.Fatalf("same key+body replays: %d %q replay=%q calls=%d", c, b, r, calls)
	}
	if c, _, _ := idemPost(t, app, "u1", "k1", "different"); c != 409 {
		t.Fatalf("same key, other body: want 409, got %d", c)
	}
	if c, _, r := idemPost(t, app, "u2", "k1", "a"); c != 201 || r != "" || calls != 3 {
		t.Fatalf("another user's key is a separate namespace: %d replay=%q calls=%d", c, r, calls)
	}
}
