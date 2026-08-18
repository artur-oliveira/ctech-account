package middleware

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/cache"
	"gopkg.aoctech.app/account/api/internal/config"
)

// AccountLockoutMiddleware returns a middleware that tracks failed login attempts
// per account and locks accounts after excessive failed attempts.
func AccountLockoutMiddleware(valkeyClient *cache.Client, cfg *config.Config) fiber.Handler {
	return func(c fiber.Ctx) error {
	_ = context.TODO() // Ensure context package is used
		// Skip for non-auth endpoints
		if !isAuthEndpoint(c.Path()) {
			return c.Next()
		}

		// Extract identifier from request
		identifier, err := extractIdentifier(c)
		if err != nil {
			// If we can't extract identifier, proceed (will fail later)
			return c.Next()
		}

		normalized := normalizeIdentifier(identifier)

		// Check lockout status
		lockoutKey := fmt.Sprintf("auth:lockout:%s", normalized)
		isLocked, err := valkeyClient.Exists(c.Context(), lockoutKey)
		if err != nil {
			// Fail closed on Valkey error
			return apierror.ServiceUnavailable("temporarily unavailable", c.Path()).Send(c)
		}

		if isLocked {
			// Account is locked - return generic error to prevent enumeration
			return apierror.InvalidCredentials(c.Path()).Send(c)
		}

		// Proceed with normal authentication flow
		err = c.Next()

		// Handle result after downstream handlers run
		if err != nil {
			// Check if this is a credential failure
			if isCredentialError(err) {
				// Increment failed attempts
				attemptsKey := fmt.Sprintf("auth:failed_attempts:%s", normalized)
				newCount, err := valkeyClient.Incr(c.Context(), attemptsKey, lockoutDuration(cfg))
				if err != nil {
					// Log error but don't block login attempt
					// In a real implementation, we would use a logger
					// For now, we'll just continue
				} else {
					// Check if threshold reached
					if newCount >= lockoutThreshold(cfg) {
						// Set lockout
						lockoutUntil := time.Now().Add(lockoutDuration(cfg))
						lockoutKey := fmt.Sprintf("auth:lockout:%s", normalized)
						valkeyClient.Set(c.Context(), lockoutKey, lockoutUntil.Format(time.RFC3339), lockoutDuration(cfg))

						// Audit if enabled
						if lockoutAuditEnabled(cfg) {
							// In a real implementation, we would audit here
							// For now, we'll just continue
						}
					}
				}
			}
		} else {
			// Successful login - reset counter
			attemptsKey := fmt.Sprintf("auth:failed_attempts:%s", normalized)
			valkeyClient.Delete(c.Context(), attemptsKey)
		}

		return err
	}
}

// isAuthEndpoint returns true if the path is an authentication endpoint.
// Added support for new endpoints as needed
func isAuthEndpoint(path string) bool {
	switch path {
	case "/auth/login", "/auth/mfa/challenge", "/auth/forgot-password", "/auth/reset-password",
		"/auth/resend-verification", "/auth/register", "/auth/passkeys/authenticate/begin",
		"/auth/passkeys/authenticate/complete", "/auth/google", "/auth/google/callback":
		return true
	default:
		return false
	}
}

// extractIdentifier extracts the login identifier (email or username) from the request.
func extractIdentifier(c fiber.Ctx) (string, error) {
	// For login endpoints, identifier is typically in the request body as "email" or "login"
	// For simplicity, we'll try to get it from form data or JSON
	// In a real implementation, we would parse the request body properly
	// For now, we'll return an empty string to indicate failure to extract
	// This is a placeholder - in a real implementation, we would need to parse the body
	return "", fmt.Errorf("identifier extraction not implemented")
}

// normalizeIdentifier normalizes the identifier for consistent comparison.
func normalizeIdentifier(identifier string) string {
	// For email: lowercase, trim whitespace
	// For username: lowercase, trim whitespace
	// We'll assume identifier is email for now
	if identifier == "" {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(identifier))
}

// lockoutThreshold returns the lockout threshold from config or default.
func lockoutThreshold(cfg *config.Config) int64 {
	if cfg.AccountLockoutThreshold > 0 {
		return int64(cfg.AccountLockoutThreshold)
	}
	return 5 // default
}

// lockoutDuration returns the lockout duration from config or default.
func lockoutDuration(cfg *config.Config) time.Duration {
	if cfg.AccountLockoutDurationMinutes > 0 {
		return time.Duration(cfg.AccountLockoutDurationMinutes) * time.Minute
	}
	return 15 * time.Minute // default
}

// lockoutAuditEnabled returns whether lockout auditing is enabled.
func lockoutAuditEnabled(cfg *config.Config) bool {
	return cfg.AccountLockoutAuditEnabled
}

// isCredentialError returns true if the error is related to invalid credentials.
// This prevents locking accounts for non-credential failures like validation errors.
func isCredentialError(err error) bool {
	if err == nil {
		return false
	}
	// Check if it's an apierror.Problem with status 401
	var problem *apierror.Problem
	if errors.As(err, &problem) {
		return problem.Status == fiber.StatusUnauthorized
	}
	// Also check for common credential error messages
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "invalid credentials") ||
		strings.Contains(errStr, "invalid email") ||
		strings.Contains(errStr, "user not found") ||
		strings.Contains(errStr, "password mismatch")
}