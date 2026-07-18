package handler

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/internal/apierror"
	"gopkg.aoctech.app/account/internal/cache"
	"gopkg.aoctech.app/account/internal/config"
	"gopkg.aoctech.app/account/internal/crypto"
	"gopkg.aoctech.app/account/internal/domain/audit"
	"gopkg.aoctech.app/account/internal/domain/mfa/passkey"
	"gopkg.aoctech.app/account/internal/domain/mfa/totp"
	oauthclient "gopkg.aoctech.app/account/internal/domain/oauth/client"
	"gopkg.aoctech.app/account/internal/domain/session"
	"gopkg.aoctech.app/account/internal/domain/user"
	"gopkg.aoctech.app/account/internal/email"
	"gopkg.aoctech.app/account/internal/legal"
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
	clientRepo oauthclient.Repository
	cache      *cache.Client
	cfg        *config.Config
	emailCli   *email.Client // nil when FROM_EMAIL is not set
	audit      *audit.Service
}

func NewAuthHandler(userSvc *user.Service, sessionSvc *session.Service, totpSvc TOTPService, passkeySvc *passkey.Service, clientRepo oauthclient.Repository, valkeyCache *cache.Client, cfg *config.Config, emailCli *email.Client, auditSvc *audit.Service) *AuthHandler {
	return &AuthHandler{userSvc: userSvc, sessionSvc: sessionSvc, totpSvc: totpSvc, passkeySvc: passkeySvc, clientRepo: clientRepo, cache: valkeyCache, cfg: cfg, emailCli: emailCli, audit: auditSvc}
}

func (h *AuthHandler) Register(v1 fiber.Router) {
	auth := v1.Group("/auth")
	auth.Post("/register", h.register)
	auth.Post("/login", h.login)
	auth.Post("/logout", h.logout)
	auth.Get("/end-session", h.endSession)
	auth.Post("/mfa/challenge", h.mfaChallenge)
	auth.Post("/mfa/passkey/begin", h.mfaPasskeyBegin)
	auth.Post("/mfa/passkey/complete", h.mfaPasskeyComplete)
	auth.Post("/verify-email", h.verifyEmail)
	auth.Post("/resend-verification", h.resendVerification)
	auth.Post("/forgot-password", h.forgotPassword)
	auth.Post("/reset-password", h.resetPassword)
}

type registerRequest struct {
	Email       string `json:"email"        validate:"required,email,max=254"`
	Password    string `json:"password"     validate:"required,min=8,max=128"`
	FirstName   string `json:"first_name"   validate:"required,max=100"`
	LastName    string `json:"last_name"    validate:"omitempty,max=100"`
	AcceptTerms bool   `json:"accept_terms" validate:"required"`
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
			// Respond exactly as on success — revealing the conflict here would let
			// anyone enumerate registered addresses. The address owner is told by email.
			if h.emailCli != nil {
				if existing, getErr := h.userSvc.GetByEmail(c.Context(), req.Email); getErr == nil {
					go func() {
						if sendErr := h.emailCli.SendAccountExistsEmail(context.Background(), existing.Email, existing.FirstName); sendErr != nil {
							log.Printf("register: failed to send account-exists email: %v", sendErr)
						}
					}()
				}
			}
			return registrationAccepted(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}

	recordAudit(c, h.audit, u.ID(), audit.EventTermsAccepted, map[string]string{
		"tos_version":     legal.CurrentToSVersion,
		"privacy_version": legal.CurrentPrivacyVersion,
	})

	// Send verification email asynchronously — non-blocking.
	if h.emailCli != nil {
		go h.sendVerificationEmail(context.Background(), u.ID(), u.Email, u.FirstName)
	}

	return registrationAccepted(c)
}

