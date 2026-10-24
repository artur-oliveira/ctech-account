package handler

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	oauthclient "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	"gopkg.aoctech.app/account/api/internal/problem"
)

// OAuthClientInternalHandler serves the service-to-service read of one OAuth
// client. Its first consumer is ctech-billing, which checks that the person
// connecting a client as a merchant credential owns it (console self-service
// spec § 6.3, D5).
type OAuthClientInternalHandler struct {
	svc *oauthclient.InternalService
}

func NewOAuthClientInternalHandler(svc *oauthclient.InternalService) *OAuthClientInternalHandler {
	return &OAuthClientInternalHandler{svc: svc}
}

// RegisterInternal mounts
//
//   - GET /internal/oauth-clients/:client_id
//     behind auth = RequireAuth and
//     scope = RequireInternalScope(scopes.InternalAccountOAuthClientRead)
//
// The guards are attached to the route, never to an /internal prefix group, so
// they cannot stack with another internal route's.
func (h *OAuthClientInternalHandler) RegisterInternal(v1 fiber.Router, auth, scope fiber.Handler) {
	v1.Get("/internal/oauth-clients/:client_id", auth, scope, h.get)
}

// internalOAuthClientResponse is the whole contract. It is a struct, not a
// map, so a new field shows up as a diff here, and the secret hash, redirect
// URIs, audience and first-party status cannot leak through a careless copy
// of the model.
type internalOAuthClientResponse struct {
	ClientID      string   `json:"client_id"`
	OwnerUserID   string   `json:"owner_user_id"`
	AllowedScopes []string `json:"allowed_scopes"`
	Active        bool     `json:"active"`
}

func (h *OAuthClientInternalHandler) get(c fiber.Ctx) error {
	view, err := h.svc.Describe(c.Context(), c.Params("client_id"))
	if errors.Is(err, oauthclient.ErrNotFound) {
		return problem.NotFound("OAuth client not found.", c.Path()).Send(c)
	}
	if err != nil {
		return problem.ServerError(c.Path()).WithCause(err).Send(c)
	}
	return c.JSON(internalOAuthClientResponse{
		ClientID:      view.ClientID,
		OwnerUserID:   view.OwnerUserID,
		AllowedScopes: view.AllowedScopes,
		Active:        view.Active,
	})
}
