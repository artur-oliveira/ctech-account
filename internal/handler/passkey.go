package handler

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/internal/apierror"
	"gopkg.aoctech.app/account/internal/cache"
	"gopkg.aoctech.app/account/internal/config"
	"gopkg.aoctech.app/account/internal/domain/audit"
	"gopkg.aoctech.app/account/internal/domain/mfa/passkey"
	"gopkg.aoctech.app/account/internal/domain/session"
	"gopkg.aoctech.app/account/internal/domain/user"
	"gopkg.aoctech.app/account/internal/middleware"
)

type PasskeyHandler struct {
	passkeySvc *passkey.Service
	userSvc    *user.Service
	sessionSvc *session.Service
	totpSvc    TOTPService
	cache      *cache.Client
	cfg        *config.Config
	audit      *audit.Service
}

func NewPasskeyHandler(passkeySvc *passkey.Service, userSvc *user.Service, sessionSvc *session.Service, totpSvc TOTPService, valkeyCache *cache.Client, cfg *config.Config, auditSvc *audit.Service) *PasskeyHandler {
	return &PasskeyHandler{passkeySvc: passkeySvc, userSvc: userSvc, sessionSvc: sessionSvc, totpSvc: totpSvc, cache: valkeyCache, cfg: cfg, audit: auditSvc}
}

// RegisterManagement registers the authenticated passkey management routes under /account/mfa.
// RegisterManagement mounts the passkey management routes. Registration stays
// open (it is the enrollment path); deletion requires a recent MFA proof.
func (h *PasskeyHandler) RegisterManagement(account fiber.Router, stepUp fiber.Handler) {
	pk := account.Group("/mfa/passkeys")
	pk.Get("/", h.list)
	pk.Post("/register/begin", h.registerBegin)
	// Complete: session_token + name as query params; raw WebAuthn credential JSON as body.
	pk.Post("/register/complete", h.registerComplete)
	pk.Delete("/:id", stepUp, h.delete)
}

// RegisterAuth registers the unauthenticated passkey authentication routes under /auth.
func (h *PasskeyHandler) RegisterAuth(auth fiber.Router) {
	pk := auth.Group("/passkeys")
	pk.Post("/authenticate/begin", h.authenticateBegin)
	// Complete: session_token as query param; raw WebAuthn assertion JSON as body.
	pk.Post("/authenticate/complete", h.authenticateComplete)
}

// ── Management (Bearer auth required) ────────────────────────────────────────

func (h *PasskeyHandler) list(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	creds, err := h.passkeySvc.List(c.Context(), userID)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	result := make([]fiber.Map, 0, len(creds))
	for _, cr := range creds {
		result = append(result, fiber.Map{
			"id":           cr.CredentialIDHex(),
			"name":         cr.Name,
			"transports":   cr.Transports,
			"aaguid":       cr.AAGUID,
			"created_at":   cr.CreatedAt,
			"last_used_at": cr.LastUsedAt,
		})
	}

	return c.JSON(fiber.Map{"passkeys": result})
}

type registerBeginRequest struct {
	Name string `json:"name" validate:"required,max=100"`
}

func (h *PasskeyHandler) registerBegin(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	var req registerBeginRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}

	u, err := h.userSvc.GetByID(c.Context(), userID)
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			return apierror.NotFound("User", c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}

	waUser, err := h.passkeySvc.LoadUser(c.Context(), u.ID(), u.Email, u.FullName())
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	optionsJSON, sessionToken, err := h.passkeySvc.BeginRegistration(c.Context(), waUser)
	if err != nil {
		if errors.Is(err, passkey.ErrCacheRequired) {
			return apierror.ServiceUnavailable("Passkey registration is temporarily unavailable.", c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}

	return c.JSON(fiber.Map{
		"session_token": sessionToken,
		"name":          req.Name,
		"options":       string(optionsJSON),
	})
}

// registerComplete receives session_token + name as query params and the raw
// PublicKeyCredential JSON (from navigator.credentials.create()) as the body.
func (h *PasskeyHandler) registerComplete(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)
	sessionToken := c.Query("session_token")
	name := c.Query("name")

	if sessionToken == "" || name == "" {
		return apierror.InvalidRequest("session_token and name query params are required.", c.Path()).Send(c)
	}
	if len(c.Body()) == 0 {
		return apierror.InvalidRequest("Request body with WebAuthn credential is required.", c.Path()).Send(c)
	}

	u, err := h.userSvc.GetByID(c.Context(), userID)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	waUser, err := h.passkeySvc.LoadUser(c.Context(), u.ID(), u.Email, u.FullName())
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	cred, err := h.passkeySvc.FinishRegistration(c.Context(), userID, name, sessionToken, c.Body(), waUser)
	if err != nil {
		switch {
		case errors.Is(err, passkey.ErrSessionExpired):
			return apierror.InvalidToken("Registration session expired. Please start over.", c.Path()).Send(c)
		case errors.Is(err, passkey.ErrInvalidResponse):
			return apierror.InvalidRequest("Invalid WebAuthn response.", c.Path()).Send(c)
		default:
			return apierror.ServerError(c.Path()).Send(c)
		}
	}

	recordAudit(c, h.audit, userID, audit.EventPasskeyAdded, map[string]string{"device_name": cred.Name})

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":         cred.CredentialIDHex(),
		"name":       cred.Name,
		"transports": cred.Transports,
		"created_at": cred.CreatedAt,
	})
}

