// Package apierror is the account's catalogue of RFC 7807 problems: every type
// the API emits, under https://accounts.aoctech.app/problems/, built on the
// shared api-commons/problem package (body shape, Send, Retry-After, cause log).
package apierror

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/api-commons/problem"
)

const baseURI = "https://accounts.aoctech.app/problems/"

// ContentType is the RFC 7807 media type.
const ContentType = problem.ContentType

// Problem is the shared problem body; the account's extension members
// (error, error_description, blockers, limit, used, plan, resource) are
// added as extras by the constructors below.
type Problem = problem.Problem

// Extension member names the UI and OAuth clients read.
const (
	memberOAuthError       = "error"
	memberOAuthDescription = "error_description"
	memberBlockers         = "blockers"
	memberLimit            = "limit"
	memberUsed             = "used"
	memberPlan             = "plan"
	memberResource         = "resource"
)

var types = problem.NewRegistry(baseURI)

var _ = func() (n int) {
	for _, d := range [...]struct {
		slug   string
		status int
		title  string
	}{
		{"invalid-request", http.StatusBadRequest, "Bad Request"},
		{"unknown-service", http.StatusUnprocessableEntity, "Unprocessable Entity"},
		{"validation-failed", http.StatusUnprocessableEntity, "Unprocessable Entity"},
		{"unauthorized", http.StatusUnauthorized, "Unauthorized"},
		{"invalid-credentials", http.StatusUnauthorized, "Invalid Credentials"},
		{"invalid-token", http.StatusUnauthorized, "Invalid Token"},
		{"session-expired", http.StatusUnauthorized, "Session Expired"},
		{"token-reuse", http.StatusUnauthorized, "Token Reuse Detected"},
		{"forbidden", http.StatusForbidden, "Forbidden"},
		{"insufficient-scope", http.StatusForbidden, "Insufficient Scope"},
		{"not-found", http.StatusNotFound, "Not Found"},
		{"conflict", http.StatusConflict, "Conflict"},
		{"precondition-failed", http.StatusPreconditionFailed, "Precondition Failed"},
		{"unsupported-grant-type", http.StatusBadRequest, "Unsupported Grant Type"},
		{"invalid-grant", http.StatusBadRequest, "Invalid Grant"},
		{"invalid-client", http.StatusUnauthorized, "Invalid Client"},
		{"invalid-scope", http.StatusBadRequest, "Invalid Scope"},
		{"server-error", http.StatusInternalServerError, "Internal Server Error"},
		{"account-disabled", http.StatusForbidden, "Account Disabled"},
		{"account-pending-deletion", http.StatusForbidden, "Account Scheduled for Deletion"},
		{"service-unavailable", http.StatusServiceUnavailable, "Service Unavailable"},
		{"too-many-requests", http.StatusTooManyRequests, "Too Many Requests"},
		{"step-up-required", http.StatusForbidden, "Step-up Authentication Required"},
		{"mfa-enrollment-required", http.StatusForbidden, "MFA Enrollment Required"},
		{"cpf-already-registered", http.StatusConflict, "CPF Already Registered"},
		{"kyc-already-verified", http.StatusConflict, "Identity Already Verified"},
		{"kyc-not-submitted", http.StatusConflict, "KYC Not Submitted"},
		{"kyc-submission-locked", http.StatusConflict, "Identity Verification Pending"},
		{"kyc-document-not-uploaded", http.StatusConflict, "Document Not Uploaded"},
		{"kyc-document-too-large", http.StatusRequestEntityTooLarge, "Document Too Large"},
		{"age-requirement-not-met", http.StatusUnprocessableEntity, "Age Requirement Not Met"},
		{"unauthorized-client", http.StatusForbidden, "Unauthorized Client"},
		{"email-not-verified", http.StatusForbidden, "Email Not Verified"},
		{"kyc-basic-required", http.StatusConflict, "Basic Verification Required"},
		{"organization-handoff-invalid", http.StatusUnprocessableEntity, "Invalid Handoff"},
		{"plan-limit", http.StatusPaymentRequired, "Plan Limit Reached"},
		{"plan-unavailable", http.StatusServiceUnavailable, "Plan Unavailable"},
		{"deletion-blocked", http.StatusConflict, "Deletion Blocked"},
	} {
		types.Register(d.slug, d.status, d.title)
		n++
	}
	return n
}()

// newProblem builds a registered type; the slug must be in the table above.
func newProblem(slug, detail, instance string) *Problem {
	d, ok := types.Lookup(types.URI(slug))
	if !ok {
		panic("apierror: unregistered problem type " + slug)
	}
	return problem.New(d.Status, d.URI, d.Title, detail).WithInstance(instance)
}

// WithOAuth adds RFC 6749 extension fields for token-endpoint compatibility.
func WithOAuth(p *Problem, oauthCode, description string) *Problem {
	p.WithExtra(memberOAuthError, oauthCode)
	if description != "" {
		p.WithExtra(memberOAuthDescription, description)
	}
	return p
}

