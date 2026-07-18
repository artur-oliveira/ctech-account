package handler

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/internal/apierror"
	"gopkg.aoctech.app/account/internal/domain/audit"
	oauthclient "gopkg.aoctech.app/account/internal/domain/oauth/client"
	"gopkg.aoctech.app/account/internal/middleware"
)

// OAuthClientsHandler exposes self-service OAuth application management.
type OAuthClientsHandler struct {
	clientSvc *oauthclient.Service
	audit     *audit.Service
}

func NewOAuthClientsHandler(clientSvc *oauthclient.Service, auditSvc *audit.Service) *OAuthClientsHandler {
	return &OAuthClientsHandler{clientSvc: clientSvc, audit: auditSvc}
}

func (h *OAuthClientsHandler) Register(account fiber.Router, stepUp fiber.Handler) {
	account.Get("/oauth-clients", h.list)
	account.Post("/oauth-clients", stepUp, h.create)
	account.Put("/oauth-clients/:id", stepUp, h.update)
	account.Delete("/oauth-clients/:id", stepUp, h.remove)
	account.Post("/oauth-clients/:id/regenerate-secret", stepUp, h.regenerateSecret)
}

// clientResponse renders a client without ever exposing the secret hash.
func clientResponse(c *oauthclient.OAuthClient) fiber.Map {
	return fiber.Map{
		"client_id":      c.ID(),
		"name":           c.Name,
		"client_type":    c.ClientType,
		"redirect_uris":  c.RedirectURIs,
		"allowed_scopes": c.AllowedScopes,
		"audience":       c.Audience,
		"created_at":     c.CreatedAt,
		"updated_at":     c.UpdatedAt,
	}
}

// sendClientError maps domain errors to RFC 7807 problems.
func sendClientError(c fiber.Ctx, err error) error {
	var scopeErr oauthclient.ErrInvalidScope
	switch {
	case errors.Is(err, oauthclient.ErrNotFound):
		return apierror.NotFound("OAuth client not found.", c.Path()).Send(c)
	case errors.Is(err, oauthclient.ErrForbidden):
		return apierror.Forbidden("This OAuth client belongs to another account.", c.Path()).Send(c)
	case errors.Is(err, oauthclient.ErrInvalidClientType),
		errors.Is(err, oauthclient.ErrInvalidRedirectURI),
		errors.Is(err, oauthclient.ErrNotConfidential):
		return apierror.InvalidRequest(err.Error(), c.Path()).Send(c)
	case errors.As(err, &scopeErr):
		return apierror.InvalidRequest(scopeErr.Error(), c.Path()).Send(c)
	default:
		return apierror.ServerError(c.Path()).Send(c)
	}
}

func (h *OAuthClientsHandler) list(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	clients, err := h.clientSvc.List(c.Context(), userID)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	result := make([]fiber.Map, 0, len(clients))
	for _, cl := range clients {
		result = append(result, clientResponse(cl))
	}
	return c.JSON(fiber.Map{"oauth_clients": result})
}

type createOAuthClientRequest struct {
	Name          string   `json:"name"           validate:"required,max=100"`
	ClientType    string   `json:"client_type"    validate:"required,oneof=public confidential"`
	RedirectURIs  []string `json:"redirect_uris"  validate:"required,min=1,max=10,dive,uri,max=500"`
	AllowedScopes []string `json:"allowed_scopes" validate:"required,min=1,max=20,dive,max=100"`
	Audience      []string `json:"audience"       validate:"omitempty,max=10,dive,max=500"`
}

func (h *OAuthClientsHandler) create(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	var req createOAuthClientRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}

	cl, rawSecret, err := h.clientSvc.Create(c.Context(), userID, req.Name, req.ClientType, req.RedirectURIs, req.AllowedScopes, req.Audience)
	if err != nil {
		return sendClientError(c, err)
	}

	recordAudit(c, h.audit, userID, audit.EventOAuthClientCreated, map[string]string{"client_id": cl.ID()})

	resp := clientResponse(cl)
	if rawSecret != "" {
		// The raw secret is returned exactly once — only its hash is stored.
		resp["client_secret"] = rawSecret
	}
	return c.Status(fiber.StatusCreated).JSON(resp)
}

type updateOAuthClientRequest struct {
	Name          string   `json:"name"           validate:"required,max=100"`
	RedirectURIs  []string `json:"redirect_uris"  validate:"required,min=1,max=10,dive,uri,max=500"`
	AllowedScopes []string `json:"allowed_scopes" validate:"required,min=1,max=20,dive,max=100"`
	Audience      []string `json:"audience"       validate:"omitempty,max=10,dive,max=500"`
}

func (h *OAuthClientsHandler) update(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)
	clientID := c.Params("id")

	var req updateOAuthClientRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}

	cl, err := h.clientSvc.Update(c.Context(), userID, clientID, req.Name, req.RedirectURIs, req.AllowedScopes, req.Audience)
	if err != nil {
		return sendClientError(c, err)
	}
	recordAudit(c, h.audit, userID, audit.EventOAuthClientUpdated, map[string]string{"client_id": clientID})
	return c.JSON(clientResponse(cl))
}

func (h *OAuthClientsHandler) remove(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)
	clientID := c.Params("id")

	if err := h.clientSvc.Delete(c.Context(), userID, clientID); err != nil {
		return sendClientError(c, err)
	}
	recordAudit(c, h.audit, userID, audit.EventOAuthClientDeleted, map[string]string{"client_id": clientID})
	return c.Status(fiber.StatusNoContent).Send(nil)
}

func (h *OAuthClientsHandler) regenerateSecret(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)
	clientID := c.Params("id")

	rawSecret, err := h.clientSvc.RegenerateSecret(c.Context(), userID, clientID)
	if err != nil {
		return sendClientError(c, err)
	}
	recordAudit(c, h.audit, userID, audit.EventOAuthClientUpdated, map[string]string{"client_id": clientID, "method": "regenerate_secret"})
	return c.JSON(fiber.Map{"client_secret": rawSecret})
}
