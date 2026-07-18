package handler

import (
	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/internal/apierror"
	"gopkg.aoctech.app/account/internal/scopes"
)

// ScopesHandler serves the grantable-scope catalog so UIs can render fixed
// pickers instead of free-form scope input.
type ScopesHandler struct {
	catalogSvc *scopes.CatalogService
}

func NewScopesHandler(catalogSvc *scopes.CatalogService) *ScopesHandler {
	return &ScopesHandler{catalogSvc: catalogSvc}
}

func (h *ScopesHandler) Register(v1 fiber.Router) {
	v1.Get("/scopes", h.list)
}

func (h *ScopesHandler) list(c fiber.Ctx) error {
	services, err := h.catalogSvc.Catalog(c.Context())
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	return c.JSON(fiber.Map{"services": scopes.FilterPublic(services)})
}
