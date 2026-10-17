package handler

import (
	"errors"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/user"
)

// UserContactInternalHandler serves the service-to-service read of one person's
// verified contact. First consumer: ctech-billing's merchant tenant closure,
// which must e-mail the owner at the account's own address.
type UserContactInternalHandler struct {
	users *user.Service
}

func NewUserContactInternalHandler(users *user.Service) *UserContactInternalHandler {
	return &UserContactInternalHandler{users: users}
}

// RegisterInternal mounts GET /internal/users/:sub/contact behind
// auth = RequireAuth and scope = RequireInternalScope(scopes.InternalAccountUserContactRead).
// The guards are attached to the route, never to an /internal group.
func (h *UserContactInternalHandler) RegisterInternal(v1 fiber.Router, auth, scope fiber.Handler) {
	v1.Get("/internal/users/:sub/contact", auth, scope, h.get)
}

// internalUserContactResponse is the whole contract (a struct, so a new field is
// a diff here): never a phone, document, address or anything else of the person.
type internalUserContactResponse struct {
	Email         string `json:"email"`
	FirstName     string `json:"first_name"`
	EmailVerified bool   `json:"email_verified"`
}

func (h *UserContactInternalHandler) get(c fiber.Ctx) error {
	u, err := h.users.GetByID(c.Context(), c.Params("sub"))
	if errors.Is(err, user.ErrNotFound) {
		return apierror.NotFound("User not found.", c.Path()).Send(c)
	}
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err).Send(c)
	}
	// A locked (deletion pending) or purged account has no contact to give.
	if u.DeletionState != "" {
		return apierror.NotFound("User not found.", c.Path()).Send(c)
	}
	return c.JSON(internalUserContactResponse{Email: u.Email, FirstName: u.FirstName, EmailVerified: u.EmailVerified})
}