func (h *PasskeyHandler) delete(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)
	idHex := c.Params("id")

	credSK, err := passkey.CredentialSKFromHex(idHex)
	if err != nil {
		return apierror.InvalidRequest("Invalid passkey ID.", c.Path()).Send(c)
	}

	if err := h.passkeySvc.Delete(c.Context(), userID, credSK); err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	recordAudit(c, h.audit, userID, audit.EventPasskeyRemoved, nil)

	return c.Status(fiber.StatusNoContent).Send(nil)
}

// ── Authentication (public) ───────────────────────────────────────────────────

func (h *PasskeyHandler) authenticateBegin(c fiber.Ctx) error {
	optionsJSON, sessionToken, err := h.passkeySvc.BeginAuthentication(c.Context())
	if err != nil {
		if errors.Is(err, passkey.ErrCacheRequired) {
			return apierror.ServiceUnavailable("Passkey authentication is temporarily unavailable.", c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}

	return c.JSON(fiber.Map{
		"session_token": sessionToken,
		"options":       string(optionsJSON),
	})
}

// authenticateComplete receives session_token as a query param and the raw
// PublicKeyCredential assertion JSON (from navigator.credentials.get()) as the body.
// If the authenticated user has TOTP enabled, returns an mfa_token instead of a session.
func (h *PasskeyHandler) authenticateComplete(c fiber.Ctx) error {
	sessionToken := c.Query("session_token")
	if sessionToken == "" {
		return apierror.InvalidRequest("session_token query param is required.", c.Path()).Send(c)
	}
	if len(c.Body()) == 0 {
		return apierror.InvalidRequest("Request body with WebAuthn assertion is required.", c.Path()).Send(c)
	}

	userID, _, err := h.passkeySvc.FinishAuthentication(c.Context(), sessionToken, c.Body())
	if err != nil {
		switch {
		case errors.Is(err, passkey.ErrSessionExpired):
			return apierror.InvalidToken("Authentication session expired. Please start over.", c.Path()).Send(c)
		case errors.Is(err, passkey.ErrInvalidResponse):
			return apierror.InvalidRequest("Invalid WebAuthn response.", c.Path()).Send(c)
		default:
			return apierror.Unauthorized("Passkey authentication failed.", c.Path()).Send(c)
		}
	}

	u, err := h.userSvc.GetByID(c.Context(), userID)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	if !u.IsEnabled {
		return apierror.AccountDisabled(c.Path()).Send(c)
	}

	// Passkey is the first factor; TOTP (if configured) is required as the second.
	if totpSecret, totpErr := h.totpSvc.Get(c.Context(), userID); totpErr == nil && totpSecret.IsSetup() {
		return issueMFAToken(c, h.cache, userID, "Passkey", clientIP(c), c.Get("User-Agent"), []string{"totp"})
	}

	ip := clientIP(c)
	sess, rawToken, err := h.sessionSvc.Create(c.Context(), u.ID(), "Passkey", ip, c.Get("User-Agent"), []string{session.AMRWebAuthn})
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	enrichSessionAsync(h.sessionSvc, u.ID(), sess.ID(), ip)

	setSessionCookies(c, h.cfg, rawToken)

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"user_id":    u.ID(),
		"email":      u.Email,
		"first_name": u.FirstName,
		"last_name":  u.LastName,
		"session_id": sess.ID(),
	})
}
