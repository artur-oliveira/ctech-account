package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/company"
	"gopkg.aoctech.app/account/api/internal/domain/company/registry"
	"gopkg.aoctech.app/account/api/internal/domain/organization"
	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/middleware"
)

// CompanyHandler exposes the companies inside an organization.
//
// It holds the organization service only to build the RequireOrgRole guard —
// the authorization is that middleware's, not this handler's.
type CompanyHandler struct {
	svc    *company.Service
	orgs   *organization.Service
	users  *user.Service
	lookup registry.Lookup
}

func NewCompanyHandler(svc *company.Service, orgs *organization.Service, users *user.Service, lookup registry.Lookup) *CompanyHandler {
	return &CompanyHandler{svc: svc, orgs: orgs, users: users, lookup: lookup}
}

// Register mounts the routes on the organizations group, which already carries
// RequireAuth and RequireClientID: a company is a first-party action, not
// something a delegated token does on a user's behalf.
func (h *CompanyHandler) Register(orgs fiber.Router) {
	scoped := func(floor string) fiber.Handler { return middleware.RequireOrgRole(h.orgs, floor) }
	orgs.Get("/:id/companies", scoped(organization.RoleViewer), h.list)
	orgs.Post("/:id/companies", scoped(organization.RoleAdmin), h.register)
	// Before /:company_id, or Fiber captures "lookup" as a company id.
	orgs.Get("/:id/companies/lookup", scoped(organization.RoleAdmin), h.lookupTaxID)
	orgs.Get("/:id/companies/:company_id", scoped(organization.RoleViewer), h.get)
	orgs.Patch("/:id/companies/:company_id", scoped(organization.RoleAdmin), h.rename)
	orgs.Get("/:id/companies/:company_id/actors", scoped(organization.RoleViewer), h.listActors)
	orgs.Put("/:id/companies/:company_id/actors/:user_id", scoped(organization.RoleAdmin), h.grantActor)
	orgs.Delete("/:id/companies/:company_id/actors/:user_id", scoped(organization.RoleAdmin), h.revokeActor)
}

type registerCompanyRequest struct {
	TaxID     string `json:"tax_id" validate:"required,max=32"`
	LegalName string `json:"legal_name" validate:"required,max=200"`
	TradeName string `json:"trade_name" validate:"max=200"`
}

type renameCompanyRequest struct {
	LegalName string `json:"legal_name" validate:"required,max=200"`
	TradeName string `json:"trade_name" validate:"max=200"`
}

type companyDTO struct {
	ID string `json:"id"`
	// Canonical: mask stripped, letters uppercased. A CNPJ is alphanumeric in
	// its first twelve positions, so clients must not assume digits.
	TaxID     string `json:"tax_id"`
	TaxIDKind string `json:"tax_id_kind"`
	LegalName string `json:"legal_name"`
	TradeName string `json:"trade_name,omitempty"`
	CreatedAt string `json:"created_at"`
}

type actorDTO struct {
	UserID    string `json:"user_id"`
	Name      string `json:"name,omitempty"`
	GrantedBy string `json:"granted_by,omitempty"`
	CreatedAt string `json:"created_at"`
}

func toCompanyDTO(c *company.Company) companyDTO {
	return companyDTO{
		ID: c.ID, TaxID: c.TaxID, TaxIDKind: c.TaxIDKind,
		LegalName: c.LegalName, TradeName: c.TradeName,
		CreatedAt: c.CreatedAt.Format(time.RFC3339),
	}
}

func (h *CompanyHandler) list(c fiber.Ctx) error {
	companies, err := h.svc.List(c.Context(), middleware.GetOrgID(c))
	if err != nil {
		return companyProblem(c, err)
	}
	out := make([]companyDTO, 0, len(companies))
	for _, item := range companies {
		out = append(out, toCompanyDTO(item))
	}
	return c.JSON(fiber.Map{"companies": out})
}

func (h *CompanyHandler) register(c fiber.Ctx) error {
	var req registerCompanyRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}
	created, err := h.svc.Register(c.Context(), middleware.GetOrgID(c), middleware.GetUserID(c),
		h.callerName(c), req.TaxID, req.LegalName, req.TradeName)
	if err != nil {
		return companyProblem(c, err)
	}
	return c.Status(http.StatusCreated).JSON(toCompanyDTO(created))
}

