package handler

import (
	"errors"
	"log"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/audit"
	"gopkg.aoctech.app/account/api/internal/domain/session"
	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/legal"
	"gopkg.aoctech.app/account/api/internal/middleware"
)

type ProfileHandler struct {
	userSvc    *user.Service
	sessionSvc *session.Service
	audit      *audit.Service
}

func NewProfileHandler(userSvc *user.Service, sessionSvc *session.Service, auditSvc *audit.Service) *ProfileHandler {
	return &ProfileHandler{userSvc: userSvc, sessionSvc: sessionSvc, audit: auditSvc}
}

func (h *ProfileHandler) Register(account fiber.Router, stepUp fiber.Handler) {
	account.Get("/profile", h.get)
	account.Put("/profile", h.update)
	account.Put("/password", stepUp, h.changePassword)
	account.Post("/password", h.setInitialPassword)
	// Unlink Google: step-up gated because removing a login method is
	// security-sensitive. Linking is driven by the authenticated Google
	// callback (social.go) and audited there.
	account.Delete("/link/google", stepUp, h.unlinkGoogle)
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
		// has_password is false for accounts created via Google that never set one.
		// The UI uses it to offer "create a password" instead of "change password".
		"has_password": u.PasswordHash != "",
		// google_linked tells the UI whether to offer "Link Google"
		// (false) or "Unlink Google" (true). The raw sub is never
		// exposed — only whether one is bound.
		"google_linked": u.GoogleSub != "",
		"created_at":    u.CreatedAt,
		// terms_pending drives the in-app re-acceptance gate. /authorize catches a
		// version bump for anyone arriving through OAuth, but a session already
		// holding a refreshable token never passes through it again.
		"terms_pending": legal.PendingFor(u.TOSVersion, u.PrivacyVersion),
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

	recordAudit(c, h.audit, userID, audit.EventPasswordChanged, nil)
	h.revokeOtherSessions(c, userID, "change-password")

	return c.Status(fiber.StatusNoContent).Send(nil)
}

type setInitialPasswordRequest struct {
	NewPassword string `json:"new_password" validate:"required,min=8,max=128"`
}

// setInitialPassword lets an account created via Google add a password later.
// It only ever succeeds when no password is set; changing an existing one requires
// the current password (PUT /account/password).
func (h *ProfileHandler) setInitialPassword(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	var req setInitialPasswordRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}

	if err := h.userSvc.SetInitialPassword(c.Context(), userID, req.NewPassword); err != nil {
		if errors.Is(err, user.ErrPasswordAlreadySet) {
			return apierror.Conflict("This account already has a password. Use the change-password endpoint.", c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}

	recordAudit(c, h.audit, userID, audit.EventPasswordChanged, map[string]string{"method": "initial"})
	h.revokeOtherSessions(c, userID, "set-initial-password")

	return c.Status(fiber.StatusNoContent).Send(nil)
}

// unlinkGoogle removes the bound Google identity. Sstep-up gated
// (like password changes) because removing a login method is
// security-sensitive. Passwordless accounts are refused — they would
// otherwise have no way to log in.
func (h *ProfileHandler) unlinkGoogle(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	if err := h.userSvc.UnlinkGoogle(c.Context(), userID); err != nil {
		if errors.Is(err, user.ErrCannotUnlink) {
			return apierror.Conflict("Cannot unlink Google without a password. Set a password first.", c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}

	recordAudit(c, h.audit, userID, audit.EventSocialUnlinked, nil)
	return c.Status(fiber.StatusNoContent).Send(nil)
}

// revokeOtherSessions logs out every device except the caller's current session.
// Any password mutation must call this so a stolen refresh token stops working.
func (h *ProfileHandler) revokeOtherSessions(c fiber.Ctx, userID, op string) {
	if err := h.sessionSvc.RevokeAll(c.Context(), userID, middleware.GetSessionID(c)); err != nil {
		log.Printf("%s: failed to revoke other sessions for user %s: %v", op, userID, err)
	}
}