// registrationAccepted is the single response shape for POST /auth/register. It must
// stay identical for new and already-registered addresses (no enumeration signal).
func registrationAccepted(c fiber.Ctx) error {
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"pending_verification": true,
		"message":              "If this email address can be registered, a verification link has been sent to it.",
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
		// Email verification is a hard gate: no session, no tokens, no access to
		// any downstream service until the address is confirmed.
		if errors.Is(err, user.ErrEmailNotVerified) {
			return apierror.EmailNotVerified(c.Path()).Send(c)
		}
		if known, getErr := h.userSvc.GetByEmail(c.Context(), strings.ToLower(req.Email)); getErr == nil {
			recordAudit(c, h.audit, known.ID(), audit.EventLoginFailed, nil)
		} else {
			recordAuditAnon(c, h.audit, audit.EventLoginFailed, map[string]string{"email_domain": emailDomain(req.Email)})
		}
		// A disabled account is reported as invalid credentials on purpose: a
		// distinct error would confirm that the account exists.
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
		recordAudit(c, h.audit, u.ID(), audit.EventLoginMFARequired, nil)
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
	sess, rawToken, err := h.sessionSvc.Create(c.Context(), u.ID(), deviceName, ip, c.Get("User-Agent"), []string{session.AMRPassword})
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	enrichSessionAsync(h.sessionSvc, u.ID(), sess.ID(), ip)
	recordAudit(c, h.audit, u.ID(), audit.EventLoginSuccess, map[string]string{"session_id": sess.ID()})

	setSessionCookies(c, h.cfg, rawToken)

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
	// GetDel consumes the MFA token atomically (single use, replay-safe).
	if err := h.cache.GetDel(c.Context(), "mfa_token:"+hashHex, &payload); err != nil {
		return apierror.InvalidToken("MFA token is invalid or has expired.", c.Path()).Send(c)
	}

	valid, err := h.totpSvc.Validate(c.Context(), payload.UserID, req.Code)
	if err != nil || !valid {
		recordAudit(c, h.audit, payload.UserID, audit.EventMFAChallengeFailed, map[string]string{"method": "totp"})
		return apierror.Unauthorized("Invalid MFA code.", c.Path()).Send(c)
	}

	u, err := h.userSvc.GetByID(c.Context(), payload.UserID)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	sess, rawToken, err := h.sessionSvc.Create(c.Context(), u.ID(), payload.DeviceName, payload.IP, payload.UserAgent, []string{session.AMRPassword, session.AMRTOTP})
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	enrichSessionAsync(h.sessionSvc, u.ID(), sess.ID(), payload.IP)
	recordAudit(c, h.audit, u.ID(), audit.EventMFAChallengeSuccess, map[string]string{"method": "totp", "session_id": sess.ID()})

	setSessionCookies(c, h.cfg, rawToken)

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
	// GetDel consumes the MFA token atomically (single use, replay-safe).
	if err := h.cache.GetDel(c.Context(), "mfa_token:"+hashHex, &payload); err != nil {
		return apierror.InvalidToken("MFA token is invalid or has expired.", c.Path()).Send(c)
	}

	if err := h.passkeySvc.FinishUserAuthentication(c.Context(), payload.UserID, sessionToken, c.Body()); err != nil {
		recordAudit(c, h.audit, payload.UserID, audit.EventMFAChallengeFailed, map[string]string{"method": "passkey"})
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

	sess, rawToken, err := h.sessionSvc.Create(c.Context(), u.ID(), payload.DeviceName, payload.IP, payload.UserAgent, []string{session.AMRPassword, session.AMRWebAuthn})
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	enrichSessionAsync(h.sessionSvc, u.ID(), sess.ID(), payload.IP)
	recordAudit(c, h.audit, u.ID(), audit.EventMFAChallengeSuccess, map[string]string{"method": "passkey", "session_id": sess.ID()})

	setSessionCookies(c, h.cfg, rawToken)

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"user_id":    u.ID(),
		"email":      u.Email,
		"first_name": u.FirstName,
		"last_name":  u.LastName,
		"session_id": sess.ID(),
	})
}

