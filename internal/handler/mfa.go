package handler

import (
	"context"
	"errors"

	"github.com/artur-oliveira/ctech-account/internal/apierror"
	"github.com/artur-oliveira/ctech-account/internal/config"
	"github.com/artur-oliveira/ctech-account/internal/domain/mfa/totp"
	"github.com/artur-oliveira/ctech-account/internal/domain/user"
	"github.com/artur-oliveira/ctech-account/internal/middleware"
	"github.com/gofiber/fiber/v3"
)

// TOTPManagementService is the full interface the MFA management handler needs.
type TOTPManagementService interface {
	Get(ctx context.Context, userID string) (*totp.TOTPSecret, error)
	Generate(ctx context.Context, userID, accountName, issuer string) (*totp.TOTPSecret, string, error)
	Verify(ctx context.Context, userID, code string) ([]string, error)
	Validate(ctx context.Context, userID, code string) (bool, error)
	Remove(ctx context.Context, userID string) error
	RegenerateBackupCodes(ctx context.Context, userID string) ([]string, error)
}

type MFAHandler struct {
	totpSvc TOTPManagementService
	userSvc *user.Service
	cfg     *config.Config
}

func NewMFAHandler(totpSvc TOTPManagementService, userSvc *user.Service, cfg *config.Config) *MFAHandler {
	return &MFAHandler{totpSvc: totpSvc, userSvc: userSvc, cfg: cfg}
}

func (h *MFAHandler) Register(account fiber.Router) {
	mfa := account.Group("/mfa")
	mfa.Get("/totp/setup", h.totpSetup)
	mfa.Post("/totp/confirm", h.totpConfirm)
	mfa.Delete("/totp", h.totpRemove)
	mfa.Post("/totp/backup-codes", h.totpRegenerateBackupCodes)
}

func (h *MFAHandler) totpSetup(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	u, err := h.userSvc.GetByID(c.Context(), userID)
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			return apierror.NotFound("User", c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}

	_, provisioningURI, err := h.totpSvc.Generate(c.Context(), userID, u.Email, h.cfg.BaseURL)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	return c.JSON(fiber.Map{
		"provisioning_uri": provisioningURI,
	})
}

type totpConfirmRequest struct {
	Code string `json:"code" validate:"required,len=6,numeric"`
}

func (h *MFAHandler) totpConfirm(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	var req totpConfirmRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}

	backupCodes, err := h.totpSvc.Verify(c.Context(), userID, req.Code)
	if err != nil {
		switch {
		case errors.Is(err, totp.ErrNotFound):
			return apierror.NotFound("TOTP configuration", c.Path()).Send(c)
		case errors.Is(err, totp.ErrAlreadyVerified):
			return apierror.Conflict("TOTP is already active for this account.", c.Path()).Send(c)
		case errors.Is(err, totp.ErrInvalidCode):
			return apierror.Unauthorized("The TOTP code is invalid or has expired.", c.Path()).Send(c)
		default:
			return apierror.ServerError(c.Path()).Send(c)
		}
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"backup_codes": backupCodes,
	})
}

func (h *MFAHandler) totpRemove(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	if err := h.totpSvc.Remove(c.Context(), userID); err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	return c.Status(fiber.StatusNoContent).Send(nil)
}

func (h *MFAHandler) totpRegenerateBackupCodes(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	codes, err := h.totpSvc.RegenerateBackupCodes(c.Context(), userID)
	if err != nil {
		if errors.Is(err, totp.ErrNotFound) {
			return apierror.NotFound("TOTP configuration", c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}

	return c.JSON(fiber.Map{
		"backup_codes": codes,
	})
}
