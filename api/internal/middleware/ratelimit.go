package middleware

import (
	"context"
	"errors"
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/cache"
	"gopkg.aoctech.app/api-commons/ratelimit"
	"gopkg.aoctech.app/api-commons/ratelimit/fiberlimit"
)

const rateLimitUnavailableMsg = "Rate limiting is temporarily unavailable. Please try again later."

const rateLimitExceededMsg = "Too many requests. Please slow down and try again later."

// SkipRateLimitCount marks the current response as exempt from the
// CountOnlyFailures counter. Only use this for errors that require possessing
// a real (if now-stale) credential (a refresh/code exchange that raced a
// concurrent logout), never for a blindly guessable one (wrong password, wrong
// client_secret, unknown code/token).
func SkipRateLimitCount(c fiber.Ctx) { fiberlimit.SkipCount(c) }

// Rate limiting defaults (see README / PLAN.md).
const (
	FailedLoginMax    = 5
	FailedLoginWindow = 15 * time.Minute
	PerUserMax        = 100
	PerUserWindow     = time.Minute
	// PasskeyBeginMax/Window bound discoverable WebAuthn challenge creation per
	// IP. Conditional UI starts one challenge per login-page load.
	PasskeyBeginMax    = 20
	PasskeyBeginWindow = time.Minute
)

// RateLimitConfig configures a go-common ratelimit policy backed by the Valkey counter.
type RateLimitConfig struct {
	Cache *cache.Client
	// Prefix namespaces the counter (e.g. "login", "user"); the key is rl:<Prefix>:<id>.
	Prefix string
	// Max is the number of counted events allowed within Window before requests are rejected.
	Max int64
	// Window is the period the counter lives for.
	Window time.Duration
	// KeyFunc derives the per-caller identity (IP or user id). Returning "" skips limiting.
	KeyFunc func(fiber.Ctx) string
	// CountOnlyFailures counts a request only when it fails (brute-force guard).
	CountOnlyFailures bool
	// CountOnlySuccesses counts a request only when it succeeds (a budget for an
	// action that typos and rejected attempts must not consume).
	CountOnlySuccesses bool
	// FailClosed denies requests the limiter cannot enforce (cache disabled or a
	// Valkey error) instead of allowing them: the posture for every auth route.
	FailClosed bool
}

var errCacheDisabled = errors.New("rate limit cache is disabled")

// counter adapts the account cache to ratelimit.TTLCounter. A disabled cache is
// an error, so a fail-closed limiter denies instead of silently counting nothing.
type counter struct{ c *cache.Client }

func (k counter) enabled() bool { return k.c != nil && k.c.Enabled() }

func (k counter) Incr(ctx context.Context, key string, w time.Duration) (int64, error) {
	if !k.enabled() {
		return 0, errCacheDisabled
	}
	return k.c.Incr(ctx, key, w)
}

func (k counter) Count(ctx context.Context, key string) (int64, error) {
	if !k.enabled() {
		return 0, errCacheDisabled
	}
	return k.c.Count(ctx, key)
}

func (k counter) TTL(ctx context.Context, key string) (time.Duration, error) {
	return k.c.TTL(ctx, key)
}

// RateLimit returns the go-common policy middleware for cfg. When FailClosed is
// false and the cache is disabled it is a no-op; when true it answers 503.
func RateLimit(cfg RateLimitConfig) fiber.Handler {
	k := counter{cfg.Cache}
	if !k.enabled() && !cfg.FailClosed {
		return func(c fiber.Ctx) error { return c.Next() }
	}
	mode := ratelimit.FailOpen
	if cfg.FailClosed {
		mode = ratelimit.FailClosed
	}
	count := fiberlimit.CountAll
	switch {
	case cfg.CountOnlyFailures:
		count = fiberlimit.CountFailures
	case cfg.CountOnlySuccesses:
		count = fiberlimit.CountSuccesses
	}
	return fiberlimit.NewPolicy(fiberlimit.PolicyConfig{
		Limiter: &ratelimit.Limiter{Counter: k, Prefix: cfg.Prefix, Max: cfg.Max, Window: cfg.Window, FailMode: mode},
		Key:     cfg.KeyFunc,
		Count:   count,
		Deny: func(c fiber.Ctx, secs int) error {
			return apierror.TooManyRequests(rateLimitExceededMsg, c.Path(), time.Duration(secs)*time.Second).Send(c)
		},
		Unavailable: func(c fiber.Ctx) error {
			return apierror.ServiceUnavailable(rateLimitUnavailableMsg, c.Path()).Send(c)
		},
	})
}

// DeletionRequestLimiters guard POST /v1.0/account/deletion, per user:
//   - a budget of 3 accepted requests per 30 days (typos and rejected attempts
//     do not consume it, so a user who lost the e-mail can always ask again);
//   - a password brute-force guard on the identity check (failures only), so
//     the endpoint is no password oracle for a stolen access token.
func DeletionRequestLimiters(c *cache.Client) []fiber.Handler {
	return []fiber.Handler{
		RateLimit(RateLimitConfig{
			Cache: c, Prefix: "deletion_req", Max: 3, Window: 30 * 24 * time.Hour,
			KeyFunc: GetUserID, CountOnlySuccesses: true, FailClosed: true,
		}),
		RateLimit(RateLimitConfig{
			Cache: c, Prefix: "deletion_pw", Max: FailedLoginMax, Window: FailedLoginWindow,
			KeyFunc: GetUserID, CountOnlyFailures: true, FailClosed: true,
		}),
	}
}

// DataExportLimiters guards the identity check of the export request against
// password guessing (failures only). The one-export-per-24h rule lives in the
// service, where a failed build can be retried at once.
func DataExportLimiters(c *cache.Client) []fiber.Handler {
	return []fiber.Handler{
		RateLimit(RateLimitConfig{
			Cache: c, Prefix: "export_pw", Max: FailedLoginMax, Window: FailedLoginWindow,
			KeyFunc: GetUserID, CountOnlyFailures: true, FailClosed: true,
		}),
	}
}
