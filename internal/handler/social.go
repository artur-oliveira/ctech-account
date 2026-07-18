package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/internal/apierror"
	"gopkg.aoctech.app/account/internal/cache"
	"gopkg.aoctech.app/account/internal/config"
	"gopkg.aoctech.app/account/internal/crypto"
	"gopkg.aoctech.app/account/internal/domain/audit"
	"gopkg.aoctech.app/account/internal/domain/session"
	"gopkg.aoctech.app/account/internal/domain/user"
	"gopkg.aoctech.app/account/internal/legal"
)

const googleStateTTL = 10 * time.Minute

type SocialHandler struct {
	userSvc    *user.Service
	sessionSvc *session.Service
	cache      *cache.Client
	cfg        *config.Config
	audit      *audit.Service
}

func NewSocialHandler(userSvc *user.Service, sessionSvc *session.Service, c *cache.Client, cfg *config.Config, auditSvc *audit.Service) *SocialHandler {
	return &SocialHandler{userSvc: userSvc, sessionSvc: sessionSvc, cache: c, cfg: cfg, audit: auditSvc}
}

func (h *SocialHandler) Register(v1 fiber.Router) {
	auth := v1.Group("/auth")
	auth.Get("/google", h.googleRedirect)
	auth.Get("/google/callback", h.googleCallback)
	auth.Post("/accept-terms", h.acceptTerms)
}

