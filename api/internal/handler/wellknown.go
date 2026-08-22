package handler

import (
	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/crypto"
	"gopkg.aoctech.app/account/api/internal/scopes"
)

type WellKnownHandler struct {
	jwtSvc    *crypto.JWTService
	baseURL   string
	issuerURL string
	resource  string
}

func NewWellKnownHandler(jwtSvc *crypto.JWTService, baseURL, issuerURL, resource string) *WellKnownHandler {
	return &WellKnownHandler{jwtSvc: jwtSvc, baseURL: baseURL, issuerURL: issuerURL, resource: resource}
}

func (h *WellKnownHandler) Register(app *fiber.App) {
	wk := app.Group("/.well-known")
	wk.Get("/openid-configuration", h.Configuration)
	wk.Get("/jwks.json", h.JWKS)
	wk.Get("/oauth-protected-resource", h.ProtectedResource)
}

// ProtectedResource publishes the Account API's RFC 9728 metadata. OIDC
// protocol scopes remain in openid-configuration; only Account Resource Server
// permissions are advertised here.
func (h *WellKnownHandler) ProtectedResource(c fiber.Ctx) error {
	supported, err := scopes.AccountPublicScopes()
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err).Send(c)
	}
	return c.JSON(fiber.Map{
		"resource":              h.resource,
		"authorization_servers": []string{h.issuerURL},
		"scopes_supported":      supported,
	})
}

func (h *WellKnownHandler) Configuration(c fiber.Ctx) error {
	issuer := h.baseURL
	return c.JSON(fiber.Map{
		"issuer":                                h.issuerURL,
		"authorization_endpoint":                issuer + "/v1.0/authorize",
		"token_endpoint":                        issuer + "/v1.0/token",
		"userinfo_endpoint":                     issuer + "/v1.0/userinfo",
		"revocation_endpoint":                   issuer + "/v1.0/revoke",
		"end_session_endpoint":                  issuer + "/v1.0/auth/end-session",
		"jwks_uri":                              issuer + "/.well-known/jwks.json",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256", "ES256"},
		// scopes_supported lists only the OIDC identity scopes. Service and
		// internal M2M scopes are deliberately absent: the public catalog lives
		// at GET /v1.0/scopes and internal scopes are hidden by design
		// (scopes.ServiceScopes.Internal).
		"scopes_supported":                      []string{"openid", "profile", "email"},
		"token_endpoint_auth_methods_supported": []string{"none", "client_secret_post"},
		"claims_supported":                      []string{"sub", "email", "name", "email_verified", "iss", "aud", "iat", "exp"},
		"code_challenge_methods_supported":      []string{"S256"},
		"grant_types_supported": []string{
			grantAuthorizationCode, grantRefreshToken, grantClientCredentials, grantAPIKey,
		},
	})
}

func (h *WellKnownHandler) JWKS(c fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"keys": h.jwtSvc.PublicKeyJWKs(),
	})
}
