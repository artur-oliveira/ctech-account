package handler

import (
	"context"
	"errors"
	"strings"

	"github.com/artur-oliveira/ctech-account/internal/apierror"
	"github.com/artur-oliveira/ctech-account/internal/domain/mfa/totp"
	"github.com/artur-oliveira/ctech-account/internal/domain/session"
	"github.com/artur-oliveira/ctech-account/internal/domain/user"
	"github.com/artur-oliveira/ctech-account/internal/middleware"
	"github.com/artur-oliveira/ctech-account/internal/validate"
	"github.com/gofiber/fiber/v3"
)

// TOTPService is the minimal interface the auth handler needs for MFA gate checks.
type TOTPService interface {
	Get(ctx context.Context, userID string) (*totp.TOTPSecret, error)
}

type AuthHandler struct {
	userSvc    *user.Service
	sessionSvc *session.Service
	totpSvc    TOTPService
}

func NewAuthHandler(userSvc *user.Service, sessionSvc *session.Service, totpSvc TOTPService) *AuthHandler {
	return &AuthHandler{userSvc: userSvc, sessionSvc: sessionSvc, totpSvc: totpSvc}
}

func (h *AuthHandler) Register(v1 fiber.Router) {
	auth := v1.Group("/auth")
	auth.Post("/register", h.register)
	auth.Post("/login", h.login)
	auth.Post("/logout", h.logout)
}

type registerRequest struct {
	Email     string `json:"email"      validate:"required,email,max=254"`
	Password  string `json:"password"   validate:"required,min=8,max=128"`
	FirstName string `json:"first_name" validate:"required,max=100"`
	LastName  string `json:"last_name"  validate:"omitempty,max=100"`
}

func (h *AuthHandler) register(c fiber.Ctx) error {
	var req registerRequest
	if err := c.Bind().JSON(&req); err != nil {
		return apierror.InvalidRequest("Request body is malformed or contains invalid JSON.", c.Path()).Send(c)
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if err := validate.Struct(req); err != nil {
		ve, _ := validate.IsValidationError(err)
		return apierror.ValidationFailed(ve.Detail(), c.Path()).Send(c)
	}

	u, err := h.userSvc.Register(c.Context(), req.Email, req.Password, req.FirstName, req.LastName)
	if err != nil {
		if errors.Is(err, user.ErrEmailConflict) {
			return apierror.Conflict("An account with this email address already exists.", c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"user_id":    u.ID(),
		"email":      u.Email,
		"first_name": u.FirstName,
		"last_name":  u.LastName,
	})
}

type loginRequest struct {
	Email    string `json:"email"    validate:"required,email"`
	Password string `json:"password" validate:"required"`
}

func (h *AuthHandler) login(c fiber.Ctx) error {
	var req loginRequest
	if err := c.Bind().JSON(&req); err != nil {
		return apierror.InvalidRequest("Request body is malformed or contains invalid JSON.", c.Path()).Send(c)
	}
	if err := validate.Struct(req); err != nil {
		ve, _ := validate.IsValidationError(err)
		return apierror.ValidationFailed(ve.Detail(), c.Path()).Send(c)
	}

	u, err := h.userSvc.Login(c.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, user.ErrAccountDisabled) {
			return apierror.AccountDisabled(c.Path()).Send(c)
		}
		return apierror.InvalidCredentials(c.Path()).Send(c)
	}

	// Sprint 2: when TOTP is configured, issue mfa_token instead of a full session.
	totpSecret, totpErr := h.totpSvc.Get(c.Context(), u.ID())
	if totpErr == nil && totpSecret.IsSetup() {
		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"requires_mfa": true,
			"mfa_methods":  []string{"totp"},
		})
	}

	deviceName := parseDeviceName(c.Get("User-Agent"))
	sess, rawToken, err := h.sessionSvc.Create(c.Context(), u.ID(), deviceName, c.IP(), c.Get("User-Agent"))
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	c.Cookie(&fiber.Cookie{
		Name:     "ctech_session",
		Value:    session.BuildCookieValue(u.ID(), sess.ID(), rawToken),
		HTTPOnly: true,
		Secure:   true,
		SameSite: "Lax",
		Path:     "/",
		MaxAge:   int(session.SessionTTL.Seconds()),
	})

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"user_id":    u.ID(),
		"email":      u.Email,
		"first_name": u.FirstName,
		"last_name":  u.LastName,
		"session_id": sess.ID(),
	})
}

func (h *AuthHandler) logout(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)
	sessionID := middleware.GetSessionID(c)

	if sessionID != "" {
		_ = h.sessionSvc.Revoke(c.Context(), userID, sessionID)
	}

	c.Cookie(&fiber.Cookie{
		Name:     "ctech_session",
		Value:    "",
		HTTPOnly: true,
		Secure:   true,
		SameSite: "Lax",
		Path:     "/",
		MaxAge:   -1,
	})

	return c.Status(fiber.StatusNoContent).Send(nil)
}

func parseDeviceName(userAgent string) string {
	ua := strings.ToLower(userAgent)
	switch {
	case strings.Contains(ua, "chrome"):
		return "Chrome"
	case strings.Contains(ua, "firefox"):
		return "Firefox"
	case strings.Contains(ua, "safari"):
		return "Safari"
	case strings.Contains(ua, "edge"):
		return "Edge"
	case strings.Contains(ua, "curl"):
		return "curl"
	default:
		if len(userAgent) > 50 {
			return userAgent[:50]
		}
		return userAgent
	}
}
