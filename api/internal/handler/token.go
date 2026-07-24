package handler

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"slices"
	"strings"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/config"
	"gopkg.aoctech.app/account/api/internal/crypto"
	"gopkg.aoctech.app/account/api/internal/domain/apikey"
	"gopkg.aoctech.app/account/api/internal/domain/audit"
	oauthclient "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	authcode "gopkg.aoctech.app/account/api/internal/domain/oauth/code"
	"gopkg.aoctech.app/account/api/internal/domain/session"
	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/middleware"
	"gopkg.aoctech.app/account/api/internal/scopes"
)

const refreshTokenCookieName = "ctech_rt"
const refreshTokenMaxAge = 90 * 24 * 60 * 60

// OAuth grant_type values accepted by POST /v1.0/token.
const (
	grantAuthorizationCode = "authorization_code"
	grantRefreshToken      = "refresh_token"
	// grantAPIKey exchanges a long-lived API key for a short-lived access token.
	// Resource servers (dfe) then validate only RS256 JWTs via JWKS — they never
	// see or store raw API keys.
	grantAPIKey = "api_key"
	// grantClientCredentials issues machine-to-machine tokens (RFC 6749 §4.4)
	// to confidential first-party clients (e.g. ctech-wallet → internal:kyc).
	grantClientCredentials = "client_credentials"
)

// accessTokenTTLSeconds is the lifetime of every issued access token; it is
// also the revocation window for exchanged API keys.
const accessTokenTTLSeconds = 900

// apiKeyClientID is the client_id claim stamped on API-key-derived tokens.
const apiKeyClientID = "api-key"

type TokenHandler struct {
	clientRepo oauthclient.Repository
	codeRepo   *authcode.Repository
	sessionSvc *session.Service
	userSvc    *user.Service
	apiKeySvc  *apikey.Service
	catalogSvc *scopes.CatalogService
	jwtSvc     *crypto.JWTService
	baseURL    string
	cfg        *config.Config
	audit      *audit.Service
}

func NewTokenHandler(
	clientRepo oauthclient.Repository,
	codeRepo *authcode.Repository,
	sessionSvc *session.Service,
	userSvc *user.Service,
	apiKeySvc *apikey.Service,
	catalogSvc *scopes.CatalogService,
	jwtSvc *crypto.JWTService,
	baseURL string,
	cfg *config.Config,
	auditSvc *audit.Service,
) *TokenHandler {
	return &TokenHandler{
		clientRepo: clientRepo,
		codeRepo:   codeRepo,
		sessionSvc: sessionSvc,
		userSvc:    userSvc,
		apiKeySvc:  apiKeySvc,
		catalogSvc: catalogSvc,
		jwtSvc:     jwtSvc,
		baseURL:    baseURL,
		cfg:        cfg,
		audit:      auditSvc,
	}
}

func (h *TokenHandler) Register(v1 fiber.Router) {
	v1.Post("/token", h.Token)
	v1.Post("/revoke", h.Revoke)
}

func (h *TokenHandler) Token(c fiber.Ctx) error {
	grantType := c.FormValue("grant_type")
	switch grantType {
	case grantAuthorizationCode:
		return h.authorizationCode(c)
	case grantRefreshToken:
		return h.refreshToken(c)
	case grantAPIKey:
		return h.apiKeyExchange(c)
	case grantClientCredentials:
		return h.clientCredentials(c)
	default:
		return apierror.UnsupportedGrantType(c.Path()).Send(c)
	}
}

// apiKeyExchange trades a raw API key for a short-lived access token carrying
// the key's scopes and the audiences of the services those scopes belong to.
func (h *TokenHandler) apiKeyExchange(c fiber.Ctx) error {
	rawKey := c.FormValue(grantAPIKey)
	if rawKey == "" {
		return apierror.InvalidRequest("api_key is required.", c.Path()).Send(c)
	}

	k, err := h.apiKeySvc.Authenticate(c.Context(), rawKey)
	if err != nil {
		return apierror.InvalidGrant("API key is invalid, expired, or revoked.", c.Path()).Send(c)
	}

	// aud = this IdP plus every service named by the key's scopes, so one token
	// works against accounts APIs and e.g. dfe without a second exchange.
	serviceAudiences, err := h.catalogSvc.AudiencesFor(c.Context(), k.Scopes)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	audience := append([]string{h.cfg.Audience}, serviceAudiences...)

	accessToken, err := h.jwtSvc.SignAccessToken(k.UserID(), k.ID(), apiKeyClientID, k.Scopes, h.baseURL, audience, 0, 0, nil, "")
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	return c.JSON(fiber.Map{
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_in":   accessTokenTTLSeconds,
		"scope":        strings.Join(k.Scopes, " "),
	})
}

