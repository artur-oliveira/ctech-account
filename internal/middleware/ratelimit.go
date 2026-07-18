package middleware

import (
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/internal/apierror"
	"gopkg.aoctech.app/account/internal/cache"
)

const rateLimitKeyPrefix = "rl:"

// localsSkipRateLimitCount is set by a handler to tell RateLimit not to count
// this response as a failure even though its status is >= 400 — used when the
// failure is a benign lifecycle outcome (e.g. a refresh/code exchange that
// raced a concurrent logout) rather than a guessable credential, so it
// shouldn't consume the brute-force budget shared with genuine attack traffic.
const localsSkipRateLimitCount = "skip_rate_limit_count"

// SkipRateLimitCount marks the current response as exempt from the
// CountOnlyFailures counter. Only use this for errors that require possessing
// a real (if now-stale) credential — never for a blindly guessable one (wrong
// password, wrong client_secret, unknown code/token).
func SkipRateLimitCount(c fiber.Ctx) {
	c.Locals(localsSkipRateLimitCount, true)
}

// Rate limiting defaults (see README / PLAN.md).
const (
	FailedLoginMax       = 5
	FailedLoginWindow    = 15 * time.Minute
	PerUserMax           = 100
	PerUserWindow        = time.Minute
	rateLimitExceededMsg = "Too many requests. Please slow down and try again later."
)

// RateLimitConfig configures a rate-limiting middleware backed by a Valkey counter.
type RateLimitConfig struct {
	Cache *cache.Client
	// Prefix namespaces the counter (e.g. "login", "user") so different limiters don't collide.
	Prefix string
	// Max is the number of counted events allowed within Window before requests are rejected.
	Max int64
	// Window is the sliding period the counter lives for.
	Window time.Duration
	// KeyFunc derives the per-caller identity (IP or user id). Returning "" skips limiting.
	KeyFunc func(fiber.Ctx) string
	// CountOnlyFailures counts a request toward the limit only when the handler responds
	// with a 4xx/5xx status (used for brute-force protection on auth endpoints). When false,
	// every request counts (used for per-user throughput limits).
	CountOnlyFailures bool
}

// RateLimit returns a Fiber middleware enforcing the given RateLimitConfig. It is a
// no-op when the cache is nil or disabled, so it degrades gracefully without Valkey.
func RateLimit(cfg RateLimitConfig) fiber.Handler {
	return func(c fiber.Ctx) error {
		if cfg.Cache == nil || !cfg.Cache.Enabled() || cfg.KeyFunc == nil {
			return c.Next()
		}
		id := cfg.KeyFunc(c)
		if id == "" {
			return c.Next()
		}
		key := rateLimitKeyPrefix + cfg.Prefix + ":" + id

		if n, err := cfg.Cache.Count(c.Context(), key); err == nil && n >= cfg.Max {
			return apierror.TooManyRequests(rateLimitExceededMsg, c.Path()).Send(c)
		}

		if !cfg.CountOnlyFailures {
			_, _ = cfg.Cache.Incr(c.Context(), key, cfg.Window)
			return c.Next()
		}

		// Failure mode: run the handler first, then count it only if it failed.
		err := c.Next()
		skip, _ := c.Locals(localsSkipRateLimitCount).(bool)
		if c.Response().StatusCode() >= fiber.StatusBadRequest && !skip {
			_, _ = cfg.Cache.Incr(c.Context(), key, cfg.Window)
		}
		return err
	}
}