func (h *SocialHandler) googleRedirect(c fiber.Ctx) error {
	if h.cfg.GoogleClientID == "" {
		return apierror.ServerError(c.Path()).Send(c)
	}
	if h.cache == nil || !h.cache.Enabled() {
		return apierror.ServiceUnavailable("Google login is temporarily unavailable.", c.Path()).Send(c)
	}

	continueURL := c.Query("continue", h.cfg.AppURL)
	if !h.isAllowedContinueURL(continueURL) {
		continueURL = h.cfg.AppURL
	}

	rawState, stateHash, err := crypto.GenerateMFAToken()
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	_ = h.cache.Set(c.Context(), "gs:"+stateHash, continueURL, googleStateTTL)

	redirectURI := h.cfg.BaseURL + "/v1.0/auth/google/callback"
	params := url.Values{
		"client_id":     {h.cfg.GoogleClientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"scope":         {"openid email profile"},
		"state":         {rawState},
		"access_type":   {"offline"},
		"prompt":        {"select_account"},
	}
	return c.Redirect().Status(fiber.StatusFound).To("https://accounts.google.com/o/oauth2/v2/auth?" + params.Encode())
}

func (h *SocialHandler) googleCallback(c fiber.Ctx) error {
	if errParam := c.Query("error"); errParam != "" {
		return c.Redirect().Status(fiber.StatusFound).To(h.cfg.AppURL + "/login?error=google_denied")
	}

	code := c.Query("code")
	rawState := c.Query("state")
	if code == "" || rawState == "" {
		return apierror.InvalidRequest("Missing code or state.", c.Path()).Send(c)
	}

	if h.cache == nil || !h.cache.Enabled() {
		return apierror.ServiceUnavailable("Google login is temporarily unavailable.", c.Path()).Send(c)
	}

	stateHash := crypto.HashToken(rawState)
	var continueURL string
	if err := h.cache.Get(c.Context(), "gs:"+stateHash, &continueURL); err != nil {
		return apierror.InvalidToken("OAuth state is invalid or has expired.", c.Path()).Send(c)
	}
	_ = h.cache.Delete(c.Context(), "gs:"+stateHash)

	googleProfile, err := h.exchangeGoogleCode(code)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	if !googleProfile.EmailVerified {
		return c.Redirect().Status(fiber.StatusFound).To(h.cfg.AppURL + "/login?error=google_email_unverified")
	}

	// Resolve relative continueURLs up front so every return path below
	// uses a final URL. API paths (/v1.0/...) belong on BaseURL;
	// all other paths (frontend routes) belong on AppURL.
	if strings.HasPrefix(continueURL, "/") {
		if strings.HasPrefix(continueURL, "/v1.0/") {
			continueURL = h.cfg.BaseURL + continueURL
		} else {
			continueURL = h.cfg.AppURL + continueURL
		}
	}

	// Already-authenticated user linking Google to their existing account:
	// the SSO session proves ownership of the password account — exactly
	// what an anonymous callback cannot show — so binding here is the
	// only safe merge path. An anonymous Google login still refuses to
	// merge into a password account.
	if cookieValue := c.Cookies(sessionCookieName); cookieValue != "" {
		if sess, vErr := h.sessionSvc.ValidateToken(c.Context(), cookieValue); vErr == nil {
			if lErr := h.userSvc.LinkGoogle(c.Context(), sess.UserID(), googleProfile.Sub, googleProfile.Email); lErr != nil {
				if errors.Is(lErr, user.ErrGoogleEmailConflict) {
					return c.Redirect().Status(fiber.StatusFound).To(h.cfg.AppURL + "/login?error=google_link_conflict")
				}
				return apierror.ServerError(c.Path()).Send(c)
			}
			recordAudit(c, h.audit, sess.UserID(), audit.EventSocialLinked, nil)
			return c.Redirect().Status(fiber.StatusFound).To(continueURL)
		}
	}

	u, created, err := h.userSvc.FindOrCreateByGoogle(
		c.Context(),
		googleProfile.Sub,
		googleProfile.Email,
		googleProfile.GivenName,
		googleProfile.FamilyName,
		googleProfile.Picture,
	)
	if err != nil {
		// A Google identity resolving to a password account is refused, not
		// merged — sending it to login keeps the attacker out instead of
		// dropping them into the victim's session.
		if errors.Is(err, user.ErrGoogleEmailConflict) {
			return c.Redirect().Status(fiber.StatusFound).To(h.cfg.AppURL + "/login?error=google_account_exists")
		}
		return apierror.ServerError(c.Path()).Send(c)
	}

	// A brand-new Google account never saw the register form's checkbox — gate
	// it behind an explicit accept-terms interstitial before any session exists.
	if created {
		return h.redirectToAcceptTerms(c, u.ID(), continueURL)
	}

	if err := h.issueSessionFromSocial(c, u); err != nil {
		return err
	}
	return c.Redirect().Status(fiber.StatusFound).To(continueURL)
}

// redirectToAcceptTerms stores a short-lived token capturing the in-flight
// login (device/IP/UA/continueURL) and sends the browser to the ui's
// accept-terms interstitial. No session exists yet at this point, so the
// interstitial authenticates with the token alone.
func (h *SocialHandler) redirectToAcceptTerms(c fiber.Ctx, userID, continueURL string) error {
	payload := termsTokenPayload{
		UserID:      userID,
		DeviceName:  parseDeviceName(c.Get("User-Agent")),
		IP:          clientIP(c),
		UserAgent:   c.Get("User-Agent"),
		ContinueURL: continueURL,
	}
	// A brand-new account has accepted nothing, so both documents are pending.
	location, err := mintAcceptTermsURL(c, h.cache, h.cfg.AppURL, payload, legal.PendingFor("", ""))
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	return c.Redirect().Status(fiber.StatusFound).To(location)
}

type acceptTermsTokenRequest struct {
	Token         string `json:"token"          validate:"required"`
	AcceptToS     bool   `json:"accept_tos"`
	AcceptPrivacy bool   `json:"accept_privacy"`
}

// acceptTerms serves the token-authenticated interstitial, which has two
// callers: a Google sign-up suspended by redirectToAcceptTerms, and an existing
// account re-gated by a version bump at /authorize. The first has no session yet
// and gets one here; the second already holds one and must keep it.
func (h *SocialHandler) acceptTerms(c fiber.Ctx) error {
	var req acceptTermsTokenRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}

	if h.cache == nil || !h.cache.Enabled() {
		return apierror.ServerError(c.Path()).Send(c)
	}

	hashHex := crypto.HashToken(req.Token)
	var payload termsTokenPayload
	// GetDel consumes the token atomically (single use, replay-safe).
	if err := h.cache.GetDel(c.Context(), termsTokenCachePrefix+hashHex, &payload); err != nil {
		return apierror.InvalidToken("Terms acceptance token is invalid or has expired.", c.Path()).Send(c)
	}

	u, err := h.userSvc.GetByID(c.Context(), payload.UserID)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	method := acceptMethodGoogle
	if payload.Reaccept {
		method = acceptMethodReaccept
	}
	if err := acceptPendingTerms(c, h.userSvc, h.audit, u, req.AcceptToS, req.AcceptPrivacy, method); err != nil {
		return err
	}

	// A re-acceptance already has a session — issuing another one here would
	// duplicate it for the same device. Only the suspended sign-up needs one.
	if !payload.Reaccept {
		sess, rawToken, sErr := h.sessionSvc.Create(c.Context(), u.ID(), payload.DeviceName, payload.IP, payload.UserAgent, []string{session.AMRGoogle})
		if sErr != nil {
			return apierror.ServerError(c.Path()).Send(c)
		}
		enrichSessionAsync(h.sessionSvc, u.ID(), sess.ID(), payload.IP)
		recordAudit(c, h.audit, u.ID(), audit.EventLoginSuccess, map[string]string{"method": acceptMethodGoogle, "session_id": sess.ID()})

		setSessionCookies(c, h.cfg, rawToken)
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{"redirect": payload.ContinueURL})
}

