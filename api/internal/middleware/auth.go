package middleware

import (
	"slices"
	"strings"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/crypto"
)

const (
	LocalUserID    = "user_id"
	LocalSessionID = "session_id"
	LocalScopes    = "scopes"
	LocalLastMFAAt = "last_mfa_at"
	LocalClientID  = "client_id"
)

func RequireAuth(jwtSvc *crypto.JWTService) fiber.Handler {
	return func(c fiber.Ctx) error {
		id, err := extractAndVerify(c, jwtSvc)
		if err != nil {
			return err // already an *apierror.Problem
		}
		setAuthLocals(c, id)
		return c.Next()
	}
}

func OptionalAuth(jwtSvc *crypto.JWTService) fiber.Handler {
	return func(c fiber.Ctx) error {
		id, err := extractAndVerify(c, jwtSvc)
		if err == nil {
			setAuthLocals(c, id)
		}
		return c.Next()
	}
}

// tokenIdentity carries the verified claims exposed to handlers via locals.
type tokenIdentity struct {
	userID    string
	sessionID string
	scopes    string
	lastMFAAt int64
	clientID  string
}

func setAuthLocals(c fiber.Ctx, id tokenIdentity) {
	c.Locals(LocalUserID, id.userID)
	c.Locals(LocalSessionID, id.sessionID)
	c.Locals(LocalScopes, id.scopes)
	c.Locals(LocalLastMFAAt, id.lastMFAAt)
	c.Locals(LocalClientID, id.clientID)
}

func extractAndVerify(c fiber.Ctx, jwtSvc *crypto.JWTService) (tokenIdentity, error) {
	var id tokenIdentity

	authHeader := c.Get("Authorization")
	if authHeader == "" {
		return id, apierror.Unauthorized("Missing Authorization header.", c.Path())
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return id, apierror.InvalidToken("Authorization header must be 'Bearer <token>'.", c.Path())
	}

	claims, verifyErr := jwtSvc.Verify(parts[1])
	if verifyErr != nil {
		return id, apierror.InvalidToken("The access token is invalid or has expired.", c.Path())
	}

	id.userID, _ = claims["sub"].(string)
	id.sessionID, _ = claims["sid"].(string)
	id.scopes, _ = claims["scope"].(string)
	id.clientID, _ = claims["azp"].(string)
	if v, ok := claims["last_mfa_at"].(float64); ok {
		id.lastMFAAt = int64(v)
	}

	if id.userID == "" {
		return id, apierror.InvalidToken("Access token is missing required claims.", c.Path())
	}

	return id, nil
}

// GetUserID retrieves the authenticated user ID from context locals.
func GetUserID(c fiber.Ctx) string {
	v, _ := c.Locals(LocalUserID).(string)
	return v
}

// GetSessionID retrieves the session ID from context locals.
func GetSessionID(c fiber.Ctx) string {
	v, _ := c.Locals(LocalSessionID).(string)
	return v
}

// GetScopes retrieves the space-joined scope string from context locals.
func GetScopes(c fiber.Ctx) string {
	v, _ := c.Locals(LocalScopes).(string)
	return v
}

// HasScope reports whether the verified token carries the given scope.
func HasScope(c fiber.Ctx, scope string) bool {
	return slices.Contains(strings.Fields(GetScopes(c)), scope)
}

// GetLastMFAAt retrieves the last_mfa_at claim from context locals (0 when the
// token carries no MFA proof).
func GetLastMFAAt(c fiber.Ctx) int64 {
	v, _ := c.Locals(LocalLastMFAAt).(int64)
	return v
}

// GetClientID retrieves the token's azp (authorized party / OAuth client_id)
// claim from context locals.
func GetClientID(c fiber.Ctx) string {
	v, _ := c.Locals(LocalClientID).(string)
	return v
}

// RequireClientID guards self-service account-management routes
// (/v1.0/account/*, /v1.0/step-up/*) that have no scope of their own. Unlike a
// resource server's API, these are never meant to be reachable by any OAuth
// client other than this service's own first-party frontend — accepting any
// audience-matching token here would let a downstream client (dfe) or a
// consented third party fully manage another app's account (API keys, OAuth
// clients, sessions, passkeys, audit log). Must run after RequireAuth.
func RequireClientID(clientID string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if GetClientID(c) != clientID {
			return apierror.Forbidden("This endpoint is only accessible to this service's own frontend.", c.Path()).Send(c)
		}
		return c.Next()
	}
}

// RequireInternalScope guards service-to-service routes. It must run after
// RequireAuth. Only client_credentials tokens pass: they carry no session
// (sid empty — user and api_key tokens always have one) and must hold the
// required internal scope.
func RequireInternalScope(scope string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if GetSessionID(c) != "" {
			return apierror.Forbidden("This endpoint accepts service tokens only.", c.Path()).Send(c)
		}
		if !HasScope(c, scope) {
			return apierror.Forbidden("Missing required scope.", c.Path()).Send(c)
		}
		return c.Next()
	}
}
