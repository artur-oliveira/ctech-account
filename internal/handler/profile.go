package handler

import (
	"errors"

	"github.com/artur-oliveira/ctech-account/internal/apierror"
	"github.com/artur-oliveira/ctech-account/internal/domain/user"
	"github.com/artur-oliveira/ctech-account/internal/middleware"
	"github.com/gofiber/fiber/v3"
)

type ProfileHandler struct {
	userSvc *user.Service
}

func NewProfileHandler(userSvc *user.Service) *ProfileHandler {
	return &ProfileHandler{userSvc: userSvc}
}

func (h *ProfileHandler) Register(account fiber.Router) {
	account.Get("/profile", h.get)
	account.Put("/profile", h.update)
	account.Put("/password", h.changePassword)
}

func (h *ProfileHandler) get(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	u, err := h.userSvc.GetByID(c.Context(), userID)
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			return apierror.NotFound("User", c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}

	return c.JSON(fiber.Map{
		"user_id":        u.ID(),
		"email":          u.Email,
		"first_name":     u.FirstName,
		"last_name":      u.LastName,
		"display_name":   u.DisplayName,
		"avatar_url":     u.AvatarURL,
		"email_verified": u.EmailVerified,
		"created_at":     u.CreatedAt,
	})
}

type updateProfileRequest struct {
	FirstName   string `json:"first_name"   validate:"required,max=100"`
	LastName    string `json:"last_name"    validate:"omitempty,max=100"`
	DisplayName string `json:"display_name" validate:"omitempty,max=100"`
}

func (h *ProfileHandler) update(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	var req updateProfileRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}

	if err := h.userSvc.UpdateProfile(c.Context(), userID, req.FirstName, req.LastName, req.DisplayName); err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	u, err := h.userSvc.GetByID(c.Context(), userID)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	return c.JSON(fiber.Map{
		"user_id":      u.ID(),
		"first_name":   u.FirstName,
		"last_name":    u.LastName,
		"display_name": u.DisplayName,
	})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password" validate:"required"`
	NewPassword     string `json:"new_password"     validate:"required,min=8,max=128"`
}

func (h *ProfileHandler) changePassword(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	var req changePasswordRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}

	if err := h.userSvc.ChangePassword(c.Context(), userID, req.CurrentPassword, req.NewPassword); err != nil {
		if errors.Is(err, user.ErrCurrentPasswordIncorrect) {
			return apierror.InvalidCredentials(c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}

	return c.Status(fiber.StatusNoContent).Send(nil)
}
