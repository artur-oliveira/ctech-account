package middleware

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/organization"
)

const (
	LocalOrgID   = "organization_id"
	LocalOrgRole = "organization_role"
)

// RequireOrgRole runs after RequireAuth and authorizes an organization-scoped
// route against the caller's membership row.
//
// It reads the role on every request, never from a token claim, for the same
// reason RequireSupportRole does: a role minted into a JWT outlives its own
// revocation for the token's lifetime, so removing somebody from an
// organization would not take effect until their session expired.
func RequireOrgRole(svc *organization.Service, minRole string) fiber.Handler {
	return func(c fiber.Ctx) error {
		orgID := c.Params("id")
		if orgID == "" {
			return apierror.Forbidden(orgAccessDenied, c.Path()).Send(c)
		}
		role, err := svc.RoleOf(c.Context(), orgID, GetUserID(c))
		if err != nil {
			// A non-member and an organization that does not exist get the
			// same answer. Distinguishing them turns this route into a probe
			// for which organization ids are real.
			if errors.Is(err, organization.ErrNotAMember) || errors.Is(err, organization.ErrNotFound) {
				return apierror.Forbidden(orgAccessDenied, c.Path()).Send(c)
			}
			return apierror.ServerError(c.Path()).WithCause(err).Send(c)
		}
		if !organization.AtLeast(role, minRole) {
			return apierror.Forbidden(orgAccessDenied, c.Path()).Send(c)
		}
		// Published so the handler acts on the role this middleware already
		// resolved, instead of reading the membership a second time and
		// possibly disagreeing with the check that let it through.
		c.Locals(LocalOrgID, orgID)
		c.Locals(LocalOrgRole, role)
		return c.Next()
	}
}

// orgAccessDenied is one message for every refusal on these routes, so the
// wording cannot leak which of them happened.
const orgAccessDenied = "You do not have access to this organization."

// GetOrgID returns the organization the current request is scoped to.
func GetOrgID(c fiber.Ctx) string {
	v, _ := c.Locals(LocalOrgID).(string)
	return v
}

// GetOrgRole returns the caller's role in that organization.
func GetOrgRole(c fiber.Ctx) string {
	v, _ := c.Locals(LocalOrgRole).(string)
	return v
}