// isAllowedContinueURL returns true when continueURL is safe to redirect to after social login.
// Relative paths (starting with "/") are always safe — they get prepended with AppURL.
// Absolute URLs must match AppURL or one of AllowedOrigins.
func (h *SocialHandler) isAllowedContinueURL(u string) bool {
	if strings.HasPrefix(u, "/") {
		// Reject protocol-relative ("//evil.com") and backslash ("/\evil.com") forms:
		// browsers resolve both to an external origin, making this an open redirect.
		return !strings.HasPrefix(u, "//") && !strings.HasPrefix(u, `/\`)
	}
	// BaseURL (backend API) is included so that OAuth authorize redirects from other clients
	// (e.g. continue=/v1.0/authorize?client_id=dfe&...) survive the validation.
	allowed := append([]string{h.cfg.AppURL, h.cfg.BaseURL}, h.cfg.AllowedOrigins...)
	for _, origin := range allowed {
		if u == origin || strings.HasPrefix(u, origin+"/") {
			return true
		}
	}
	return false
}

// googleHTTPClient bounds the Google token/userinfo calls so a slow or
// unresponsive upstream can't hang the callback handler.
var googleHTTPClient = &http.Client{Timeout: 10 * time.Second}

type googleUserInfo struct {
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	GivenName     string `json:"given_name"`
	FamilyName    string `json:"family_name"`
	Picture       string `json:"picture"`
}

func (h *SocialHandler) exchangeGoogleCode(code string) (*googleUserInfo, error) {
	redirectURI := h.cfg.BaseURL + "/v1.0/auth/google/callback"

	tokenBody := url.Values{
		"code":          {code},
		"client_id":     {h.cfg.GoogleClientID},
		"client_secret": {h.cfg.GoogleClientSecret},
		"redirect_uri":  {redirectURI},
		"grant_type":    {"authorization_code"},
	}

	resp, err := googleHTTPClient.PostForm("https://oauth2.googleapis.com/token", tokenBody)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(resp.Body)

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &tokenResp); err != nil || tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("token parse error: %s", tokenResp.Error)
	}

	req, _ := http.NewRequest(http.MethodGet, "https://www.googleapis.com/oauth2/v3/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)
	uiResp, err := googleHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("userinfo fetch: %w", err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(uiResp.Body)

	var profile googleUserInfo
	if err := json.NewDecoder(uiResp.Body).Decode(&profile); err != nil {
		return nil, fmt.Errorf("userinfo decode: %w", err)
	}
	if profile.Email == "" {
		return nil, fmt.Errorf("google did not return email")
	}
	return &profile, nil
}

func (h *SocialHandler) issueSessionFromSocial(c fiber.Ctx, u *user.User) error {
	deviceName := parseDeviceName(c.Get("User-Agent"))
	ip := clientIP(c)
	sess, rawToken, err := h.sessionSvc.Create(c.Context(), u.ID(), deviceName, ip, c.Get("User-Agent"), []string{session.AMRGoogle})
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	enrichSessionAsync(h.sessionSvc, u.ID(), sess.ID(), ip)
	recordAudit(c, h.audit, u.ID(), audit.EventLoginSuccess, map[string]string{"method": "google", "session_id": sess.ID()})

	// ctech_rt is set alongside ctech_session so the /token refresh_token grant can
	// rotate the session without JS access to the HttpOnly ctech_session cookie.
	setSessionCookies(c, h.cfg, rawToken)
	return nil
}