// WithPlanUsage adds the numbers. Left off when the plan is somebody else's.
func WithPlanUsage(p *Problem, limit, used int64, plan string) *Problem {
	p.WithExtra(memberLimit, limit).WithExtra(memberUsed, used)
	if plan != "" {
		p.WithExtra(memberPlan, plan)
	}
	return p
}

// NewFromFiber converts a *fiber.Error to an RFC 7807 Problem with correct status.
func NewFromFiber(fe *fiber.Error, instance string) *Problem {
	title := http.StatusText(fe.Code)
	if title == "" {
		title = "Request Error"
	}
	return problem.New(fe.Code, types.URI("request-error"), title, fe.Error()).WithInstance(instance)
}

func InvalidRequest(detail, instance string) *Problem {
	return newProblem("invalid-request", detail, instance)
}

// UnknownService → 422: the product named in a service unlink is not one a
// person can leave on its own.
func UnknownService(detail, instance string) *Problem {
	return newProblem("unknown-service", detail, instance)
}

func ValidationFailed(detail, instance string) *Problem {
	return newProblem("validation-failed", detail, instance)
}

func Unauthorized(detail, instance string) *Problem {
	return newProblem("unauthorized", detail, instance)
}

func InvalidCredentials(instance string) *Problem {
	return newProblem("invalid-credentials", "The email or password is incorrect.", instance)
}

func InvalidToken(detail, instance string) *Problem {
	return newProblem("invalid-token", detail, instance)
}

func SessionExpired(instance string) *Problem {
	return newProblem("session-expired", "Your session has expired. Please log in again.", instance)
}

func TokenReuse(instance string) *Problem {
	return newProblem("token-reuse", "A previously invalidated refresh token was presented. The session has been revoked for security.", instance)
}

func Forbidden(detail, instance string) *Problem {
	return newProblem("forbidden", detail, instance)
}

func InsufficientScope(scope, instance string) *Problem {
	p := newProblem("insufficient-scope", "The access token does not include the required permission.", instance)
	return p.WithRequiredScope(scope)
}

func NotFound(resource, instance string) *Problem {
	return newProblem("not-found", resource+" was not found.", instance)
}

func Conflict(detail, instance string) *Problem {
	return newProblem("conflict", detail, instance)
}

func PreconditionFailed(detail, instance string) *Problem {
	return newProblem("precondition-failed", detail, instance)
}

func UnsupportedGrantType(instance string) *Problem {
	return WithOAuth(newProblem("unsupported-grant-type", "The grant_type is not supported. Supported values: authorization_code, refresh_token.", instance), "unsupported_grant_type", "supported: authorization_code, refresh_token")
}

func InvalidGrant(detail, instance string) *Problem {
	return WithOAuth(newProblem("invalid-grant", detail, instance), "invalid_grant", detail)
}

func InvalidClient(detail, instance string) *Problem {
	return WithOAuth(newProblem("invalid-client", detail, instance), "invalid_client", detail)
}

func InvalidScope(instance string) *Problem {
	return WithOAuth(newProblem("invalid-scope", "No valid scopes were requested for this client.", instance), "invalid_scope", "no valid scopes requested")
}

// InvalidScopeBecause names the scope that made the request invalid.
func InvalidScopeBecause(detail, instance string) *Problem {
	return WithOAuth(newProblem("invalid-scope", detail, instance), "invalid_scope", detail)
}

func ServerError(instance string) *Problem {
	return newProblem("server-error", "An unexpected error occurred. Please try again later.", instance)
}

func AccountDisabled(instance string) *Problem {
	return newProblem("account-disabled", "This account has been disabled. Contact support if you believe this is an error.", instance)
}

// AccountPendingDeletion answers a sign-in for an account with a confirmed
// deletion request. Sent only after the credentials were proven, so it is not
// an enumeration oracle.
func AccountPendingDeletion(instance string) *Problem {
	return newProblem("account-pending-deletion", "This account is scheduled for deletion. Use the cancel link sent to your e-mail to keep it.", instance)
}

func ServiceUnavailable(detail, instance string) *Problem {
	return newProblem("service-unavailable", detail, instance)
}

// TooManyRequests → 429. retryAfter, if > 0, is surfaced as retry_after_seconds
// in the body and (via Send) the Retry-After header; pass 0 when the caller
// has no TTL to report.
func TooManyRequests(detail, instance string, retryAfter time.Duration) *Problem {
	p := newProblem("too-many-requests", detail, instance)
	if retryAfter > 0 {
		p.RetryAfterSeconds = int(retryAfter.Seconds())
	}
	return p
}

// StepUpRequired signals the token's MFA proof is missing or older than maxAge.
// Clients should run the step-up challenge, silent-refresh, and retry.
func StepUpRequired(maxAge time.Duration, instance string) *Problem {
	p := newProblem("step-up-required", "This operation requires recent multi-factor authentication.", instance)
	return p.WithMaxAge(maxAge)
}

