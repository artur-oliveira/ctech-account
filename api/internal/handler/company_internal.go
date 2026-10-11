package handler

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/organization"
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
	// Middleware is attached per route, not to a prefix group: another internal
	// route shares /internal/organizations (membership, behind its own scope), and
	// a group guard would stack this scope on it.
	with := func(h fiber.Handler) []any { return append(append([]any(nil), handlers...), h) }
	reach := with(h.internalReach)
	v1.Get("/internal/companies/:company_id/actors/:user_id", reach[0], reach[1:]...)
	// Identity, which is a different question from reach and gets its own
	// route rather than being folded into the answer above. Reach is asked on
	// every request and must stay one small answer; identity is asked once,
	// when a product first links a company, and carries the names.
	// Both ids, because the handoff hands the product both: looking the
	// organization up from the company alone would need an index that exists
	// for nothing else.
	identity := with(h.internalIdentity)
	v1.Get("/internal/organizations/:organization_id/companies/:company_id", identity[0], identity[1:]...)
	// The organization's companies, each with the identity above: what a
	// product needs to offer a picker before it holds a company id
	// (ctech-billing console self-service part 2, decision 1).
	list := with(h.internalList)
	v1.Get("/internal/organizations/:organization_id/companies", list[0], list[1:]...)
}

// internalCompanyDTO is one company of the internal list: the identity read's
// company fields plus the id. The organization fields are hoisted to the
// envelope, said once. No timestamps, no import provenance, no role.
type internalCompanyDTO struct {
	CompanyID string `json:"company_id"`
	TaxID     string `json:"tax_id"`
	TaxIDKind string `json:"tax_id_kind"`
	LegalName string `json:"legal_name"`
	TradeName string `json:"trade_name"`
}

// internalList lists an organization's companies, ordered by legal name then
// id, at most InternalCompanyListLimit of them, truncated saying whether there
// were more.
//
// An unknown organization is 404, as the identity read answers a pair that does
// not resolve, and for the same reason: the caller was handed this id. An
// organization with no companies is 200 with an empty list.
func (h *CompanyHandler) internalList(c fiber.Ctx) error {
	orgID := c.Params("organization_id")
	kind, err := h.orgs.KindOf(c.Context(), orgID)
	if errors.Is(err, organization.ErrNotFound) {
		return apierror.NotFound("organization", c.Path()).Send(c)
	}
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err).Send(c)
	}
	companies, err := h.svc.List(c.Context(), orgID)
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err).Send(c)
	}
	truncated := len(companies) > InternalCompanyListLimit
	if truncated {
		companies = companies[:InternalCompanyListLimit]
	}
	out := make([]internalCompanyDTO, 0, len(companies))
	for _, item := range companies {
		out = append(out, internalCompanyDTO{
			CompanyID: item.ID, TaxID: item.TaxID, TaxIDKind: item.TaxIDKind,
			LegalName: item.LegalName, TradeName: item.TradeName,
		})
	}
	return c.JSON(fiber.Map{
		"organization_id":   orgID,
		"organization_kind": kind,
		"companies":         out,
		"truncated":         truncated,
	})
}

// internalIdentity returns who a company IS: the tax id, its kind, the legal
// name, and the organization it belongs to.
//
// This is the half of ADR 0022 that products READ — accounts owns the identity
// and every product needs it to issue in that company's name. It carries no
// role and no permission, for the same reason the reach answer does not.
//
// An unknown company is 404 here, unlike reach's 200-with-false. The difference
// is deliberate: reach is asked speculatively about ids a caller may not be
// entitled to, so its refusal must be indistinguishable from absence. This is
// asked about a company the caller was just handed, so "there is no such
// company" is the useful answer and discloses nothing they did not already
// have.
func (h *CompanyHandler) internalIdentity(c fiber.Ctx) error {
	orgID := c.Params("organization_id")
	company, err := h.svc.Get(c.Context(), orgID, c.Params("company_id"))
	if err != nil {
		return apierror.NotFound("company", c.Path()).Send(c)
	}
	kind, err := h.orgs.KindOf(c.Context(), orgID)
	if err != nil {
		return apierror.NotFound("company", c.Path()).Send(c)
	}
	return c.JSON(fiber.Map{
		"organization_id":   orgID,
		"organization_kind": kind,
		"tax_id":            company.TaxID,
		"tax_id_kind":       company.TaxIDKind,
		"legal_name":        company.LegalName,
		"trade_name":        company.TradeName,
	})
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
	kind, err := h.orgs.KindOf(c.Context(), orgID)
	if errors.Is(err, organization.ErrNotFound) {
		// An edge left pointing at a workspace that is gone reaches nothing.
		return c.JSON(fiber.Map{"may_act": false})
	}
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err).Send(c)
	}
	// Reach, the organization and its kind, and nothing else. A role or a
	// permission list here would be the platform holding the product's
	// vocabulary (ctech-billing ADR 0023). The kind is there so the DF-e can
	// refuse a company in a space on its own (ctech-dfe spec 2026-10-09).
	return c.JSON(fiber.Map{"may_act": true, "organization_id": orgID, "organization_kind": kind})
}

// InternalCompanyListLimit caps the internal company list. One organization's
// page comes back in one answer: at most this many identities, a few hundred
// bytes each, well under what a caller's body cap reads (ctech-billing caps at
// 256 KiB). An accountant past it gets the first page by name and truncated.
const InternalCompanyListLimit = 200
