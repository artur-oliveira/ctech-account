package handler

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/middleware"
	"gopkg.aoctech.app/account/api/internal/scopes"
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

	resp := fiber.Map{
		"sub":                u.ID(),
		"email":              u.Email,
		"name":               u.FullName(),
		"preferred_username": u.DisplayOrFullName(),
		"given_name":         u.FirstName,
		"family_name":        u.LastName,
		"email_verified":     u.EmailVerified,
	}
	if middleware.HasScope(c, scopes.KYC) && u.KYCLevel != "" {
		resp["kyc_level"] = u.KYCLevel
	}
	return c.JSON(resp)
}
