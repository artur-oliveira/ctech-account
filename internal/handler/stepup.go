package handler

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/internal/apierror"
	"gopkg.aoctech.app/account/internal/cache"
	"gopkg.aoctech.app/account/internal/domain/audit"
	"gopkg.aoctech.app/account/internal/domain/mfa/passkey"
	"gopkg.aoctech.app/account/internal/domain/session"
	"gopkg.aoctech.app/account/internal/middleware"
)

const (
	stepUpMethodTOTP    = "totp"
	stepUpMethodPasskey = "passkey"
	// stepUpRateLimitPrefix namespaces the step-up brute-force counter.
	stepUpRateLimitPrefix = "stepup"
)

// StepUpHandler serves the step-up (re-authentication) challenge. A successful
// challenge stamps a fresh MFA proof on the session; the client then
// silent-refreshes so its next access token passes RequireRecentMFA.
type StepUpHandler struct {
	sessionSvc *session.Service
	totpSvc    TOTPService
	passkeySvc *passkey.Service
	cache      *cache.Client
	audit      *audit.Service
}

func NewStepUpHandler(sessionSvc *session.Service, totpSvc TOTPService, passkeySvc *passkey.Service, valkeyCache *cache.Client, auditSvc *audit.Service) *StepUpHandler {
	return &StepUpHandler{sessionSvc: sessionSvc, totpSvc: totpSvc, passkeySvc: passkeySvc, cache: valkeyCache, audit: auditSvc}
}

// Register mounts the step-up routes. requireAuth is the shared RequireAuth
// middleware; selfOnly restricts these self-service endpoints to this
// service's own first-party frontend (middleware.RequireClientID); challenges
// are rate-limited like login (5 failures / 15 min).
func (h *StepUpHandler) Register(v1 fiber.Router, requireAuth, selfOnly fiber.Handler) {
	rl := middleware.RateLimit(middleware.RateLimitConfig{
		Cache:             h.cache,
		Prefix:            stepUpRateLimitPrefix,
		Max:               middleware.FailedLoginMax,
		Window:            middleware.FailedLoginWindow,
		KeyFunc:           middleware.GetUserID,
		CountOnlyFailures: true,
	})
	auth := v1.Group("/auth")
	auth.Post("/step-up", requireAuth, selfOnly, rl, h.challenge)
	auth.Post("/step-up/passkeys/begin", requireAuth, selfOnly, rl, h.passkeyBegin)
	auth.Post("/step-up/passkeys/complete", requireAuth, selfOnly, rl, h.passkeyComplete)
}

type stepUpRequest struct {
	Method string `json:"method" validate:"required,oneof=totp"`
	Code   string `json:"code"   validate:"required,len=6,numeric"`
}

func (h *StepUpHandler) challenge(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)
	sessionID := middleware.GetSessionID(c)

	var req stepUpRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}

	if !h.userHasMFA(c, userID) {
		return apierror.MFAEnrollmentRequired(c.Path()).Send(c)
	}

	ok, err := h.totpSvc.Validate(c.Context(), userID, req.Code)
	if err != nil || !ok {
		recordAudit(c, h.audit, userID, audit.EventStepUpFailed, map[string]string{"method": stepUpMethodTOTP})
		return apierror.InvalidCredentials(c.Path()).Send(c)
	}

	return h.finish(c, userID, sessionID, session.AMRTOTP, stepUpMethodTOTP)
}

// passkeyBegin issues a WebAuthn assertion challenge for the authenticated
// user. The challenge lives in the passkey service's cache (5-min TTL).
func (h *StepUpHandler) passkeyBegin(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	optionsJSON, sessionToken, err := h.passkeySvc.BeginUserAuthentication(c.Context(), userID)
	if err != nil {
		switch {
		case errors.Is(err, passkey.ErrCacheRequired):
			return apierror.ServiceUnavailable("Passkey authentication is temporarily unavailable.", c.Path()).Send(c)
		case errors.Is(err, passkey.ErrNoCredentials):
			return apierror.MFAEnrollmentRequired(c.Path()).Send(c)
		default:
			return apierror.ServerError(c.Path()).Send(c)
		}
	}

	return c.JSON(fiber.Map{
		"session_token": sessionToken,
		"options":       string(optionsJSON),
	})
}

// passkeyComplete validates the assertion. Query param: session_token.
// Body: raw WebAuthn assertion JSON.
func (h *StepUpHandler) passkeyComplete(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)
	sessionID := middleware.GetSessionID(c)
	sessionToken := c.Query("session_token")

	if sessionToken == "" {
		return apierror.InvalidRequest("session_token query param is required.", c.Path()).Send(c)
	}
	if len(c.Body()) == 0 {
		return apierror.InvalidRequest("Request body with WebAuthn assertion is required.", c.Path()).Send(c)
	}

	if err := h.passkeySvc.FinishUserAuthentication(c.Context(), userID, sessionToken, c.Body()); err != nil {
		recordAudit(c, h.audit, userID, audit.EventStepUpFailed, map[string]string{"method": stepUpMethodPasskey})
		switch {
		case errors.Is(err, passkey.ErrSessionExpired):
			return apierror.InvalidToken("Passkey session expired. Please try again.", c.Path()).Send(c)
		case errors.Is(err, passkey.ErrInvalidResponse):
			return apierror.InvalidRequest("Invalid passkey response.", c.Path()).Send(c)
		default:
			return apierror.Unauthorized("Passkey authentication failed.", c.Path()).Send(c)
		}
	}

	return h.finish(c, userID, sessionID, session.AMRWebAuthn, stepUpMethodPasskey)
}

// finish stamps the MFA proof on the session and audits the challenge.
func (h *StepUpHandler) finish(c fiber.Ctx, userID, sessionID, amrMethod, auditMethod string) error {
	if err := h.sessionSvc.RecordMFA(c.Context(), userID, sessionID, amrMethod); err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	recordAudit(c, h.audit, userID, audit.EventStepUpSuccess, map[string]string{"method": auditMethod})
	return c.SendStatus(fiber.StatusNoContent)
}

// userHasMFA reports whether the user has TOTP or at least one passkey enrolled.
func (h *StepUpHandler) userHasMFA(c fiber.Ctx, userID string) bool {
	if secret, err := h.totpSvc.Get(c.Context(), userID); err == nil && secret.IsSetup() {
		return true
	}
	has, err := h.passkeySvc.HasPasskeys(c.Context(), userID)
	return err == nil && has
}
