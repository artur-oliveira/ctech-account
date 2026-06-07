package handler

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/artur-oliveira/ctech-account/internal/apierror"
	"github.com/artur-oliveira/ctech-account/internal/crypto"
	oauthclient "github.com/artur-oliveira/ctech-account/internal/domain/oauth/client"
	authcode "github.com/artur-oliveira/ctech-account/internal/domain/oauth/code"
	"github.com/artur-oliveira/ctech-account/internal/domain/session"
	"github.com/gofiber/fiber/v3"
)

type AuthorizeHandler struct {
	clientRepo oauthclient.Repository
	codeRepo   *authcode.Repository
	sessionSvc *session.Service
	baseURL    string
}

func NewAuthorizeHandler(
	clientRepo oauthclient.Repository,
	codeRepo *authcode.Repository,
	sessionSvc *session.Service,
	baseURL string,
) *AuthorizeHandler {
	return &AuthorizeHandler{
		clientRepo: clientRepo,
		codeRepo:   codeRepo,
		sessionSvc: sessionSvc,
		baseURL:    baseURL,
	}
}

func (h *AuthorizeHandler) Register(v1 fiber.Router) {
	v1.Get("/authorize", h.Authorize)
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

	// Validate params that don't require a trusted redirect_uri first.
	if clientID == "" || redirectURI == "" {
		return apierror.InvalidRequest("client_id and redirect_uri are required.", c.Path()).Send(c)
	}
	if responseType != "code" {
		return h.redirectError(c, redirectURI, state, "unsupported_response_type", "only response_type=code is supported")
	}
	if codeChallenge != "" && codeChallengeMethod != "S256" {
		return h.redirectError(c, redirectURI, state, "invalid_request", "only S256 code_challenge_method is supported")
	}

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

	requestedScopes := strings.Fields(scopeParam)
	allowedScopes := oauthClient.FilterScopes(requestedScopes)
	if len(allowedScopes) == 0 {
		return h.redirectError(c, redirectURI, state, "invalid_scope", "no valid scopes requested for this client")
	}

	cookieValue := c.Cookies("ctech_session")
	if cookieValue == "" {
		return c.Redirect().To(h.loginURL(c.OriginalURL()))
	}

	sess, err := h.sessionSvc.ValidateCookie(c.Context(), cookieValue)
	if err != nil {
		c.Cookie(&fiber.Cookie{Name: "ctech_session", Value: "", MaxAge: -1, Path: "/"})
		return c.Redirect().To(h.loginURL(c.OriginalURL()))
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

func (h *AuthorizeHandler) loginURL(continueURL string) string {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(continueURL))
	return h.baseURL + "/login?continue=" + encoded
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
