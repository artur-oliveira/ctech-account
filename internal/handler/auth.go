package handler

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/artur-oliveira/ctech-account/internal/apierror"
	"github.com/artur-oliveira/ctech-account/internal/cache"
	"github.com/artur-oliveira/ctech-account/internal/config"
	"github.com/artur-oliveira/ctech-account/internal/crypto"
	"github.com/artur-oliveira/ctech-account/internal/domain/mfa/totp"
	"github.com/artur-oliveira/ctech-account/internal/domain/session"
	"github.com/artur-oliveira/ctech-account/internal/domain/user"
	"github.com/artur-oliveira/ctech-account/internal/middleware"
	"github.com/gofiber/fiber/v3"
)

const mfaTokenTTL = 5 * time.Minute

// TOTPService is the subset of totp.Service the auth handler needs.
type TOTPService interface {
	Get(ctx context.Context, userID string) (*totp.TOTPSecret, error)
	Validate(ctx context.Context, userID, code string) (bool, error)
}

type mfaTokenPayload struct {
	UserID     string `json:"user_id"`
	DeviceName string `json:"device_name"`
	IP         string `json:"ip"`
	UserAgent  string `json:"user_agent"`
}

type AuthHandler struct {
	userSvc    *user.Service
	sessionSvc *session.Service
	totpSvc    TOTPService
	cache      *cache.Client
	cfg        *config.Config
}

func NewAuthHandler(userSvc *user.Service, sessionSvc *session.Service, totpSvc TOTPService, valkeyCache *cache.Client, cfg *config.Config) *AuthHandler {
	return &AuthHandler{userSvc: userSvc, sessionSvc: sessionSvc, totpSvc: totpSvc, cache: valkeyCache, cfg: cfg}
}

func (h *AuthHandler) Register(v1 fiber.Router) {
	auth := v1.Group("/auth")
	auth.Post("/register", h.register)
	auth.Post("/login", h.login)
	auth.Post("/logout", h.logout)
	auth.Post("/mfa/challenge", h.mfaChallenge)
}

type registerRequest struct {
	Email     string `json:"email"      validate:"required,email,max=254"`
	Password  string `json:"password"   validate:"required,min=8,max=128"`
	FirstName string `json:"first_name" validate:"required,max=100"`
	LastName  string `json:"last_name"  validate:"omitempty,max=100"`
}

func (h *AuthHandler) register(c fiber.Ctx) error {
	var req registerRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

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
	if err := parseBody(c, &req); err != nil {
		return err
	}

	u, err := h.userSvc.Login(c.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, user.ErrAccountDisabled) {
			return apierror.AccountDisabled(c.Path()).Send(c)
		}
		return apierror.InvalidCredentials(c.Path()).Send(c)
	}

	totpSecret, totpErr := h.totpSvc.Get(c.Context(), u.ID())
	if totpErr == nil && totpSecret.IsSetup() {
		return h.issueMFAToken(c, u)
	}

	return h.issueSession(c, u)
}

// issueMFAToken stores a short-lived token in Valkey and returns it to the client for MFA completion.
func (h *AuthHandler) issueMFAToken(c fiber.Ctx, u *user.User) error {
	if h.cache == nil || !h.cache.Enabled() {
		return apierror.ServerError(c.Path()).Send(c)
	}

	rawToken, hashHex, err := crypto.GenerateMFAToken()
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	payload := mfaTokenPayload{
		UserID:     u.ID(),
		DeviceName: parseDeviceName(c.Get("User-Agent")),
		IP:         c.IP(),
		UserAgent:  c.Get("User-Agent"),
	}

	if err := h.cache.Set(c.Context(), "mfa_token:"+hashHex, payload, mfaTokenTTL); err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"requires_mfa": true,
		"mfa_token":    rawToken,
		"mfa_methods":  []string{"totp"},
	})
}

// issueSession creates a session, sets the cookie, and returns user info.
func (h *AuthHandler) issueSession(c fiber.Ctx, u *user.User) error {
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

type mfaChallengeRequest struct {
	MFAToken string `json:"mfa_token" validate:"required"`
	Code     string `json:"code"      validate:"required"`
}

func (h *AuthHandler) mfaChallenge(c fiber.Ctx) error {
	var req mfaChallengeRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}

	if h.cache == nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	hashHex := crypto.HashToken(req.MFAToken)
	var payload mfaTokenPayload
	if err := h.cache.Get(c.Context(), "mfa_token:"+hashHex, &payload); err != nil {
		return apierror.InvalidToken("MFA token is invalid or has expired.", c.Path()).Send(c)
	}
	_ = h.cache.Delete(c.Context(), "mfa_token:"+hashHex) // single use

	valid, err := h.totpSvc.Validate(c.Context(), payload.UserID, req.Code)
	if err != nil || !valid {
		return apierror.Unauthorized("Invalid MFA code.", c.Path()).Send(c)
	}

	u, err := h.userSvc.GetByID(c.Context(), payload.UserID)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	sess, rawToken, err := h.sessionSvc.Create(c.Context(), u.ID(), payload.DeviceName, payload.IP, payload.UserAgent)
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

	// Also clear the SPA refresh token cookie.
	c.Cookie(&fiber.Cookie{
		Name:     refreshTokenCookieName,
		Value:    "",
		HTTPOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: "Lax",
		Domain:   h.cfg.CookieDomain,
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
