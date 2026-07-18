package audit

// Event type constants. Naming: <area>.<action>. Never rename a shipped
// value — stored events are immutable history.
const (
	EventLoginSuccess          = "login.success"
	EventLoginFailed           = "login.failed"
	EventLoginMFARequired      = "login.mfa_required"
	EventMFAChallengeSuccess   = "mfa.challenge.success"
	EventMFAChallengeFailed    = "mfa.challenge.failed"
	EventStepUpSuccess         = "stepup.success"
	EventStepUpFailed          = "stepup.failed"
	EventPasswordChanged       = "password.changed"
	EventPasswordResetRequest  = "password.reset_requested"
	EventPasswordResetComplete = "password.reset_completed"
	EventEmailVerified         = "email.verified"
	EventTOTPEnabled           = "totp.enabled"
	EventTOTPDisabled          = "totp.disabled"
	EventPasskeyAdded          = "passkey.added"
	EventPasskeyRemoved        = "passkey.removed"
	EventBackupCodesRegen      = "backup_codes.regenerated"
	EventAPIKeyCreated         = "apikey.created"
	EventAPIKeyRevoked         = "apikey.revoked"
	EventOAuthClientCreated    = "oauth_client.created"
	EventOAuthClientUpdated    = "oauth_client.updated"
	EventOAuthClientDeleted    = "oauth_client.deleted"
	EventConsentGranted        = "consent.granted"
	EventConsentRevoked        = "consent.revoked"
	EventSessionRevoked        = "session.revoked"
	EventSessionRevokedAll     = "session.revoked_all"
	EventTokenReuseDetected    = "token.reuse_detected"
	EventSocialLinked          = "social.linked"
	EventSocialUnlinked        = "social.unlinked"

	// KYC lifecycle. Metadata never contains personal data (no CPF).
	EventKYCSubmitted        = "kyc.submitted"
	EventKYCVerified         = "kyc.verified"
	EventKYCDocumentUploaded = "kyc.document_uploaded"
	EventKYCRejected         = "kyc.rejected"

	// EventTermsAccepted fires at registration for the password flow, on the
	// post-signup interstitial for Google sign-up, and again whenever a ToS or
	// Privacy version bump re-gates an existing account. The metadata carries
	// the accepted versions and the method that captured them.
	EventTermsAccepted = "auth.terms_accepted"
)
