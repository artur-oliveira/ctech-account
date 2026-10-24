package middleware

import (
	"errors"
	"slices"
	"strings"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/crypto"
	"gopkg.aoctech.app/account/api/internal/problem"
	"gopkg.aoctech.app/api-commons/authguard"
	"gopkg.aoctech.app/api-commons/jwtverify"
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
			return err // already an *problem.Problem
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
	// The go-common guards (scope, step-up, client, internal) read these claims.
	authguard.SetClaims(c, &jwtverify.Claims{
		Sub: id.userID, Scope: id.scopes, SID: id.sessionID, AZP: id.clientID, LastMFAAt: id.lastMFAAt,
	})
}

func extractAndVerify(c fiber.Ctx, jwtSvc *crypto.JWTService) (tokenIdentity, error) {
	var id tokenIdentity

	authHeader := c.Get("Authorization")
	if authHeader == "" {
		return id, problem.Unauthorized("Missing Authorization header.", c.Path())
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return id, problem.InvalidToken("Authorization header must be 'Bearer <token>'.", c.Path())
	}

	claims, verifyErr := jwtSvc.Verify(parts[1])
	if verifyErr != nil {
		return id, problem.InvalidToken("The access token is invalid or has expired.", c.Path())
	}
	sub, _ := claims["sub"].(string)
	iat, _ := claims["iat"].(float64)
	// Reads fail open when the revocation list is down, every other verb fails
	// closed (503): same policy as go-common authguard.RequireUnrevokedByMethod.
	failClosed := authguard.PolicyForMethod(c.Method()) == authguard.PolicyWriteClosed
	if err := jwtSvc.CheckRevoked(c.Context(), sub, int64(iat), failClosed); err != nil {
		if errors.Is(err, jwtverify.ErrRevocationUnavailable) {
			return id, problem.ServiceUnavailable("Could not validate the session. Please try again.", c.Path()).WithCause(err)
		}
		return id, problem.InvalidToken("The access token is invalid or has expired.", c.Path())
	}

	id.userID, _ = claims["sub"].(string)
	id.sessionID, _ = claims["sid"].(string)
	id.scopes, _ = claims["scope"].(string)
	id.clientID, _ = claims["azp"].(string)
	if v, ok := claims["last_mfa_at"].(float64); ok {
		id.lastMFAAt = int64(v)
	}

	if id.userID == "" {
		return id, problem.InvalidToken("Access token is missing required claims.", c.Path())
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

// RequireScope guards an individual Resource Server operation. It must run
// after RequireAuth and emits the OAuth bearer insufficient_scope challenge so
// clients can distinguish missing authorization from a generic policy denial.
// Kept local (not authguard.RequireScope): the wire type must stay
// <base>/problems/insufficient-scope with required_scope, which the UI and
// clients branch on; go-common answers a plain forbidden.
func RequireScope(scope string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if !HasScope(c, scope) {
			c.Set(fiber.HeaderWWWAuthenticate, `Bearer error="insufficient_scope", scope="`+scope+`"`)
			return problem.InsufficientScope(scope, c.Path()).Send(c)
		}
		return c.Next()
	}
}
