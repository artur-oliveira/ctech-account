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
	"github.com/artur-oliveira/ctech-account/internal/domain/mfa/passkey"
	"github.com/artur-oliveira/ctech-account/internal/domain/mfa/totp"
	"github.com/artur-oliveira/ctech-account/internal/domain/session"
	"github.com/artur-oliveira/ctech-account/internal/domain/user"
	"github.com/artur-oliveira/ctech-account/internal/email"
	"github.com/artur-oliveira/ctech-account/internal/middleware"
	"github.com/gofiber/fiber/v3"
)

const mfaTokenTTL = 5 * time.Minute
const emailVerifyTTL = 24 * time.Hour
const passwordResetTTL = 15 * time.Minute

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
	passkeySvc *passkey.Service
	cache      *cache.Client
	cfg        *config.Config
	emailCli   *email.Client // nil when FROM_EMAIL is not set
}

func NewAuthHandler(userSvc *user.Service, sessionSvc *session.Service, totpSvc TOTPService, passkeySvc *passkey.Service, valkeyCache *cache.Client, cfg *config.Config, emailCli *email.Client) *AuthHandler {
	return &AuthHandler{userSvc: userSvc, sessionSvc: sessionSvc, totpSvc: totpSvc, passkeySvc: passkeySvc, cache: valkeyCache, cfg: cfg, emailCli: emailCli}
}

func (h *AuthHandler) Register(v1 fiber.Router) {
	auth := v1.Group("/auth")
	auth.Post("/register", h.register)
	auth.Post("/login", h.login)
	auth.Post("/logout", h.logout)
	auth.Post("/mfa/challenge", h.mfaChallenge)
	auth.Post("/mfa/passkey/begin", h.mfaPasskeyBegin)
	auth.Post("/mfa/passkey/complete", h.mfaPasskeyComplete)
	auth.Post("/verify-email", h.verifyEmail)
	auth.Post("/resend-verification", h.resendVerification)
	auth.Post("/forgot-password", h.forgotPassword)
	auth.Post("/reset-password", h.resetPassword)
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

	// Send verification email asynchronously — non-blocking.
	if h.emailCli != nil {
		go h.sendVerificationEmail(context.Background(), u.ID(), u.Email, u.FirstName)
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

	var methods []string
	if hasKeys, _ := h.passkeySvc.HasPasskeys(c.Context(), u.ID()); hasKeys {
		methods = append(methods, "passkey")
	}
	if totpSecret, totpErr := h.totpSvc.Get(c.Context(), u.ID()); totpErr == nil && totpSecret.IsSetup() {
		methods = append(methods, "totp")
	}

	if len(methods) > 0 {
		return issueMFAToken(c, h.cache, u.ID(), parseDeviceName(c.Get("User-Agent")), clientIP(c), c.Get("User-Agent"), methods)
	}

	return h.issueSession(c, u)
}

// issueMFAToken stores a short-lived MFA token in Valkey and sends the challenge response.
// Shared by AuthHandler (password login) and PasskeyHandler (passkey login with TOTP required).
func issueMFAToken(c fiber.Ctx, cacheClient *cache.Client, userID, deviceName, ip, userAgent string, methods []string) error {
	if cacheClient == nil || !cacheClient.Enabled() {
		return apierror.ServerError(c.Path()).Send(c)
	}

	rawToken, hashHex, err := crypto.GenerateMFAToken()
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	payload := mfaTokenPayload{
		UserID:     userID,
		DeviceName: deviceName,
		IP:         ip,
		UserAgent:  userAgent,
	}

	if err := cacheClient.Set(c.Context(), "mfa_token:"+hashHex, payload, mfaTokenTTL); err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"requires_mfa": true,
		"mfa_token":    rawToken,
		"mfa_methods":  methods,
	})
}