func (h *CompanyHandler) get(c fiber.Ctx) error {
	found, err := h.svc.Get(c.Context(), middleware.GetOrgID(c), c.Params("company_id"))
	if err != nil {
		return companyProblem(c, err)
	}
	return c.JSON(toCompanyDTO(found))
}

func (h *CompanyHandler) rename(c fiber.Ctx) error {
	var req renameCompanyRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}
	err := h.svc.Rename(c.Context(), middleware.GetOrgID(c), c.Params("company_id"), req.LegalName, req.TradeName)
	if err != nil {
		return companyProblem(c, err)
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h *CompanyHandler) listActors(c fiber.Ctx) error {
	actors, err := h.svc.ListActors(c.Context(), middleware.GetOrgID(c), c.Params("company_id"))
	if err != nil {
		return companyProblem(c, err)
	}
	out := make([]actorDTO, 0, len(actors))
	for _, a := range actors {
		out = append(out, actorDTO{
			UserID: a.UserID, Name: a.Name, GrantedBy: a.GrantedBy,
			CreatedAt: a.CreatedAt.Format(time.RFC3339),
		})
	}
	return c.JSON(fiber.Map{"actors": out})
}

// grantActor is idempotent by design — PUT, not POST. Granting an edge somebody
// already holds is not an error worth an answer that reads like one.
func (h *CompanyHandler) grantActor(c fiber.Ctx) error {
	targetUserID := c.Params("user_id")
	err := h.svc.GrantActor(c.Context(), middleware.GetOrgID(c), c.Params("company_id"),
		targetUserID, h.nameOf(c, targetUserID), middleware.GetUserID(c))
	if err != nil {
		return companyProblem(c, err)
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h *CompanyHandler) revokeActor(c fiber.Ctx) error {
	err := h.svc.RevokeActor(c.Context(), middleware.GetOrgID(c), c.Params("company_id"), c.Params("user_id"))
	if err != nil {
		return companyProblem(c, err)
	}
	return c.SendStatus(http.StatusNoContent)
}

// lookupTaxID fills the names for a CNPJ.
//
// A miss is 200 with nothing found, never a 404: the register not knowing a
// company says nothing about whether the company exists, and a 404 here would
// read as "invalid CNPJ" to a screen that has to branch on something.
func (h *CompanyHandler) lookupTaxID(c fiber.Ctx) error {
	canonical, kind, ok := company.NormalizeTaxID(c.Query("tax_id"))
	if !ok {
		return apierror.ValidationFailed("That is not a valid CNPJ or CPF.", c.Path()).Send(c)
	}
	// No lookup for a CPF, deliberately: a person's name is not ours to fetch
	// from a public register.
	if kind != company.KindCNPJ || h.lookup == nil {
		return c.JSON(fiber.Map{"found": false})
	}
	names, found := h.lookup.Names(c.Context(), canonical)
	if !found {
		return c.JSON(fiber.Map{"found": false})
	}
	return c.JSON(fiber.Map{
		"found": true, "legal_name": names.LegalName, "trade_name": names.TradeName,
	})
}

func (h *CompanyHandler) callerName(c fiber.Ctx) string {
	return h.nameOf(c, middleware.GetUserID(c))
}

// nameOf resolves a display name for the row about to be written. A failure is
// not worth refusing the write over: a row with no name renders as the user id,
// which is what every row looked like before names were stored.
func (h *CompanyHandler) nameOf(c fiber.Ctx, userID string) string {
	if h.users == nil {
		return ""
	}
	u, err := h.users.GetByID(c.Context(), userID)
	if err != nil {
		return ""
	}
	return u.DisplayOrFullName()
}

func companyProblem(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, company.ErrInvalidTaxID):
		return apierror.ValidationFailed("That is not a valid CNPJ or CPF.", c.Path()).Send(c)
	case errors.Is(err, company.ErrInvalidName):
		return apierror.ValidationFailed("The legal name is required and must be at most 200 characters.", c.Path()).Send(c)
	case errors.Is(err, company.ErrTaxIDTaken):
		// A conflict, not a validation failure: the request was well formed and
		// the state refused it.
		return apierror.Conflict("This organization already has a company with that CNPJ or CPF.", c.Path()).Send(c)
	case errors.Is(err, company.ErrNotFound):
		// The same answer the organization routes give, for the same reason:
		// confirming that a company id is real is itself a disclosure.
		return apierror.Forbidden("You do not have access to this organization.", c.Path()).Send(c)
	default:
		return apierror.ServerError(c.Path()).WithCause(err).Send(c)
	}
}