// clientCredentials issues a machine-to-machine token (RFC 6749 §4.4).
// Restricted to confidential first-party clients: internal:* scopes gate
// service-to-service APIs, and third parties must never reach them.
func (h *TokenHandler) clientCredentials(c fiber.Ctx) error {
	clientID := c.FormValue("client_id")
	clientSecret := c.FormValue("client_secret")
	if clientID == "" || clientSecret == "" {
		return apierror.InvalidRequest("client_id and client_secret are required.", c.Path()).Send(c)
	}

	oauthClient, err := h.clientRepo.GetByID(c.Context(), clientID)
	if err != nil {
		if errors.Is(err, oauthclient.ErrNotFound) {
			return apierror.InvalidClient("Unknown client_id.", c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}

	if oauthClient.IsPublic() || !oauthClient.FirstParty {
		return apierror.UnauthorizedClient(c.Path()).Send(c)
	}
	ok, _ := crypto.VerifyPassword(clientSecret, oauthClient.ClientSecretHash)
	if !ok {
		return apierror.InvalidClient("Invalid client_secret.", c.Path()).Send(c)
	}

	requested := strings.Fields(c.FormValue("scope"))
	scp := oauthClient.FilterScopes(requested)
	if len(scp) == 0 {
		return apierror.InvalidScope(c.Path()).Send(c)
	}

	serviceAudiences, err := h.catalogSvc.AudiencesFor(c.Context(), scp)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	audience := append([]string{h.cfg.Audience}, serviceAudiences...)

	// sub = client_id, no session: internal middleware relies on the empty sid
	// to tell machine tokens from user tokens. No step-up claims, no kyc_level,
	// no refresh token.
	accessToken, err := h.jwtSvc.SignAccessToken(clientID, "", clientID, scp, h.baseURL, audience, 0, 0, nil, "")
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	return c.JSON(fiber.Map{
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_in":   accessTokenTTLSeconds,
		"scope":        strings.Join(scp, " "),
	})
}

func (h *TokenHandler) authorizationCode(c fiber.Ctx) error {
	code := c.FormValue("code")
	clientID := c.FormValue("client_id")
	redirectURI := c.FormValue("redirect_uri")
	codeVerifier := c.FormValue("code_verifier")

	if code == "" || clientID == "" || redirectURI == "" {
		return apierror.InvalidRequest("code, client_id and redirect_uri are required.", c.Path()).Send(c)
	}

	oauthClient, err := h.clientRepo.GetByID(c.Context(), clientID)
	if err != nil {
		if errors.Is(err, oauthclient.ErrNotFound) {
			return apierror.InvalidClient("Unknown client_id.", c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}

	if !oauthClient.IsPublic() {
		clientSecret := c.FormValue("client_secret")
		if clientSecret == "" {
			return apierror.InvalidClient("client_secret is required for confidential clients.", c.Path()).Send(c)
		}
		ok, _ := crypto.VerifyPassword(clientSecret, oauthClient.ClientSecretHash)
		if !ok {
			return apierror.InvalidClient("Invalid client_secret.", c.Path()).Send(c)
		}
	}

	codeHash := crypto.HashToken(code)
	// Consume the code atomically so concurrent requests can't both redeem it.
	ac, err := h.codeRepo.GetAndDelete(c.Context(), codeHash)
	if err != nil {
		if errors.Is(err, authcode.ErrNotFound) {
			return apierror.InvalidGrant("Authorization code not found or has expired.", c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}

	if ac.ClientID != clientID {
		return apierror.InvalidGrant("Authorization code was not issued to this client.", c.Path()).Send(c)
	}
	if ac.RedirectURI != redirectURI {
		return apierror.InvalidGrant("redirect_uri does not match the one used in the authorization request.", c.Path()).Send(c)
	}

	if ac.CodeChallenge != "" {
		if codeVerifier == "" {
			return apierror.InvalidGrant("code_verifier is required for PKCE.", c.Path()).Send(c)
		}
		if !verifyPKCE(codeVerifier, ac.CodeChallenge) {
			return apierror.InvalidGrant("code_verifier does not match the code_challenge.", c.Path()).Send(c)
		}
	}

	u, err := h.userSvc.GetByID(c.Context(), ac.UserID)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	sess, err := h.sessionSvc.Get(c.Context(), ac.UserID, ac.SessionID)
	if err != nil {
		// Only reachable with a real, just-issued single-use code — never
		// guessable — so this is a benign "user logged out concurrently" race,
		// not brute-force traffic. Don't burn the shared /token rate-limit budget.
		middleware.SkipRateLimitCount(c)
		return apierror.InvalidGrant("Session no longer exists.", c.Path()).Send(c)
	}

	accessToken, err := h.jwtSvc.SignAccessToken(ac.UserID, ac.SessionID, clientID, ac.Scopes, h.baseURL, accessTokenAudience(h.cfg.Audience, oauthClient), sess.AuthTime, sess.LastMFAAt, sess.AMR, kycClaimFor(u, ac.Scopes))
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	idToken := ""
	if slices.Contains(ac.Scopes, "openid") {
		idToken, err = h.jwtSvc.SignIDToken(
			ac.UserID, u.Email, u.FullName(), u.DisplayOrFullName(), u.FirstName, u.LastName, u.EmailVerified, clientID, ac.Nonce, h.baseURL, kycClaimFor(u, ac.Scopes),
		)
		if err != nil {
			return apierror.ServerError(c.Path()).Send(c)
		}
	}

	newRefreshToken, err := h.sessionSvc.IssueClientToken(c.Context(), ac.UserID, ac.SessionID, clientID, ac.Scopes)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	// Set httpOnly cookie so SPA clients receive the refresh token without JS access.
	// The SSO session cookie (ctech_session) is independent and is never rotated here,
	// so server-side exchanges by other clients can't log the browser out.
	h.setRefreshCookie(c, newRefreshToken, refreshTokenMaxAge)
	setAuthHintCookie(c, h.cfg, refreshTokenMaxAge)

	// Public (SPA) clients receive the refresh token only in the HttpOnly
	// ctech_rt cookie set above — never in the JSON body, which JS can read and
	// an XSS could exfiltrate. Confidential clients (server-side) get it in the
	// body because they have no usable cookie jar.
	response := fiber.Map{
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_in":   900,
		"id_token":     idToken,
		"scope":        strings.Join(ac.Scopes, " "),
	}
	if !oauthClient.IsPublic() {
		response["refresh_token"] = newRefreshToken
	}
	return c.JSON(response)
}

func (h *TokenHandler) refreshToken(c fiber.Ctx) error {
	// Accept refresh token from form field or httpOnly cookie (SPA clients use the cookie).
	rawRefreshToken := c.FormValue("refresh_token")
	if rawRefreshToken == "" {
		rawRefreshToken = c.Cookies(refreshTokenCookieName)
	}
	clientID := c.FormValue("client_id")

	if rawRefreshToken == "" || clientID == "" {
		// Missing params, not a wrong credential — the common case is a silent
		// refresh fired before any session exists (no ctech_rt cookie yet). Don't
		// burn the shared /token rate-limit budget on a client-side race.
		middleware.SkipRateLimitCount(c)
		return apierror.InvalidRequest("refresh_token and client_id are required.", c.Path()).Send(c)
	}

	oauthClient, err := h.clientRepo.GetByID(c.Context(), clientID)
	if err != nil {
		if errors.Is(err, oauthclient.ErrNotFound) {
			return apierror.InvalidClient("Unknown client_id.", c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}

	// Authenticate confidential clients on refresh, exactly as on the code grant —
	// otherwise a stolen refresh token could be redeemed without the client secret.
	if !oauthClient.IsPublic() {
		clientSecret := c.FormValue("client_secret")
		if clientSecret == "" {
			return apierror.InvalidClient("client_secret is required for confidential clients.", c.Path()).Send(c)
		}
		ok, _ := crypto.VerifyPassword(clientSecret, oauthClient.ClientSecretHash)
		if !ok {
			return apierror.InvalidClient("Invalid client_secret.", c.Path()).Send(c)
		}
	}

	sess, newRawToken, grantedScopes, err := h.sessionSvc.RotateClientToken(c.Context(), rawRefreshToken, clientID)
	if err != nil {
		if errors.Is(err, session.ErrTokenReuse) {
			recordAuditAnon(c, h.audit, audit.EventTokenReuseDetected, map[string]string{"client_id": clientID})
			return apierror.TokenReuse(c.Path()).Send(c)
		}
		if errors.Is(err, session.ErrSessionExpired) {
			// Only reachable with a real refresh token hash — never guessable —
			// so this covers natural TTL expiry and the benign "logout raced an
			// in-flight refresh" case, not brute-force traffic. Don't burn the
			// shared /token rate-limit budget.
			middleware.SkipRateLimitCount(c)
			return apierror.SessionExpired(c.Path()).Send(c)
		}
		if errors.Is(err, session.ErrClientMismatch) {
			return apierror.InvalidGrant("This refresh token was not issued to this client.", c.Path()).Send(c)
		}
		return apierror.InvalidGrant("Invalid or expired refresh token.", c.Path()).Send(c)
	}

	// Clamp the refresh to the scopes actually granted at authorization time, so
	// it can never widen the grant to the client's full allowed set (scope /
	// kyc_level escalation). Re-filtering by the client's current allowed set
	// also drops any scope the client has since lost.
	granted := grantedScopes
	if len(granted) == 0 {
		// Tokens issued before granted scopes were persisted carry none: fall
		// back to the OIDC-grantable set so in-flight sessions keep working.
		granted = []string{scopes.OpenID, scopes.Profile, scopes.Email, scopes.KYC}
	}
	scp := oauthClient.FilterScopes(granted)

	// The kyc_level claim reflects the level at refresh time, so a Confirm
	// becomes visible on the next silent refresh without any push.
	kycLevel := ""
	if slices.Contains(scp, scopes.KYC) {
		u, err := h.userSvc.GetByID(c.Context(), sess.UserID())
		if err != nil {
			return apierror.ServerError(c.Path()).Send(c)
		}
		kycLevel = u.KYCLevel
	}

	accessToken, err := h.jwtSvc.SignAccessToken(sess.UserID(), sess.ID(), clientID, scp, h.baseURL, accessTokenAudience(h.cfg.Audience, oauthClient), sess.AuthTime, sess.LastMFAAt, sess.AMR, kycLevel)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	// Rotate the cookie with the new refresh token value.
	h.setRefreshCookie(c, newRawToken, refreshTokenMaxAge)
	setAuthHintCookie(c, h.cfg, refreshTokenMaxAge)

	// See authorizationCode: public clients get the rotated refresh token only
	// via the ctech_rt cookie, not the JSON body.
	response := fiber.Map{
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_in":   900,
		"scope":        strings.Join(scp, " "),
	}
	if !oauthClient.IsPublic() {
		response["refresh_token"] = newRawToken
	}
	return c.JSON(response)
}

func (h *TokenHandler) Revoke(c fiber.Ctx) error {
	// SPA clients hold the refresh token only in the HttpOnly ctech_rt cookie
	// (no JS access), so accept it from the cookie when the form field is absent.
	rawToken := c.FormValue("token")
	if rawToken == "" {
		rawToken = c.Cookies(refreshTokenCookieName)
	}
	if rawToken == "" {
		return apierror.InvalidRequest("token is required.", c.Path()).Send(c)
	}

	// A presented token is either a per-client refresh token (revoke just that
	// client's chain) or an SSO session token (revoke the whole session).
	if err := h.sessionSvc.RevokeClientToken(c.Context(), rawToken); err != nil {
		if sess, vErr := h.sessionSvc.ValidateToken(c.Context(), rawToken); vErr == nil {
			_ = h.sessionSvc.Revoke(c.Context(), sess.UserID(), sess.ID())
		}
	}

	h.clearRefreshCookie(c)

	return c.Status(fiber.StatusOK).JSON(fiber.Map{"revoked": true})
}

func (h *TokenHandler) setRefreshCookie(c fiber.Ctx, value string, maxAge int) {
	setAuthCookie(c, h.cfg, refreshTokenCookieName, value, maxAge)
}

func (h *TokenHandler) clearRefreshCookie(c fiber.Ctx) {
	clearAuthCookie(c, h.cfg, refreshTokenCookieName)
}

// accessTokenAudience returns this IdP's own audience plus the client's resource
// audience. Without the self audience, a user-flow access token (aud = the
// client's resource server only, e.g. dfe-api) is rejected by this service's own
// bearer-protected endpoints (GET /v1.0/userinfo), since JWTService.Verify
// requires aud to contain cfg.Audience.
func accessTokenAudience(selfAudience string, oauthClient *oauthclient.OAuthClient) []string {
	return append([]string{selfAudience}, oauthClient.EffectiveAudience()...)
}

// kycClaimFor returns the user's KYC level when the kyc scope was granted;
// empty otherwise so the claim is omitted from the token.
func kycClaimFor(u *user.User, scp []string) string {
	if u == nil || !slices.Contains(scp, scopes.KYC) {
		return ""
	}
	return u.KYCLevel
}

func verifyPKCE(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}
