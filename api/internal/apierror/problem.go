package apierror

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

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
	// MaxAgeSeconds extends step-up-required responses with the freshness
	// window the client must satisfy (see StepUpRequired).
	MaxAgeSeconds int64 `json:"max_age_seconds,omitempty"`
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

func TooManyRequests(detail, instance string) *Problem {
	return newProblem("too-many-requests", "Too Many Requests", http.StatusTooManyRequests, detail, instance)
}

// StepUpRequired signals the token's MFA proof is missing or older than maxAge.
// Clients should run the step-up challenge, silent-refresh, and retry.
func StepUpRequired(maxAge time.Duration, instance string) *Problem {
	p := newProblem("step-up-required", "Step-up Authentication Required", http.StatusForbidden,
		"This operation requires recent multi-factor authentication.", instance)
	p.MaxAgeSeconds = int64(maxAge.Seconds())
	return p
}

// MFAEnrollmentRequired signals the user must enroll an MFA method before
// performing a step-up-protected operation.
func MFAEnrollmentRequired(instance string) *Problem {
	return newProblem("mfa-enrollment-required", "MFA Enrollment Required", http.StatusForbidden,
		"Enroll an authenticator app or passkey to perform this operation.", instance)
}

// CPFAlreadyRegistered → 409: the CPF is claimed by another account.
func CPFAlreadyRegistered(instance string) *Problem {
	return newProblem("cpf-already-registered", "CPF Already Registered", http.StatusConflict,
		"This CPF is already registered to another account.", instance)
}

// KYCAlreadyVerified → 409: identity data is immutable after verification.
func KYCAlreadyVerified(instance string) *Problem {
	return newProblem("kyc-already-verified", "Identity Already Verified", http.StatusConflict,
		"Identity data cannot be changed after verification.", instance)
}

// KYCCPFMismatch → 409: presented CPF does not match the declared one.
func KYCCPFMismatch(instance string) *Problem {
	return newProblem("kyc-cpf-mismatch", "CPF Mismatch", http.StatusConflict,
		"The presented CPF does not match the declared one.", instance)
}

// KYCNotSubmitted → 409: confirm called before any submission.
func KYCNotSubmitted(instance string) *Problem {
	return newProblem("kyc-not-submitted", "KYC Not Submitted", http.StatusConflict,
		"The user has not submitted identity data.", instance)
}

// KYCSubmissionLocked → 409: a pending submission is frozen until it is
// rejected by a reviewer or expires.
func KYCSubmissionLocked(instance string) *Problem {
	return newProblem("kyc-submission-locked", "Identity Verification Pending", http.StatusConflict,
		"Your identity verification is pending. It cannot be changed until it is reviewed or expires.", instance)
}

// KYCWrongMethod → 409: the operation does not match the verification method
// the user chose (e.g. a PIX confirmation for a document submission).
func KYCWrongMethod(instance string) *Problem {
	return newProblem("kyc-wrong-method", "Wrong Verification Method", http.StatusConflict,
		"This operation does not match the verification method chosen by the user.", instance)
}

// KYCDocumentNotUploaded → 409: confirm called for an object that is not in the
// bucket.
func KYCDocumentNotUploaded(instance string) *Problem {
	return newProblem("kyc-document-not-uploaded", "Document Not Uploaded", http.StatusConflict,
		"The document was not uploaded. Retry the upload before confirming.", instance)
}

// KYCDocumentTooLarge → 413: the uploaded document exceeds the size cap.
func KYCDocumentTooLarge(maxBytes int64, instance string) *Problem {
	return newProblem("kyc-document-too-large", "Document Too Large", http.StatusRequestEntityTooLarge,
		fmt.Sprintf("The document exceeds the maximum size of %d bytes.", maxBytes), instance)
}

// AgeRequirementNotMet → 422: user is under the minimum age.
func AgeRequirementNotMet(instance string) *Problem {
	return newProblem("age-requirement-not-met", "Age Requirement Not Met", http.StatusUnprocessableEntity,
		"You must be at least 18 years old.", instance)
}

// UnauthorizedClient → 403 with OAuth error code unauthorized_client (RFC 6749).
func UnauthorizedClient(instance string) *Problem {
	return newProblem("unauthorized-client", "Unauthorized Client", http.StatusForbidden,
		"This client is not authorized to use this grant type.", instance).
		WithOAuth("unauthorized_client", "This client is not authorized to use this grant type.")
}

// EmailNotVerified is returned when credentials are valid but the account's email
// address has not been confirmed. Clients should offer to resend the verification link.
func EmailNotVerified(instance string) *Problem {
	return newProblem("email-not-verified", "Email Not Verified", http.StatusForbidden,
		"Verify your email address before signing in. Check your inbox for the verification link.", instance)
}
