package middleware

import (
	"fmt"
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/cache"
	"gopkg.aoctech.app/api-commons/observability"
)

const rateLimitKeyPrefix = "rl:"

const rateLimitUnavailableMsg = "Rate limiting is temporarily unavailable. Please try again later."

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
	FailedLoginMax    = 5
	FailedLoginWindow = 15 * time.Minute
	PerUserMax        = 100
	PerUserWindow     = time.Minute
	// PasskeyBeginMax/Window bound discoverable WebAuthn challenge creation per
	// IP. Conditional UI starts one challenge per login-page load.
	PasskeyBeginMax      = 20
	PasskeyBeginWindow   = time.Minute
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
	// FailClosed makes the limiter deny requests when it cannot enforce the limit
	// (cache disabled, or a Valkey error) instead of silently allowing them. This is the
	// correct posture for an auth service: a missing cache is a fail-open hole that lets
	// brute force run unbounded (SEC-002/SEC-003). Valkey is mandatory in production, so
	// this only triggers on a cache blip — degrading to denial rather than to "allow all".
	FailClosed bool
}

// retryAfter reads the counter's remaining TTL and rounds it up to a whole
// second, so a client is never told to retry before the window has actually
// elapsed. A TTL lookup failure just omits the hint (0) rather than failing
// the request — Retry-After is advisory, not part of the enforcement path.
func retryAfter(c fiber.Ctx, cache *cache.Client, key string) time.Duration {
	ttl, err := cache.TTL(c.Context(), key)
	if err != nil {
		observability.Warn(c.Context(), "rate limit: failed to read retry TTL", err)
		return 0
	}
	if ttl <= 0 {
		return 0
	}
	return ((ttl + time.Second - 1) / time.Second) * time.Second
}

// denyUnavailable rejects the request because the limiter cannot enforce.
// It logs the underlying cause (Valkey error or disabled cache) so a cache
// blip that degrades to 503 is diagnosable instead of silent — see the
// 2026-07-20 prod incident where /account/* 503'd with no logged reason.
func denyUnavailable(c fiber.Ctx, prefix, op string, cause error) error {
	path := c.Path()
	if cause == nil {
		cause = fmt.Errorf("rate limit cache is disabled")
	}
	return apierror.ServiceUnavailable(rateLimitUnavailableMsg, path).WithCause(fmt.Errorf("ratelimit[%s] %s: %w", prefix, op, cause)).Send(c)
}

// RateLimit returns a Fiber middleware enforcing the given RateLimitConfig.
// When FailClosed is false it degrades gracefully (no-op) without Valkey; when
// true it denies requests it cannot protect (SEC-002/SEC-003).
func RateLimit(cfg RateLimitConfig) fiber.Handler {
	return func(c fiber.Ctx) error {
		if cfg.Cache == nil || !cfg.Cache.Enabled() || cfg.KeyFunc == nil {
			if cfg.FailClosed {
				return denyUnavailable(c, cfg.Prefix, "enforce", nil)
			}
			return c.Next()
		}
		id := cfg.KeyFunc(c)
		if id == "" {
			return c.Next()
		}
		key := rateLimitKeyPrefix + cfg.Prefix + ":" + id

		if cfg.CountOnlyFailures {
			// Brute-force guard: count only failed responses. Admit on the
			// pre-check, but treat a cache error or an already-saturated bucket
			// as denial when FailClosed (SEC-003). The window boundary TOCTOU is
			// bounded by the failure-only counting model; the limiter's job is
			// to make unbounded guessing impossible, which FailClosed guarantees.
			n, err := cfg.Cache.Count(c.Context(), key)
			if err != nil && cfg.FailClosed {
				return denyUnavailable(c, cfg.Prefix, "count", err)
			}
			if err != nil {
				observability.Warn(c.Context(), "rate limit: counter read failed open", err, "prefix", cfg.Prefix)
			}
			if err == nil && n >= cfg.Max {
				return apierror.TooManyRequests(rateLimitExceededMsg, c.Path(), retryAfter(c, cfg.Cache, key)).Send(c)
			}

			err = c.Next()
			skip, _ := c.Locals(localsSkipRateLimitCount).(bool)
			if c.Response().StatusCode() >= fiber.StatusBadRequest && !skip {
				if _, incrErr := cfg.Cache.Incr(c.Context(), key, cfg.Window); incrErr != nil {
					observability.Error(c.Context(), "rate limit: failed to count rejected request", incrErr, "prefix", cfg.Prefix)
				}
			}
			return err
		}

		// Throughput guard: count every request atomically and decide on the
		// returned count, eliminating the read-then-write TOCTOU (CON-013).
		n, err := cfg.Cache.Incr(c.Context(), key, cfg.Window)
		if err != nil {
			if cfg.FailClosed {
				return denyUnavailable(c, cfg.Prefix, "incr", err)
			}
			observability.Warn(c.Context(), "rate limit: request counter failed open", err, "prefix", cfg.Prefix)
			return c.Next()
		}
		if n > cfg.Max {
			return apierror.TooManyRequests(rateLimitExceededMsg, c.Path(), retryAfter(c, cfg.Cache, key)).Send(c)
		}
		return c.Next()
	}
}
