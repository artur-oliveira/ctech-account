package middleware

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/user"
)

// supportRoleRank orders support roles by privilege. Empty and unknown roles
// deliberately rank below every supported role.
var supportRoleRank = map[string]int{
	user.SupportRoleAgent:   1,
	user.SupportRoleManager: 2,
	user.SupportRoleAdmin:   3,
}

// RequireSupportRole runs after RequireAuth and authorizes support operations
// against the current user record. It intentionally does not use a JWT claim:
// support-role changes take effect immediately without expanding the critical
// token-issuance paths.
func RequireSupportRole(userSvc *user.Service, minRole string) fiber.Handler {
	minRank := supportRoleRank[minRole]
	return func(c fiber.Ctx) error {
		u, err := userSvc.GetByID(c.Context(), GetUserID(c))
		if err != nil {
			if errors.Is(err, user.ErrNotFound) {
				return apierror.Forbidden("Support role required.", c.Path()).Send(c)
			}
			return apierror.ServerError(c.Path()).WithCause(err).Send(c)
		}
		if supportRoleRank[u.SupportRole] < minRank {
			return apierror.Forbidden("Support role required.", c.Path()).Send(c)
		}
		return c.Next()
	}
}
