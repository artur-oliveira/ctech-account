package handler

import (
	"github.com/artur-oliveira/ctech-account/internal/apierror"
	"github.com/artur-oliveira/ctech-account/internal/domain/user"
	"github.com/gofiber/fiber/v3"
)

type InternalHandler struct {
	userSvc       *user.Service
	internalToken string
}

func NewInternalHandler(userSvc *user.Service, internalToken string) *InternalHandler {
	return &InternalHandler{userSvc: userSvc, internalToken: internalToken}
}

func (h *InternalHandler) Register(app fiber.Router) {
	internal := app.Group("/internal/v1.0", h.requireToken)
	internal.Post("/users/migrate", h.migrateUser)
}

func (h *InternalHandler) requireToken(c fiber.Ctx) error {
	if h.internalToken == "" || c.Get("X-Internal-Token") != h.internalToken {
		return apierror.Unauthorized("Invalid internal token.", c.Path()).Send(c)
	}
	return c.Next()
}

type migrateUserRequest struct {
	Email        string `json:"email" validate:"required,email"`
	PasswordHash string `json:"password_hash" validate:"required"`
	FirstName    string `json:"first_name" validate:"required"`
	LastName     string `json:"last_name" validate:"required"`
}

// migrateUser creates (or returns existing) a ctech user from a py-dfe user.
// RegisterWithHash is idempotent — returns the existing user if the email already exists.
func (h *InternalHandler) migrateUser(c fiber.Ctx) error {
	var req migrateUserRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}

	u, err := h.userSvc.RegisterWithHash(c.Context(), req.Email, req.PasswordHash, req.FirstName, req.LastName)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"user_id": u.ID(),
		"email":   u.Email,
	})
}
