package apierror

import (
	"encoding/json"
	"net/http"

	"github.com/gofiber/fiber/v3"
)

const baseURI = "https://accounts.aoctech.app/problems/"
const ContentType = "application/problem+json"

// Problem implements RFC 7807 Problem Details for HTTP APIs.
// The OAuthError and OAuthDescription fields extend the base type to remain
// compatible with RFC 6749 clients on the token endpoint.
type Problem struct {
	Type             string `json:"type"`
	Title            string `json:"title"`
	Status           int    `json:"status"`
	Detail           string `json:"detail,omitempty"`
	Instance         string `json:"instance,omitempty"`
	OAuthError       string `json:"error,omitempty"`
	OAuthDescription string `json:"error_description,omitempty"`
}

func (p *Problem) Error() string { return p.Detail }

// Send writes the problem as an RFC 7807 response.
// Uses manual JSON marshaling so fiber.JSON() cannot override the content type.
func (p *Problem) Send(c fiber.Ctx) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	c.Status(p.Status)
	c.Set(fiber.HeaderContentType, ContentType)
	return c.Send(b)
}

// WithOAuth adds RFC 6749 extension fields for token-endpoint compatibility.
func (p *Problem) WithOAuth(oauthCode, description string) *Problem {
	p.OAuthError = oauthCode
	p.OAuthDescription = description
	return p
}

func newProblem(slug, title string, status int, detail, instance string) *Problem {
	return &Problem{
		Type:     baseURI + slug,
		Title:    title,
		Status:   status,
		Detail:   detail,
		Instance: instance,
	}
}

func InvalidRequest(detail, instance string) *Problem {
	return newProblem("invalid-request", "Bad Request", http.StatusBadRequest, detail, instance)
}

func ValidationFailed(detail, instance string) *Problem {
	return newProblem("validation-failed", "Unprocessable Entity", http.StatusUnprocessableEntity, detail, instance)
}

func Unauthorized(detail, instance string) *Problem {
	return newProblem("unauthorized", "Unauthorized", http.StatusUnauthorized, detail, instance)
}

func InvalidCredentials(instance string) *Problem {
	return newProblem("invalid-credentials", "Invalid Credentials", http.StatusUnauthorized,
		"The email or password is incorrect.", instance)
}

func InvalidToken(detail, instance string) *Problem {
	return newProblem("invalid-token", "Invalid Token", http.StatusUnauthorized, detail, instance)
}

func SessionExpired(instance string) *Problem {
	return newProblem("session-expired", "Session Expired", http.StatusUnauthorized,
		"Your session has expired. Please log in again.", instance)
}

func TokenReuse(instance string) *Problem {
	return newProblem("token-reuse", "Token Reuse Detected", http.StatusUnauthorized,
		"A previously invalidated refresh token was presented. The session has been revoked for security.", instance)
}

func Forbidden(detail, instance string) *Problem {
	return newProblem("forbidden", "Forbidden", http.StatusForbidden, detail, instance)
}

func NotFound(resource, instance string) *Problem {
	return newProblem("not-found", "Not Found", http.StatusNotFound,
		resource+" was not found.", instance)
}

func Conflict(detail, instance string) *Problem {
	return newProblem("conflict", "Conflict", http.StatusConflict, detail, instance)
}

func UnsupportedGrantType(instance string) *Problem {
	return newProblem("unsupported-grant-type", "Unsupported Grant Type", http.StatusBadRequest,
		"The grant_type is not supported. Supported values: authorization_code, refresh_token.", instance).
		WithOAuth("unsupported_grant_type", "supported: authorization_code, refresh_token")
}

func InvalidGrant(detail, instance string) *Problem {
	return newProblem("invalid-grant", "Invalid Grant", http.StatusBadRequest, detail, instance).
		WithOAuth("invalid_grant", detail)
}

func InvalidClient(detail, instance string) *Problem {
	return newProblem("invalid-client", "Invalid Client", http.StatusUnauthorized, detail, instance).
		WithOAuth("invalid_client", detail)
}

func InvalidScope(instance string) *Problem {
	return newProblem("invalid-scope", "Invalid Scope", http.StatusBadRequest,
		"No valid scopes were requested for this client.", instance).
		WithOAuth("invalid_scope", "no valid scopes requested")
}

func ServerError(instance string) *Problem {
	return newProblem("server-error", "Internal Server Error", http.StatusInternalServerError,
		"An unexpected error occurred. Please try again later.", instance)
}

// NewFromFiber converts a *fiber.Error to an RFC 7807 Problem with correct status.
func NewFromFiber(fe *fiber.Error, instance string) *Problem {
	title := http.StatusText(fe.Code)
	if title == "" {
		title = "Request Error"
	}
	return &Problem{
		Type:     baseURI + "request-error",
		Title:    title,
		Status:   fe.Code,
		Detail:   fe.Error(),
		Instance: instance,
	}
}

func AccountDisabled(instance string) *Problem {
	return newProblem("account-disabled", "Account Disabled", http.StatusForbidden,
		"This account has been disabled. Contact support if you believe this is an error.", instance)
}

func ServiceUnavailable(detail, instance string) *Problem {
	return newProblem("service-unavailable", "Service Unavailable", http.StatusServiceUnavailable, detail, instance)
}
