package middleware

import (
	"strings"

	"github.com/artur-oliveira/ctech-account/internal/apierror"
	"github.com/artur-oliveira/ctech-account/internal/crypto"
	"github.com/gofiber/fiber/v3"
)

const (
	LocalUserID    = "user_id"
	LocalSessionID = "session_id"
	LocalScopes    = "scopes"
)

func RequireAuth(jwtSvc *crypto.JWTService) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, sessionID, scopes, err := extractAndVerify(c, jwtSvc)
		if err != nil {
			return err // already an *apierror.Problem
		}
		c.Locals(LocalUserID, userID)
		c.Locals(LocalSessionID, sessionID)
		c.Locals(LocalScopes, scopes)
		return c.Next()
	}
}

func OptionalAuth(jwtSvc *crypto.JWTService) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, sessionID, scopes, err := extractAndVerify(c, jwtSvc)
		if err == nil {
			c.Locals(LocalUserID, userID)
			c.Locals(LocalSessionID, sessionID)
			c.Locals(LocalScopes, scopes)
		}
		return c.Next()
	}
}

func extractAndVerify(c fiber.Ctx, jwtSvc *crypto.JWTService) (userID, sessionID, scopes string, err error) {
	authHeader := c.Get("Authorization")
	if authHeader == "" {
		return "", "", "", apierror.Unauthorized("Missing Authorization header.", c.Path())
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", "", "", apierror.InvalidToken("Authorization header must be 'Bearer <token>'.", c.Path())
	}

	claims, verifyErr := jwtSvc.Verify(parts[1])
	if verifyErr != nil {
		return "", "", "", apierror.InvalidToken("The access token is invalid or has expired.", c.Path())
	}

	sub, _ := claims["sub"].(string)
	sid, _ := claims["sid"].(string)
	scope, _ := claims["scope"].(string)

	if sub == "" {
		return "", "", "", apierror.InvalidToken("Access token is missing required claims.", c.Path())
	}

	return sub, sid, scope, nil
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
