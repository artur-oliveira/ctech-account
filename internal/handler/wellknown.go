package handler

import (
	"github.com/artur-oliveira/ctech-account/internal/crypto"
	"github.com/gofiber/fiber/v3"
)

type WellKnownHandler struct {
	jwtSvc  *crypto.JWTService
	baseURL string
}

func NewWellKnownHandler(jwtSvc *crypto.JWTService, baseURL string) *WellKnownHandler {
	return &WellKnownHandler{jwtSvc: jwtSvc, baseURL: baseURL}
}

func (h *WellKnownHandler) Register(app *fiber.App) {
	wk := app.Group("/.well-known")
	wk.Get("/openid-configuration", h.Configuration)
	wk.Get("/jwks.json", h.JWKS)
}

func (h *WellKnownHandler) Configuration(c fiber.Ctx) error {
	issuer := h.baseURL
	return c.JSON(fiber.Map{
		"issuer":                                issuer,
		"authorization_endpoint":                issuer + "/v1/authorize",
		"token_endpoint":                        issuer + "/v1/token",
		"userinfo_endpoint":                     issuer + "/v1/userinfo",
		"revocation_endpoint":                   issuer + "/v1/revoke",
		"jwks_uri":                              issuer + "/.well-known/jwks.json",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported":                      []string{"openid", "profile", "email"},
		"token_endpoint_auth_methods_supported": []string{"none", "client_secret_post"},
		"claims_supported":                      []string{"sub", "email", "name", "email_verified", "iss", "aud", "iat", "exp"},
		"code_challenge_methods_supported":      []string{"S256"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
	})
}

func (h *WellKnownHandler) JWKS(c fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"keys": []fiber.Map{h.jwtSvc.PublicKeyJWK()},
	})
}
