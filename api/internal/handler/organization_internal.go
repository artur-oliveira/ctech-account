package handler

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/organization"
)

// RegisterInternal mounts the two service-to-service routes a product uses for
// workspaces, each behind its own scope (guards are attached per route, never to
// a /internal prefix group, so the scopes cannot stack):
//
//   - GET /internal/organizations/:organization_id/members/:user_id
//     behind memberScope = RequireInternalScope(scopes.InternalAccountOrgMember)
//   - GET /internal/users/:user_id/organizations
//     behind listScope = RequireInternalScope(scopes.InternalAccountUserOrganizations)
//
// auth = RequireAuth.
func (h *OrganizationHandler) RegisterInternal(v1 fiber.Router, auth, memberScope, listScope fiber.Handler) {
	v1.Get("/internal/organizations/:organization_id/members/:user_id", auth, memberScope, h.internalMember)
	v1.Get("/internal/users/:user_id/organizations", auth, listScope, h.internalUserOrganizations)
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
	role, kind, err := h.svc.MembershipOf(c.Context(), c.Params("organization_id"), c.Params("user_id"))
	if errors.Is(err, organization.ErrNotAMember) {
		// No kind on a refusal: it would tell a prober which ids are spaces.
		return c.JSON(fiber.Map{"member": false})
	}
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err).Send(c)
	}
	// The kind travels with the role because a role alone does not say what it
	// grants: `member` is full access in a space and narrower in an
	// organization (ctech-billing ADR 0027).
	return c.JSON(fiber.Map{"member": true, "role": role, "kind": kind})
}

// internalUserOrganizations lists the organizations a person belongs to, with
// their role in each, for a product that builds a space switcher (ctech-billing
// ADR 0025). It is information, not authorization: the product resolves every
// request's space through internalMember, so a stale or generous list cannot
// grant access.
//
// An unknown user answers an empty list rather than 404, for the reason the
// membership route does: asked about ids a caller may not be entitled to, a
// refusal must not reveal which exist.
func (h *OrganizationHandler) internalUserOrganizations(c fiber.Ctx) error {
	userID := c.Params("user_id")
	workspaces, err := h.svc.ListWorkspaces(c.Context(), userID)
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err).Send(c)
	}
	out := make([]fiber.Map, 0, len(workspaces))
	for _, w := range workspaces {
		item := fiber.Map{"id": w.ID, "display_name": w.DisplayName, "kind": w.Kind, "role": w.Role}
		// Counts for billing's plan screen (spec § 6): only on the person's own
		// spaces — a member gets no counts for somebody else's.
		if w.Kind == organization.KindPersonal && w.Role == organization.RoleOwner && w.OwnerUserID == userID {
			people, pending, err := h.svc.SpaceCounts(c.Context(), w.ID)
			if err != nil {
				return apierror.ServerError(c.Path()).WithCause(err).Send(c)
			}
			item["people"], item["pending_invitations"] = people, pending
		}
		out = append(out, item)
	}
	return c.JSON(fiber.Map{"organizations": out})
}
