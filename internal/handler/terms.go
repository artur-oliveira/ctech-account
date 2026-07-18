package handler

import (
	"errors"
	"net/url"
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/internal/apierror"
	"gopkg.aoctech.app/account/internal/cache"
	"gopkg.aoctech.app/account/internal/crypto"
	"gopkg.aoctech.app/account/internal/domain/audit"
	"gopkg.aoctech.app/account/internal/domain/user"
	"gopkg.aoctech.app/account/internal/legal"
	"gopkg.aoctech.app/account/internal/middleware"
)

const (
	acceptTermsTokenTTL   = 10 * time.Minute
	termsTokenCachePrefix = "terms_token:"
	acceptTermsPath       = "/accept-terms"

	acceptMethodGoogle   = "google"
	acceptMethodReaccept = "reaccept"
	acceptMethodInApp    = "in-app"

	acceptTermsTokenParam   = "token"
	acceptTermsToSParam     = "tos"
	acceptTermsPrivacyParam = "privacy"
	acceptTermsParamOn      = "1"
)

// termsTokenPayload is stored in Valkey while a user sits on the accept-terms
// interstitial. It carries everything issueSessionFromSocial needs, captured
// from the ORIGINAL request — the eventual POST /auth/accept-terms call comes
// from a different one.
type termsTokenPayload struct {
	UserID      string `json:"user_id"`
	DeviceName  string `json:"device_name"`
	IP          string `json:"ip"`
	UserAgent   string `json:"user_agent"`
	ContinueURL string `json:"continue_url"`
	// Reaccept marks a token minted for a user who ALREADY holds a session (a
	// ToS/Privacy version bump caught at /authorize), as opposed to a brand-new
	// Google account still waiting for its first one. Only the latter gets a
	// session issued on acceptance.
	Reaccept bool `json:"reaccept"`
}

// mintAcceptTermsURL stores a single-use token describing the in-flight request
// and returns the frontend interstitial URL carrying it. The tos/privacy query
// params are cosmetic — they only pick which checkboxes the page renders;
// acceptPendingTerms recomputes the real pending set server-side before stamping.
func mintAcceptTermsURL(c fiber.Ctx, cch *cache.Client, appURL string, payload termsTokenPayload, pending legal.Pending) (string, error) {
	rawToken, hashHex, err := crypto.GenerateMFAToken()
	if err != nil {
		return "", err
	}
	if err := cch.Set(c.Context(), termsTokenCachePrefix+hashHex, payload, acceptTermsTokenTTL); err != nil {
		return "", err
	}

	params := url.Values{acceptTermsTokenParam: {rawToken}}
	if pending.ToS {
		params.Set(acceptTermsToSParam, acceptTermsParamOn)
	}
	if pending.Privacy {
		params.Set(acceptTermsPrivacyParam, acceptTermsParamOn)
	}
	return appURL + acceptTermsPath + "?" + params.Encode(), nil
}

// acceptPendingTerms stamps the documents the user actually owes. The client's
// flags are a confirmation, never the source of truth: the pending set is
// recomputed here, and a pending document left unconfirmed is a 422.
func acceptPendingTerms(c fiber.Ctx, userSvc *user.Service, auditSvc *audit.Service, u *user.User, acceptToS, acceptPrivacy bool, method string) error {
	pending := legal.PendingFor(u.TOSVersion, u.PrivacyVersion)
	if !pending.Any() {
		return nil
	}

	if (pending.ToS && !acceptToS) || (pending.Privacy && !acceptPrivacy) {
		return apierror.ValidationFailed("You must accept the updated Terms of Service and Privacy Policy to continue.", c.Path())
	}

	if err := userSvc.AcceptTerms(c.Context(), u.ID(), pending.ToS, pending.Privacy); err != nil {
		return apierror.ServerError(c.Path())
	}

	meta := map[string]string{"method": method}
	if pending.ToS {
		meta["tos_version"] = legal.CurrentToSVersion
	}
	if pending.Privacy {
		meta["privacy_version"] = legal.CurrentPrivacyVersion
	}
	recordAudit(c, auditSvc, u.ID(), audit.EventTermsAccepted, meta)

	return nil
}

// TermsHandler serves the in-app re-acceptance gate. A user whose access token
// is still being refreshed never passes through /authorize again, so the SPA
// needs a bearer-authenticated path of its own to clear a version bump.
type TermsHandler struct {
	userSvc *user.Service
	audit   *audit.Service
}

func NewTermsHandler(userSvc *user.Service, auditSvc *audit.Service) *TermsHandler {
	return &TermsHandler{userSvc: userSvc, audit: auditSvc}
}

func (h *TermsHandler) Register(account fiber.Router) {
	account.Post("/terms/accept", h.accept)
}

type acceptTermsRequest struct {
	AcceptToS     bool `json:"accept_tos"`
	AcceptPrivacy bool `json:"accept_privacy"`
}

func (h *TermsHandler) accept(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	var req acceptTermsRequest
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

	if err := acceptPendingTerms(c, h.userSvc, h.audit, u, req.AcceptToS, req.AcceptPrivacy, acceptMethodInApp); err != nil {
		return err
	}

	u, err = h.userSvc.GetByID(c.Context(), userID)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	return c.JSON(fiber.Map{"terms_pending": legal.PendingFor(u.TOSVersion, u.PrivacyVersion)})
}
