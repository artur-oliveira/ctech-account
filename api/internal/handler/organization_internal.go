package handler

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/organization"
)

// RegisterInternal mounts the service-to-service route a product uses to learn
// whether a person belongs to an organization and with which role.
//
// internalAuth = RequireAuth + RequireInternalScope(scopes.InternalAccountOrgMember).
// The guard is attached to this route alone: /internal/organizations is shared
// with the company identity read, which has its own scope.
func (h *OrganizationHandler) RegisterInternal(v1 fiber.Router, internalAuth ...fiber.Handler) {
	handlers := make([]any, 0, len(internalAuth)+1)
	for _, m := range internalAuth {
		handlers = append(handlers, m)
	}
	handlers = append(handlers, h.internalMember)
	v1.Get("/internal/organizations/:organization_id/members/:user_id", handlers[0], handlers[1:]...)
}

// internalMember answers whether one person belongs to one organization, and
// with which ladder role.
//
// The role is read from the membership row on every call (Service.RoleOf), never
// from a token: the caller caches the answer with a TTL it controls, and a
// demotion or removal lands when that cache expires.
//
// A non-member and an organization that does not exist answer identically — 200
// {"member":false} — for the same reason the company reach route does: this is
// asked speculatively about ids a caller may not be entitled to, so a refusal
// must not reveal which organizations exist, and "not a member" is an answer
// where a 404 would invite the caller to read a refusal and an outage alike.
func (h *OrganizationHandler) internalMember(c fiber.Ctx) error {
	role, err := h.svc.RoleOf(c.Context(), c.Params("organization_id"), c.Params("user_id"))
	if errors.Is(err, organization.ErrNotAMember) {
		return c.JSON(fiber.Map{"member": false})
	}
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err).Send(c)
	}
	return c.JSON(fiber.Map{"member": true, "role": role})
}
