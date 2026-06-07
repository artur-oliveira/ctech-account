package handler

import (
	"errors"

	"github.com/artur-oliveira/ctech-account/internal/apierror"
	"github.com/artur-oliveira/ctech-account/internal/domain/user"
	"github.com/artur-oliveira/ctech-account/internal/middleware"
	"github.com/gofiber/fiber/v3"
)

type UserInfoHandler struct {
	userSvc *user.Service
}

func NewUserInfoHandler(userSvc *user.Service) *UserInfoHandler {
	return &UserInfoHandler{userSvc: userSvc}
}

func (h *UserInfoHandler) UserInfo(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	u, err := h.userSvc.GetByID(c.Context(), userID)
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			return apierror.NotFound("User", c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}

	return c.JSON(fiber.Map{
		"sub":            u.ID(),
		"email":          u.Email,
		"name":           u.FullName(),
		"given_name":     u.FirstName,
		"family_name":    u.LastName,
		"email_verified": u.EmailVerified,
	})
}