// issueSession creates a session, sets the cookie, and returns user info.
func (h *AuthHandler) issueSession(c fiber.Ctx, u *user.User) error {
	deviceName := parseDeviceName(c.Get("User-Agent"))
	ip := clientIP(c)
	sess, rawToken, err := h.sessionSvc.Create(c.Context(), u.ID(), deviceName, ip, c.Get("User-Agent"))
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	enrichSessionAsync(h.sessionSvc, u.ID(), sess.ID(), ip)

	cookie := &fiber.Cookie{
		Name:     "ctech_session",
		Value:    rawToken,
		HTTPOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: "Lax",
		Path:     "/",
		Domain:   effectiveCookieDomain(c, h.cfg),
		MaxAge:   int(session.SessionTTL.Seconds()),
	}
	c.Cookie(cookie)

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
	enrichSessionAsync(h.sessionSvc, u.ID(), sess.ID(), payload.IP)

	c.Cookie(&fiber.Cookie{
		Name:     "ctech_session",
		Value:    rawToken,
		HTTPOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: "Lax",
		Path:     "/",
		Domain:   effectiveCookieDomain(c, h.cfg),
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

type mfaPasskeyBeginRequest struct {
	MFAToken string `json:"mfa_token" validate:"required"`
}

// mfaPasskeyBegin peeks at the mfa_token (does not consume it) and returns a
// user-specific WebAuthn challenge. The mfa_token is consumed in mfaPasskeyComplete.
func (h *AuthHandler) mfaPasskeyBegin(c fiber.Ctx) error {
	var req mfaPasskeyBeginRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}

	if h.cache == nil || !h.cache.Enabled() {
		return apierror.ServerError(c.Path()).Send(c)
	}

	hashHex := crypto.HashToken(req.MFAToken)
	var payload mfaTokenPayload
	if err := h.cache.Get(c.Context(), "mfa_token:"+hashHex, &payload); err != nil {
		return apierror.InvalidToken("MFA token is invalid or has expired.", c.Path()).Send(c)
	}

	optionsJSON, sessionToken, err := h.passkeySvc.BeginUserAuthentication(c.Context(), payload.UserID)
	if err != nil {
		switch {
		case errors.Is(err, passkey.ErrCacheRequired):
			return apierror.ServiceUnavailable("Passkey authentication is temporarily unavailable.", c.Path()).Send(c)
		case errors.Is(err, passkey.ErrNoCredentials):
			return apierror.InvalidRequest("No passkeys registered for this account.", c.Path()).Send(c)
		default:
			return apierror.ServerError(c.Path()).Send(c)
		}
	}

	return c.JSON(fiber.Map{
		"session_token": sessionToken,
		"options":       string(optionsJSON),
	})
}

// mfaPasskeyComplete consumes the mfa_token, validates the passkey assertion, and issues a session.
// Query params: mfa_token, session_token. Body: raw WebAuthn assertion JSON.
func (h *AuthHandler) mfaPasskeyComplete(c fiber.Ctx) error {
	mfaToken := c.Query("mfa_token")
	sessionToken := c.Query("session_token")

	if mfaToken == "" || sessionToken == "" {
		return apierror.InvalidRequest("mfa_token and session_token query params are required.", c.Path()).Send(c)
	}
	if len(c.Body()) == 0 {
		return apierror.InvalidRequest("Request body with WebAuthn assertion is required.", c.Path()).Send(c)
	}

	if h.cache == nil || !h.cache.Enabled() {
		return apierror.ServerError(c.Path()).Send(c)
	}

	hashHex := crypto.HashToken(mfaToken)
	var payload mfaTokenPayload
	if err := h.cache.Get(c.Context(), "mfa_token:"+hashHex, &payload); err != nil {
		return apierror.InvalidToken("MFA token is invalid or has expired.", c.Path()).Send(c)
	}
	_ = h.cache.Delete(c.Context(), "mfa_token:"+hashHex)

	if err := h.passkeySvc.FinishUserAuthentication(c.Context(), payload.UserID, sessionToken, c.Body()); err != nil {
		switch {
		case errors.Is(err, passkey.ErrSessionExpired):
			return apierror.InvalidToken("Passkey session expired. Please try again.", c.Path()).Send(c)
		case errors.Is(err, passkey.ErrInvalidResponse):
			return apierror.InvalidRequest("Invalid passkey response.", c.Path()).Send(c)
		default:
			return apierror.Unauthorized("Passkey authentication failed.", c.Path()).Send(c)
		}
	}

	u, err := h.userSvc.GetByID(c.Context(), payload.UserID)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	sess, rawToken, err := h.sessionSvc.Create(c.Context(), u.ID(), payload.DeviceName, payload.IP, payload.UserAgent)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	enrichSessionAsync(h.sessionSvc, u.ID(), sess.ID(), payload.IP)

	c.Cookie(&fiber.Cookie{
		Name:     "ctech_session",
		Value:    rawToken,
		HTTPOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: "Lax",
		Path:     "/",
		Domain:   effectiveCookieDomain(c, h.cfg),
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
		Secure:   h.cfg.CookieSecure,
		SameSite: "Lax",
		Path:     "/",
		Domain:   effectiveCookieDomain(c, h.cfg),
		MaxAge:   -1,
	})

	// Also clear the SPA refresh token cookie.
	c.Cookie(&fiber.Cookie{
		Name:     refreshTokenCookieName,
		Value:    "",
		HTTPOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: "Lax",
		Domain:   effectiveCookieDomain(c, h.cfg),
		Path:     "/",
		MaxAge:   -1,
	})

	return c.Status(fiber.StatusNoContent).Send(nil)
}

// ── Email verification ─────────────────────────────────────────────────────

func (h *AuthHandler) sendVerificationEmail(ctx context.Context, userID, toEmail, firstName string) {
	if h.cache == nil || !h.cache.Enabled() || h.emailCli == nil {
		return
	}
	rawToken, hashHex, err := crypto.GenerateMFAToken()
	if err != nil {
		return
	}
	if err := h.cache.Set(ctx, "ev:"+hashHex, userID, emailVerifyTTL); err != nil {
		return
	}
	_ = h.emailCli.SendVerificationEmail(ctx, toEmail, firstName, rawToken)
}

func (h *AuthHandler) verifyEmail(c fiber.Ctx) error {
	type req struct {
		Token string `json:"token" validate:"required"`
	}
	var r req
	if err := parseBody(c, &r); err != nil {
		return err
	}
	if h.cache == nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	hashHex := crypto.HashToken(r.Token)
	var userID string
	if err := h.cache.Get(c.Context(), "ev:"+hashHex, &userID); err != nil {
		return apierror.InvalidToken("Verification link is invalid or has expired.", c.Path()).Send(c)
	}
	_ = h.cache.Delete(c.Context(), "ev:"+hashHex)
	if err := h.userSvc.MarkEmailVerified(c.Context(), userID); err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	return c.Status(fiber.StatusOK).JSON(fiber.Map{"verified": true})
}

func (h *AuthHandler) resendVerification(c fiber.Ctx) error {
	type req struct {
		Email string `json:"email" validate:"required,email"`
	}
	var r req
	if err := parseBody(c, &r); err != nil {
		return err
	}
	// Always respond 200 regardless — prevents email enumeration.
	u, err := h.userSvc.GetByEmail(c.Context(), strings.ToLower(r.Email))
	if err == nil && !u.EmailVerified && h.emailCli != nil {
		go h.sendVerificationEmail(context.Background(), u.ID(), u.Email, u.FirstName)
	}
	return c.Status(fiber.StatusOK).JSON(fiber.Map{"sent": true})
}

// ── Password reset ──────────────────────────────────────────────────────────

func (h *AuthHandler) forgotPassword(c fiber.Ctx) error {
	type req struct {
		Email string `json:"email" validate:"required,email"`
	}
	var r req
	if err := parseBody(c, &r); err != nil {
		return err
	}
	// Always 200 — no email enumeration.
	u, err := h.userSvc.GetByEmail(c.Context(), strings.ToLower(r.Email))
	if err == nil && h.emailCli != nil && h.cache != nil && h.cache.Enabled() {
		rawToken, hashHex, genErr := crypto.GenerateMFAToken()
		if genErr == nil {
			if setErr := h.cache.Set(c.Context(), "pr:"+hashHex, u.ID(), passwordResetTTL); setErr == nil {
				go func() {
					err := h.emailCli.SendPasswordResetEmail(context.Background(), u.Email, u.FirstName, rawToken)
					if err != nil {

					}
				}()
			}
		}
	}
	return c.Status(fiber.StatusOK).JSON(fiber.Map{"sent": true})
}

func (h *AuthHandler) resetPassword(c fiber.Ctx) error {
	type req struct {
		Token       string `json:"token"        validate:"required"`
		NewPassword string `json:"new_password" validate:"required,min=8,max=128"`
	}
	var r req
	if err := parseBody(c, &r); err != nil {
		return err
	}
	if h.cache == nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	hashHex := crypto.HashToken(r.Token)
	var userID string
	if err := h.cache.Get(c.Context(), "pr:"+hashHex, &userID); err != nil {
		return apierror.InvalidToken("Reset link is invalid or has expired.", c.Path()).Send(c)
	}
	_ = h.cache.Delete(c.Context(), "pr:"+hashHex)

	hash, err := crypto.HashPassword(r.NewPassword)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	if _, err := h.userSvc.GetByID(c.Context(), userID); err != nil {
		return apierror.InvalidToken("Reset link is invalid or has expired.", c.Path()).Send(c)
	}
	if updErr := h.userSvc.ForceSetPassword(c.Context(), userID, hash); updErr != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	return c.Status(fiber.StatusOK).JSON(fiber.Map{"reset": true})
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