// MFAEnrollmentRequired signals the user must enroll an MFA method before
// performing a step-up-protected operation.
func MFAEnrollmentRequired(instance string) *Problem {
	return newProblem("mfa-enrollment-required", "Enroll an authenticator app or passkey to perform this operation.", instance)
}

// CPFAlreadyRegistered → 409: the CPF is claimed by another account.
func CPFAlreadyRegistered(instance string) *Problem {
	return newProblem("cpf-already-registered", "This CPF is already registered to another account.", instance)
}

// KYCAlreadyVerified → 409: identity data is immutable after verification.
func KYCAlreadyVerified(instance string) *Problem {
	return newProblem("kyc-already-verified", "Identity data cannot be changed after verification.", instance)
}

// KYCNotSubmitted → 409: confirm called before any submission.
func KYCNotSubmitted(instance string) *Problem {
	return newProblem("kyc-not-submitted", "The user has not submitted identity data.", instance)
}

// KYCSubmissionLocked → 409: a pending submission is frozen until it is
// rejected by a reviewer or expires.
func KYCSubmissionLocked(instance string) *Problem {
	return newProblem("kyc-submission-locked", "Your identity verification is pending. It cannot be changed until it is reviewed or expires.", instance)
}

// KYCDocumentNotUploaded → 409: confirm called for an object that is not in the
// bucket.
func KYCDocumentNotUploaded(instance string) *Problem {
	return newProblem("kyc-document-not-uploaded", "The document was not uploaded. Retry the upload before confirming.", instance)
}

// KYCDocumentTooLarge → 413: the uploaded document exceeds the size cap.
func KYCDocumentTooLarge(maxBytes int64, instance string) *Problem {
	return newProblem("kyc-document-too-large", fmt.Sprintf("The document exceeds the maximum size of %d bytes.", maxBytes), instance)
}

// AgeRequirementNotMet → 422: user is under the minimum age.
func AgeRequirementNotMet(instance string) *Problem {
	return newProblem("age-requirement-not-met", "You must be at least 18 years old.", instance)
}

// UnauthorizedClient → 403 with OAuth error code unauthorized_client (RFC 6749).
func UnauthorizedClient(instance string) *Problem {
	return UnauthorizedClientBecause("This client is not authorized to use this grant type.", instance)
}

// UnauthorizedClientBecause is UnauthorizedClient with the specific reason: the
// client authenticated, but may not use this grant as it is configured.
func UnauthorizedClientBecause(detail, instance string) *Problem {
	return WithOAuth(newProblem("unauthorized-client", detail, instance), "unauthorized_client", detail)
}

// EmailNotVerified is returned when credentials are valid but the account's email
// address has not been confirmed. Clients should offer to resend the verification link.
func EmailNotVerified(instance string) *Problem {
	return newProblem("email-not-verified", "Verify your email address before signing in. Check your inbox for the verification link.", instance)
}

// KYCBasicRequired → 409: Enhanced verification requires Basic to be
// phone-verified first.
func KYCBasicRequired(instance string) *Problem {
	return newProblem("kyc-basic-required", "Complete phone-verified Basic identity verification before submitting Enhanced documents.", instance)
}

// OrganizationHandoffInvalid → 422: a product sent somebody here to create an
// organization, and the handoff parameters do not check out.
//
// One type for four causes — unknown client, third-party client, unregistered
// origin, malformed URL. The distinctions matter to whoever is fixing the
// integration and travel in the log; they mean nothing to the person on the
// screen, who can do nothing about any of them.
func OrganizationHandoffInvalid(instance string) *Problem {
	return newProblem("organization-handoff-invalid", "The product that sent you here is not configured correctly. You can still create an organization from your account.", instance)
}

const (
	CodePlanLimit       = "plan_limit"
	CodePlanUnavailable = "plan_unavailable"
)

// PlanLimit → 402: the caller's plan does not allow one more space or person.
func PlanLimit(resource, detail, instance string) *Problem {
	p := newProblem("plan-limit", detail, instance)
	p.WithCode(CodePlanLimit)
	if resource != "" {
		p.WithExtra(memberResource, resource)
	}
	return p
}

// PlanUnavailable → 503: billing could not be asked, so nothing was written.
func PlanUnavailable(instance string) *Problem {
	p := newProblem("plan-unavailable", "The plan could not be checked right now. Try again shortly.", instance)
	return p.WithCode(CodePlanUnavailable)
}

// DeletionBlocked answers a deletion request the user must unblock first
// (transfer an organization, delete their OAuth apps, ...).
func DeletionBlocked(blockers any, instance string) *Problem {
	p := newProblem("deletion-blocked", "Resolve the listed items before deleting this account.", instance)
	if blockers != nil {
		p.WithExtra(memberBlockers, blockers)
	}
	return p
}
