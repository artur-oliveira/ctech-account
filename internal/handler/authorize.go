package handler

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/internal/apierror"
	"gopkg.aoctech.app/account/internal/cache"
	"gopkg.aoctech.app/account/internal/crypto"
	"gopkg.aoctech.app/account/internal/domain/audit"
	oauthclient "gopkg.aoctech.app/account/internal/domain/oauth/client"
	authcode "gopkg.aoctech.app/account/internal/domain/oauth/code"
	"gopkg.aoctech.app/account/internal/domain/oauth/consent"
	"gopkg.aoctech.app/account/internal/domain/session"
	"gopkg.aoctech.app/account/internal/domain/user"
	"gopkg.aoctech.app/account/internal/legal"
)

type AuthorizeHandler struct {
	clientRepo   oauthclient.Repository
	codeRepo     *authcode.Repository
	sessionSvc   *session.Service
	consentSvc   *consent.Service
	userSvc      *user.Service
	cache        *cache.Client
	appURL       string // frontend origin — login, consent and accept-terms pages
	apiBaseURL   string // this API's origin — target for post-consent re-authorize
	cookieDomain string
	audit        *audit.Service
}

func NewAuthorizeHandler(
	clientRepo oauthclient.Repository,
	codeRepo *authcode.Repository,
	sessionSvc *session.Service,
	consentSvc *consent.Service,
	userSvc *user.Service,
	c *cache.Client,
	appURL string,
	apiBaseURL string,
	cookieDomain string,
	auditSvc *audit.Service,
) *AuthorizeHandler {
	return &AuthorizeHandler{
		clientRepo:   clientRepo,
		codeRepo:     codeRepo,
		sessionSvc:   sessionSvc,
		consentSvc:   consentSvc,
		userSvc:      userSvc,
		cache:        c,
		appURL:       appURL,
		apiBaseURL:   apiBaseURL,
		cookieDomain: cookieDomain,
		audit:        auditSvc,
	}
}

func (h *AuthorizeHandler) Register(v1 fiber.Router) {
	v1.Get("/authorize", h.Authorize)
	v1.Post("/authorize/consent", h.ConsentDecision)
}

