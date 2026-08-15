package middleware

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/cache"
)

// staticKey returns a fixed identity so every request shares one counter.
func staticKey(_ fiber.Ctx) string { return "tester" }

func newApp(mw fiber.Handler, status int) *fiber.App {
	app := fiber.New()
	app.Post("/x", mw, func(c fiber.Ctx) error {
		return c.SendStatus(status)
	})
	return app
}

func doPost(t *testing.T, app *fiber.App) int {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest("POST", "/x", nil), fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestRateLimit_CountOnlyFailures(t *testing.T) {
	c := cache.NewInMemory()
	mw := RateLimit(RateLimitConfig{
		Cache: c, Prefix: "login", Max: 3, Window: time.Minute,
		KeyFunc: staticKey, CountOnlyFailures: true,
	})
	// Handler returns 401 (a failure), so each call counts.
	app := newApp(mw, fiber.StatusUnauthorized)

	for i := 0; i < 3; i++ {
		if got := doPost(t, app); got != fiber.StatusUnauthorized {
			t.Fatalf("call %d: want 401, got %d", i+1, got)
		}
	}
	// 4th call: counter already at 3 (== Max) → blocked with 429 before handler runs.
	if got := doPost(t, app); got != fiber.StatusTooManyRequests {
		t.Fatalf("4th call: want 429, got %d", got)
	}
}

func TestRateLimit_SuccessesNotCounted(t *testing.T) {
	c := cache.NewInMemory()
	mw := RateLimit(RateLimitConfig{
		Cache: c, Prefix: "login", Max: 2, Window: time.Minute,
		KeyFunc: staticKey, CountOnlyFailures: true,
	})
	app := newApp(mw, fiber.StatusOK) // success — never counts

	for i := 0; i < 10; i++ {
		if got := doPost(t, app); got != fiber.StatusOK {
			t.Fatalf("call %d: want 200, got %d", i+1, got)
		}
	}
}

func TestRateLimit_PerRequestCounting(t *testing.T) {
	c := cache.NewInMemory()
	mw := RateLimit(RateLimitConfig{
		Cache: c, Prefix: "user", Max: 2, Window: time.Minute,
		KeyFunc: staticKey, CountOnlyFailures: false,
	})
	app := newApp(mw, fiber.StatusOK) // even successes count

	if got := doPost(t, app); got != fiber.StatusOK {
		t.Fatalf("call 1: want 200, got %d", got)
	}
	if got := doPost(t, app); got != fiber.StatusOK {
		t.Fatalf("call 2: want 200, got %d", got)
	}
	if got := doPost(t, app); got != fiber.StatusTooManyRequests {
		t.Fatalf("call 3: want 429, got %d", got)
	}
}

// TestRateLimit_SkippedFailuresNotCounted is the regression test for the
// post-logout /token throttling bug: a benign failure (e.g. a refresh token
// that raced a concurrent logout — reachable only by possessing a real, if
// now-stale, credential, never by guessing) must not burn the same
// brute-force budget as an actual guessed credential.
func TestRateLimit_SkippedFailuresNotCounted(t *testing.T) {
	c := cache.NewInMemory()
	mw := RateLimit(RateLimitConfig{
		Cache: c, Prefix: "token", Max: 2, Window: time.Minute,
		KeyFunc: staticKey, CountOnlyFailures: true,
	})
	app := fiber.New()
	app.Post("/x", mw, func(fc fiber.Ctx) error {
		SkipRateLimitCount(fc)
		return fc.SendStatus(fiber.StatusBadRequest)
	})

	// Many more failures than Max — none should count because each opts out.
	for i := 0; i < 10; i++ {
		if got := doPost(t, app); got != fiber.StatusBadRequest {
			t.Fatalf("call %d: want 400, got %d", i+1, got)
		}
	}
}

// TestRateLimit_RetryAfter covers both rejection paths (CountOnlyFailures
// true and false): a 429 must carry a Retry-After header and a matching
// retry_after_seconds body field derived from the counter's remaining TTL.
func TestRateLimit_RetryAfter(t *testing.T) {
	for _, tc := range []struct {
		name              string
		countOnlyFailures bool
		status            int
	}{
		{"count-only-failures", true, fiber.StatusUnauthorized},
		{"count-every-request", false, fiber.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := cache.NewInMemory()
			mw := RateLimit(RateLimitConfig{
				Cache: c, Prefix: "rt", Max: 1, Window: time.Minute,
				KeyFunc: staticKey, CountOnlyFailures: tc.countOnlyFailures,
			})
			app := newApp(mw, tc.status)

			// First request establishes the window (and, for CountOnlyFailures,
			// counts because tc.status is a failure).
			if got := doPost(t, app); got != tc.status {
				t.Fatalf("1st call: want %d, got %d", tc.status, got)
			}

			resp, err := app.Test(httptest.NewRequest("POST", "/x", nil), fiber.TestConfig{Timeout: 5 * time.Second})
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)

			if resp.StatusCode != fiber.StatusTooManyRequests {
				t.Fatalf("2nd call: want 429, got %d (body=%s)", resp.StatusCode, body)
			}

			ra := resp.Header.Get(fiber.HeaderRetryAfter)
			if ra == "" {
				t.Fatal("missing Retry-After header")
			}
			raSeconds, err := strconv.ParseInt(ra, 10, 64)
			if err != nil || raSeconds <= 0 || raSeconds > 60 {
				t.Fatalf("Retry-After=%q not a sane value in (0, 60]: %v", ra, err)
			}

			var problem struct {
				RetryAfterSeconds int64 `json:"retry_after_seconds"`
			}
			if err := json.Unmarshal(body, &problem); err != nil {
				t.Fatalf("unmarshal body: %v", err)
			}
			if problem.RetryAfterSeconds != raSeconds {
				t.Fatalf("body retry_after_seconds=%d must match header %q", problem.RetryAfterSeconds, ra)
			}
		})
	}
}

func TestRateLimit_DisabledCacheIsNoop(t *testing.T) {
	c, _ := cache.New("") // disabled
	mw := RateLimit(RateLimitConfig{
		Cache: c, Prefix: "login", Max: 1, Window: time.Minute,
		KeyFunc: staticKey, CountOnlyFailures: false,
	})
	app := newApp(mw, fiber.StatusOK)

	for i := 0; i < 5; i++ {
		if got := doPost(t, app); got != fiber.StatusOK {
			t.Fatalf("call %d: disabled cache must not limit, got %d", i+1, got)
		}
	}
}
