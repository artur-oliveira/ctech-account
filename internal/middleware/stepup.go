package middleware

import (
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/internal/apierror"
)

// StepUpMaxAge is the default freshness window for step-up-protected routes.
const StepUpMaxAge = 5 * time.Minute

// RequireRecentMFA rejects requests whose token lacks an MFA proof newer than
// maxAge. Stateless: it reads only JWT claims, so after a successful step-up
// challenge the client must silent-refresh to obtain updated claims.
// Must be registered after RequireAuth.
func RequireRecentMFA(maxAge time.Duration) fiber.Handler {
	return func(c fiber.Ctx) error {
		lastMFA := GetLastMFAAt(c)
		if lastMFA == 0 || time.Since(time.Unix(lastMFA, 0)) > maxAge {
			return apierror.StepUpRequired(maxAge, c.Path()).Send(c)
		}
		return c.Next()
	}
}