func (h *AuthHandler) logout(c fiber.Ctx) error {
	// /auth/logout runs without RequireAuth, so the session is identified by the
	// SSO cookie rather than bearer locals. Revoke it server-side: clearing the
	// cookie alone leaves the session token valid until its 90-day TTL, so a
	// copied or stolen ctech_session could still be replayed after logout.
	if cookieValue := c.Cookies(sessionCookieName); cookieValue != "" {
		if sess, err := h.sessionSvc.ValidateToken(c.Context(), cookieValue); err == nil {
			_ = h.sessionSvc.Revoke(c.Context(), sess.UserID(), sess.ID())
		}
	}
	// Also drop the per-client refresh token when the SPA sends its cookie.
	if rt := c.Cookies(refreshTokenCookieName); rt != "" {
		_ = h.sessionSvc.RevokeClientToken(c.Context(), rt)
	}

	clearAuthCookie(c, h.cfg, sessionCookieName)
	// Also clear the SPA refresh token cookie and the JS-readable auth hint.
	clearAuthCookie(c, h.cfg, refreshTokenCookieName)
	setAuthHintCookie(c, h.cfg, clearCookieMaxAge)

	return c.Status(fiber.StatusNoContent).Send(nil)
}

// endSession is the RP-Initiated-Logout endpoint (OIDC-style): it ends the
// browser's SSO session, not just a downstream client's local tokens. A
// downstream RP (e.g. dfe) that only clears its own tokens leaves ctech_session
// valid, so GET /authorize silently re-authenticates on the very next login
// attempt instead of showing a fresh login. Public GET (top-level navigation
// from the RP), authenticated by the SSO cookie itself rather than a bearer token.
func (h *AuthHandler) endSession(c fiber.Ctx) error {
	if cookieValue := c.Cookies(sessionCookieName); cookieValue != "" {
		if sess, err := h.sessionSvc.ValidateToken(c.Context(), cookieValue); err == nil {
			_ = h.sessionSvc.Revoke(c.Context(), sess.UserID(), sess.ID())
		}
	}

	clearAuthCookie(c, h.cfg, sessionCookieName)
	clearAuthCookie(c, h.cfg, refreshTokenCookieName)
	setAuthHintCookie(c, h.cfg, clearCookieMaxAge)

	redirectTo := h.cfg.AppURL + "/login"
	if postLogout := c.Query("post_logout_redirect_uri"); postLogout != "" {
		if oauthClient, err := h.clientRepo.GetByID(c.Context(), c.Query("client_id")); err == nil && oauthClient.IsPostLogoutRedirectAllowed(postLogout) {
			redirectTo = postLogout
		}
	}
	return c.Redirect().Status(fiber.StatusFound).To(redirectTo)
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
	if err := h.emailCli.SendVerificationEmail(ctx, toEmail, firstName, rawToken); err != nil {
		log.Printf("send-verification-email: failed to send verification email to user %s: %v", userID, err)
	}

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
	// GetDel consumes the token atomically so a link cannot be replayed.
	if err := h.cache.GetDel(c.Context(), "ev:"+hashHex, &userID); err != nil {
		return apierror.InvalidToken("Verification link is invalid or has expired.", c.Path()).Send(c)
	}
	if err := h.userSvc.MarkEmailVerified(c.Context(), userID); err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	recordAudit(c, h.audit, userID, audit.EventEmailVerified, nil)
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
	if err == nil {
		recordAudit(c, h.audit, u.ID(), audit.EventPasswordResetRequest, nil)
	}
	if err == nil && h.emailCli != nil && h.cache != nil && h.cache.Enabled() {
		rawToken, hashHex, genErr := crypto.GenerateMFAToken()
		if genErr == nil {
			if setErr := h.cache.Set(c.Context(), "pr:"+hashHex, u.ID(), passwordResetTTL); setErr == nil {
				go func() {
					if err := h.emailCli.SendPasswordResetEmail(context.Background(), u.Email, u.FirstName, rawToken); err != nil {
						log.Printf("forgot-password: failed to send reset email to user %s: %v", u.ID(), err)
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
	// GetDel consumes the token atomically so a reset link cannot be replayed.
	if err := h.cache.GetDel(c.Context(), "pr:"+hashHex, &userID); err != nil {
		return apierror.InvalidToken("Reset link is invalid or has expired.", c.Path()).Send(c)
	}

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
	// Revoke every existing session: a password reset must lock out anyone
	// (including an attacker) holding a refresh token issued before the reset.
	if revErr := h.sessionSvc.RevokeAll(c.Context(), userID, ""); revErr != nil {
		log.Printf("reset-password: failed to revoke sessions for user %s: %v", userID, revErr)
	}
	recordAudit(c, h.audit, userID, audit.EventPasswordResetComplete, nil)
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