func (h *AuthorizeHandler) Authorize(c fiber.Ctx) error {
	clientID := c.Query("client_id")
	redirectURI := c.Query("redirect_uri")
	responseType := c.Query("response_type")
	scopeParam := c.Query("scope")
	state := c.Query("state")
	codeChallenge := c.Query("code_challenge")
	codeChallengeMethod := c.Query("code_challenge_method")
	nonce := c.Query("nonce")

	if clientID == "" || redirectURI == "" {
		return apierror.InvalidRequest("client_id and redirect_uri are required.", c.Path()).Send(c)
	}

	// Resolve the client and confirm the redirect_uri is registered BEFORE any
	// error is delivered via redirect. Redirecting to an unvalidated redirect_uri
	// would turn this endpoint into an open redirect (phishing vector).
	oauthClient, err := h.clientRepo.GetByID(c.Context(), clientID)
	if err != nil {
		if errors.Is(err, oauthclient.ErrNotFound) {
			return apierror.InvalidClient("Unknown client_id.", c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}

	if !oauthClient.IsRedirectURIAllowed(redirectURI) {
		return apierror.InvalidRequest("The redirect_uri is not registered for this client.", c.Path()).Send(c)
	}

	// The redirect_uri is now trusted — OAuth errors may be delivered to it.
	if responseType != "code" {
		return h.redirectError(c, redirectURI, state, "unsupported_response_type", "only response_type=code is supported")
	}
	if codeChallenge != "" && codeChallengeMethod != "S256" {
		return h.redirectError(c, redirectURI, state, "invalid_request", "only S256 code_challenge_method is supported")
	}

	requestedScopes := strings.Fields(scopeParam)
	allowedScopes := oauthClient.FilterScopes(requestedScopes)
	if len(allowedScopes) == 0 {
		return h.redirectError(c, redirectURI, state, "invalid_scope", "no valid scopes requested for this client")
	}

	if oauthClient.IsPublic() && codeChallenge == "" {
		return h.redirectError(c, redirectURI, state, "invalid_request", "PKCE code_challenge is required for public clients")
	}

	// Only the SSO session cookie authenticates /authorize. ctech_rt is a
	// per-client refresh token for the /token grant — never an SSO credential.
	cookieValue := c.Cookies(sessionCookieName)
	if cookieValue == "" {
		return c.Redirect().To(h.loginURL(c.OriginalURL()))
	}

	sess, err := h.sessionSvc.ValidateToken(c.Context(), cookieValue)
	if err != nil {
		c.Cookie(&fiber.Cookie{Name: sessionCookieName, Value: "", MaxAge: clearCookieMaxAge, Path: "/", Domain: h.cookieDomain})
		return c.Redirect().To(h.loginURL(c.OriginalURL()))
	}

	// OIDC max_age: force an interactive re-login when the session's last
	// active authentication is older than the caller demands (e.g. a
	// downstream app's step-up flow passing max_age=0 to guarantee a fresh
	// MFA proof). A valid SSO cookie alone is not enough once this is set —
	// this is what lets a client force real re-authentication instead of a
	// silent SSO bounce. Self-resolving: Session.AuthTime is reset to "now" by
	// every fresh login (session/service.go Create), so the very next pass
	// through here — right after that login — always satisfies its own
	// max_age and never loops.
	if maxAge, ok := parseMaxAge(c.Query("max_age")); ok {
		if time.Now().UTC().Unix()-sess.AuthTime > maxAge {
			return c.Redirect().To(h.loginURL(c.OriginalURL()))
		}
	}

	// Terms gate: a ToS/Privacy version bump re-gates every account here, so every
	// product behind this IdP inherits the block. The user holds an SSO session but
	// not necessarily an access token (the code exchange hasn't happened yet), so
	// the interstitial is authenticated by a single-use token, not by bearer.
	u, uErr := h.userSvc.GetByID(c.Context(), sess.UserID())
	if uErr != nil {
		return h.redirectError(c, redirectURI, state, "server_error", "failed to load user")
	}
	if pending := legal.PendingFor(u.TOSVersion, u.PrivacyVersion); pending.Any() {
		payload := termsTokenPayload{
			UserID: sess.UserID(),
			// Absolute on THIS API's origin: the interstitial lives on the frontend
			// and sends the browser here, so a bare "/v1.0/authorize?..." would
			// resolve against the frontend's origin instead.
			ContinueURL: h.apiBaseURL + c.OriginalURL(),
			Reaccept:    true,
		}
		location, mErr := mintAcceptTermsURL(c, h.cache, h.appURL, payload, pending)
		if mErr != nil {
			return h.redirectError(c, redirectURI, state, "server_error", "failed to start terms acceptance")
		}
		return c.Redirect().Status(fiber.StatusFound).To(location)
	}

	// Consent gate: third-party clients need an explicit user grant covering the
	// requested scopes. First-party clients (platform apps) skip the screen.
	if !oauthClient.FirstParty {
		granted, cErr := h.consentSvc.HasGrant(c.Context(), sess.UserID(), clientID, allowedScopes)
		if cErr != nil {
			return h.redirectError(c, redirectURI, state, "server_error", "failed to check consent")
		}
		if !granted {
			return c.Redirect().Status(fiber.StatusFound).To(h.consentURL(c.OriginalURL(), oauthClient.Name, allowedScopes))
		}
	}

	rawCode, codeHash, err := crypto.GenerateCode()
	if err != nil {
		return h.redirectError(c, redirectURI, state, "server_error", "failed to generate authorization code")
	}

	ac := &authcode.AuthCode{
		UserID:              sess.UserID(),
		SessionID:           sess.ID(),
		ClientID:            clientID,
		RedirectURI:         redirectURI,
		Scopes:              allowedScopes,
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: codeChallengeMethod,
		MFAVerified:         false,
		Nonce:               nonce,
		CreatedAt:           time.Now().UTC().Format(time.RFC3339),
	}

	if err := h.codeRepo.Store(c.Context(), codeHash, ac); err != nil {
		return h.redirectError(c, redirectURI, state, "server_error", "failed to store authorization code")
	}

	location := fmt.Sprintf("%s?code=%s", redirectURI, url.QueryEscape(rawCode))
	if state != "" {
		location += "&state=" + url.QueryEscape(state)
	}
	return c.Redirect().Status(fiber.StatusFound).To(location)
}

// parseMaxAge reads the OIDC max_age query param (seconds). Missing or
// unparseable is "not requested" — the caller skips the freshness check
// entirely, preserving today's behavior for every existing client.
func parseMaxAge(raw string) (int64, bool) {
	if raw == "" {
		return 0, false
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

func (h *AuthorizeHandler) loginURL(continueURL string) string {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(continueURL))
	return h.appURL + "/login?continue=" + encoded
}

// consentURL builds the frontend consent page URL. req carries the original
// /v1.0/authorize path+query (base64url) so the decision endpoint can
// re-validate everything server-side — the display params are cosmetic only.
func (h *AuthorizeHandler) consentURL(originalURL, clientName string, scopes []string) string {
	q := url.Values{}
	q.Set("req", base64.RawURLEncoding.EncodeToString([]byte(originalURL)))
	q.Set("client_name", clientName)
	q.Set("scope", strings.Join(scopes, " "))
	return h.appURL + "/consent?" + q.Encode()
}

// authorizeRequestPath is the only path a consent `req` value may point to.
const authorizeRequestPath = "/v1.0/authorize"

// maxConsentReqLen caps the decoded authorize URL to reject absurd payloads.
const maxConsentReqLen = 4096

type consentDecisionRequest struct {
	Req      string `json:"req"      validate:"required,max=8192"`
	Approved bool   `json:"approved"`
}

// ConsentDecision records the user's approve/deny choice for a pending
// authorization request and tells the frontend where to navigate next. It is
// authenticated by the SSO session cookie, exactly like GET /authorize.
func (h *AuthorizeHandler) ConsentDecision(c fiber.Ctx) error {
	var req consentDecisionRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}

	cookieValue := c.Cookies(sessionCookieName)
	if cookieValue == "" {
		return apierror.Unauthorized("No active session.", c.Path()).Send(c)
	}
	sess, err := h.sessionSvc.ValidateToken(c.Context(), cookieValue)
	if err != nil {
		return apierror.Unauthorized("Session is invalid or has expired.", c.Path()).Send(c)
	}

	decoded, err := base64.RawURLEncoding.DecodeString(req.Req)
	if err != nil || len(decoded) > maxConsentReqLen {
		return apierror.InvalidRequest("Malformed consent request reference.", c.Path()).Send(c)
	}
	authorizeURL, err := url.Parse(string(decoded))
	if err != nil || authorizeURL.Path != authorizeRequestPath || authorizeURL.Host != "" {
		return apierror.InvalidRequest("Consent request must reference the authorize endpoint.", c.Path()).Send(c)
	}

	params := authorizeURL.Query()
	clientID := params.Get("client_id")
	redirectURI := params.Get("redirect_uri")
	state := params.Get("state")

	// Re-validate exactly as GET /authorize does — the frontend-supplied req is
	// untrusted, and redirecting to an unregistered URI would be an open redirect.
	oauthClient, err := h.clientRepo.GetByID(c.Context(), clientID)
	if err != nil {
		if errors.Is(err, oauthclient.ErrNotFound) {
			return apierror.InvalidClient("Unknown client_id.", c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}
	if !oauthClient.IsRedirectURIAllowed(redirectURI) {
		return apierror.InvalidRequest("The redirect_uri is not registered for this client.", c.Path()).Send(c)
	}

	if !req.Approved {
		denyURL := fmt.Sprintf("%s?error=access_denied&error_description=%s",
			redirectURI, url.QueryEscape("the user denied the authorization request"))
		if state != "" {
			denyURL += "&state=" + url.QueryEscape(state)
		}
		return c.JSON(fiber.Map{"redirect_to": denyURL})
	}

	allowedScopes := oauthClient.FilterScopes(strings.Fields(params.Get("scope")))
	if len(allowedScopes) == 0 {
		return apierror.InvalidRequest("No valid scopes requested for this client.", c.Path()).Send(c)
	}
	if err := h.consentSvc.Grant(c.Context(), sess.UserID(), clientID, allowedScopes); err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	recordAudit(c, h.audit, sess.UserID(), audit.EventConsentGranted, map[string]string{"client_id": clientID})

	// Send the browser back through /authorize — the grant now covers the scopes,
	// so it will issue the code and redirect to the client.
	return c.JSON(fiber.Map{"redirect_to": h.apiBaseURL + authorizeURL.String()})
}

// redirectError redirects with OAuth error query params when the redirect_uri is already trusted.
func (h *AuthorizeHandler) redirectError(c fiber.Ctx, redirectURI, state, code, description string) error {
	location := fmt.Sprintf("%s?error=%s&error_description=%s",
		redirectURI,
		url.QueryEscape(code),
		url.QueryEscape(description),
	)
	if state != "" {
		location += "&state=" + url.QueryEscape(state)
	}
	return c.Redirect().Status(fiber.StatusFound).To(location)
}
