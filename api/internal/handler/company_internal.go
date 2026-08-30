package handler

import (
	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
)

// RegisterInternal mounts the one service-to-service route a product needs:
// whether a person may act for a company.
//
// A route rather than a claim in the access token. Minting reach into a JWT
// makes a revocation wait for the token to expire, which is the defect
// RequireOrgRole already refuses to have ("a role minted into a JWT outlives
// its own revocation for the token's lifetime"). The caller caches the answer
// with a TTL it controls and can drop.
//
// internalAuth = RequireAuth + RequireInternalScope(scopes.InternalAccountCompanyActor).
func (h *CompanyHandler) RegisterInternal(v1 fiber.Router, internalAuth ...fiber.Handler) {
	handlers := make([]any, len(internalAuth))
	for i, m := range internalAuth {
		handlers[i] = m
	}
	grp := v1.Group("/internal/companies", handlers...)
	grp.Get("/:company_id/actors/:user_id", h.internalReach)
}

// internalReach answers whether one person may act for one company.
//
// The company id alone, with no organization: the caller does not know it, and
// finding it out is half of what it is asking. The answer carries it back so the
// product can store both ids and keep its own authorization to one read.
//
// A person who cannot reach the company and a company that does not exist get
// the same 200 with may_act false. Telling them apart would make this a probe
// for which company ids are real — and 200 rather than 404 because "not
// permitted" is an answer, while a 404 invites the caller to treat a refusal and
// an outage alike.
func (h *CompanyHandler) internalReach(c fiber.Ctx) error {
	orgID, mayAct, err := h.svc.ReachOf(c.Context(), c.Params("company_id"), c.Params("user_id"))
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err).Send(c)
	}
	if !mayAct {
		// No organization on a refusal: naming one would say the company exists.
		return c.JSON(fiber.Map{"may_act": false})
	}
	// Reach and the organization, and nothing else. A role or a permission list
	// here would be the platform holding the product's vocabulary
	// (ctech-billing ADR 0023).
	return c.JSON(fiber.Map{"may_act": true, "organization_id": orgID})
}
