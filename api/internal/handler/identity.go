package handler

import (
	"errors"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/middleware"
	"gopkg.aoctech.app/api-commons/authguard"
)

// verifyIdentity (deletion ruling R2, also gating the data export): a recent MFA proof, or the account password.
// A password-less (Google-only) account relies on the e-mail confirmation.
func verifyIdentity(c fiber.Ctx, users *user.Service, userID, password string) error {
	if last := middleware.GetLastMFAAt(c); last != 0 && time.Since(time.Unix(last, 0)) <= authguard.DefaultStepUpMaxAge {
		return nil
	}
	hasPassword, err := users.HasPassword(c.Context(), userID)
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err)
	}
	if !hasPassword {
		return nil
	}
	if password == "" {
		return apierror.StepUpRequired(authguard.DefaultStepUpMaxAge, c.Path())
	}
	if err := users.CheckPassword(c.Context(), userID, password); err != nil {
		if errors.Is(err, user.ErrInvalidCredentials) {
			return apierror.InvalidCredentials(c.Path())
		}
		return apierror.ServerError(c.Path()).WithCause(err)
	}
	return nil
}
