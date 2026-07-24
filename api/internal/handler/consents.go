package handler

import (
	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/audit"
	oauthclient "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	"gopkg.aoctech.app/account/api/internal/domain/oauth/consent"
	"gopkg.aoctech.app/account/api/internal/middleware"
)

// ConsentsHandler exposes the user's "connected applications" — OAuth clients
// they granted scopes to — with revocation.
type ConsentsHandler struct {
	consentSvc *consent.Service
	clientRepo oauthclient.Repository
	audit      *audit.Service
}

func NewConsentsHandler(consentSvc *consent.Service, clientRepo oauthclient.Repository, auditSvc *audit.Service) *ConsentsHandler {
	return &ConsentsHandler{consentSvc: consentSvc, clientRepo: clientRepo, audit: auditSvc}
}

func (h *ConsentsHandler) Register(account fiber.Router) {
	account.Get("/consents", h.list)
	account.Delete("/consents/:clientID", h.revoke)
}

func (h *ConsentsHandler) list(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	grants, err := h.consentSvc.List(c.Context(), userID)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	result := make([]fiber.Map, 0, len(grants))
	for _, g := range grants {
		clientName := g.ClientID()
		if cl, cErr := h.clientRepo.GetByID(c.Context(), g.ClientID()); cErr == nil {
			clientName = cl.Name
		}
		result = append(result, fiber.Map{
			"client_id":   g.ClientID(),
			"client_name": clientName,
			"scopes":      g.Scopes,
			"created_at":  g.CreatedAt,
			"updated_at":  g.UpdatedAt,
		})
	}
	return c.JSON(fiber.Map{"consents": result})
}

func (h *ConsentsHandler) revoke(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)
	clientID := c.Params("clientID")

	if err := h.consentSvc.Revoke(c.Context(), userID, clientID); err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	recordAudit(c, h.audit, userID, audit.EventConsentRevoked, map[string]string{"client_id": clientID})
	return c.Status(fiber.StatusNoContent).Send(nil)
}
