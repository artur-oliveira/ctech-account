package handler

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/artur-oliveira/ctech-account/internal/apierror"
	"github.com/artur-oliveira/ctech-account/internal/config"
	"github.com/artur-oliveira/ctech-account/internal/crypto"
	oauthclient "github.com/artur-oliveira/ctech-account/internal/domain/oauth/client"
	authcode "github.com/artur-oliveira/ctech-account/internal/domain/oauth/code"
	"github.com/artur-oliveira/ctech-account/internal/domain/session"
	"github.com/artur-oliveira/ctech-account/internal/domain/user"
	"github.com/gofiber/fiber/v3"
)

const refreshTokenCookieName = "ctech_rt"
const refreshTokenMaxAge = 90 * 24 * 60 * 60

type TokenHandler struct {
	clientRepo oauthclient.Repository
	codeRepo   *authcode.Repository
	sessionSvc *session.Service
	userSvc    *user.Service
	jwtSvc     *crypto.JWTService
	baseURL    string
	cfg        *config.Config
}

func NewTokenHandler(
	clientRepo oauthclient.Repository,
	codeRepo *authcode.Repository,
	sessionSvc *session.Service,
	userSvc *user.Service,
	jwtSvc *crypto.JWTService,
	baseURL string,
	cfg *config.Config,
) *TokenHandler {
	return &TokenHandler{
		clientRepo: clientRepo,
		codeRepo:   codeRepo,
		sessionSvc: sessionSvc,
		userSvc:    userSvc,
		jwtSvc:     jwtSvc,
		baseURL:    baseURL,
		cfg:        cfg,
	}
}

func (h *TokenHandler) Register(v1 fiber.Router) {
	v1.Post("/token", h.Token)
	v1.Post("/revoke", h.Revoke)
}

func (h *TokenHandler) Token(c fiber.Ctx) error {
	grantType := c.FormValue("grant_type")
	switch grantType {
	case "authorization_code":
		return h.authorizationCode(c)
	case "refresh_token":
		return h.refreshToken(c)
	default:
		return apierror.UnsupportedGrantType(c.Path()).Send(c)
	}
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
	ac, err := h.codeRepo.Get(c.Context(), codeHash)
	if err != nil {
		if errors.Is(err, authcode.ErrNotFound) {
			return apierror.InvalidGrant("Authorization code not found or has expired.", c.Path()).Send(c)
		}
		return apierror.ServerError(c.Path()).Send(c)
	}
	_ = h.codeRepo.Delete(c.Context(), codeHash) // single use

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

	accessToken, err := h.jwtSvc.SignAccessToken(ac.UserID, ac.SessionID, ac.Scopes, h.baseURL)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	idToken := ""
	if slices.Contains(ac.Scopes, "openid") {
		idToken, err = h.jwtSvc.SignIDToken(ac.UserID, u.Email, u.FullName(), clientID, ac.Nonce, h.baseURL)
		if err != nil {
			return apierror.ServerError(c.Path()).Send(c)
		}
	}

	newRefreshToken, err := h.sessionSvc.ReplaceRefreshToken(c.Context(), ac.UserID, ac.SessionID)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	refreshTokenValue := session.BuildCookieValue(ac.UserID, ac.SessionID, newRefreshToken)

	// Set httpOnly cookie so SPA clients receive the refresh token without JS access.
	h.setRefreshCookie(c, refreshTokenValue, refreshTokenMaxAge)

	return c.JSON(fiber.Map{
		"access_token":  accessToken,
		"token_type":    "Bearer",
		"expires_in":    900,
		"refresh_token": refreshTokenValue,
		"id_token":      idToken,
		"scope":         strings.Join(ac.Scopes, " "),
	})
}

func (h *TokenHandler) refreshToken(c fiber.Ctx) error {
	// Accept refresh token from form field or httpOnly cookie (SPA clients use the cookie).
	rawRefreshToken := c.FormValue("refresh_token")
	if rawRefreshToken == "" {
		rawRefreshToken = c.Cookies(refreshTokenCookieName)
	}
	clientID := c.FormValue("client_id")

	if rawRefreshToken == "" || clientID == "" {
		return apierror.InvalidRequest("refresh_token and client_id are required.", c.Path()).Send(c)
	}

	userID, sessionID, rawToken, ok := splitRefreshToken(rawRefreshToken)
	if !ok {
		return apierror.InvalidGrant("Invalid refresh_token format.", c.Path()).Send(c)
	}

	newRawToken, err := h.sessionSvc.Rotate(c.Context(), userID, sessionID, rawToken)
	if err != nil {
		if errors.Is(err, session.ErrTokenReuse) {
			return apierror.TokenReuse(c.Path()).Send(c)
		}
		if errors.Is(err, session.ErrSessionExpired) {
			return apierror.SessionExpired(c.Path()).Send(c)
		}
		return apierror.InvalidGrant("Invalid or expired refresh token.", c.Path()).Send(c)
	}

	scopes := []string{"openid", "profile", "email"}
	accessToken, err := h.jwtSvc.SignAccessToken(userID, sessionID, scopes, h.baseURL)
	if err != nil {
		return apierror.ServerError(c.Path()).Send(c)
	}

	newTokenValue := session.BuildCookieValue(userID, sessionID, newRawToken)

	// Rotate the cookie with the new refresh token value.
	h.setRefreshCookie(c, newTokenValue, refreshTokenMaxAge)

	return c.JSON(fiber.Map{
		"access_token":  accessToken,
		"token_type":    "Bearer",
		"expires_in":    900,
		"refresh_token": newTokenValue,
		"scope":         strings.Join(scopes, " "),
	})
}

func (h *TokenHandler) Revoke(c fiber.Ctx) error {
	rawToken := c.FormValue("token")
	if rawToken == "" {
		return apierror.InvalidRequest("token is required.", c.Path()).Send(c)
	}

	if userID, sessionID, _, ok := splitRefreshToken(rawToken); ok {
		_ = h.sessionSvc.Revoke(c.Context(), userID, sessionID)
	}

	h.clearRefreshCookie(c)

	return c.Status(fiber.StatusOK).JSON(fiber.Map{"revoked": true})
}

func (h *TokenHandler) setRefreshCookie(c fiber.Ctx, value string, maxAge int) {
	c.Cookie(&fiber.Cookie{
		Name:     refreshTokenCookieName,
		Value:    value,
		MaxAge:   maxAge,
		HTTPOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: "Lax",
		Domain:   h.cfg.CookieDomain,
		Path:     "/",
		Expires:  time.Now().Add(time.Duration(maxAge) * time.Second),
	})
}

func (h *TokenHandler) clearRefreshCookie(c fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     refreshTokenCookieName,
		Value:    "",
		MaxAge:   -1,
		HTTPOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: "Lax",
		Domain:   h.cfg.CookieDomain,
		Path:     "/",
	})
}

func splitRefreshToken(raw string) (userID, sessionID, token string, ok bool) {
	parts := strings.SplitN(raw, ":", 3)
	if len(parts) != 3 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

func verifyPKCE(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}
