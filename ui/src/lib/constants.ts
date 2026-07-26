// Shared literals — see ui/CLAUDE.md "Constants — no magic strings".

// sessionStorage keys carrying the MFA challenge between /login and /login/mfa.
export const MFA_TOKEN_KEY = 'mfa_token'
export const MFA_METHODS_KEY = 'mfa_methods'
export const CONTINUE_URL_KEY = 'continue_url'

export const MFA_METHOD_TOTP = 'totp'
export const MFA_METHOD_PASSKEY = 'passkey'

// KYC — must stay in step with api/internal/domain/kyc/model.go.
export const CPF_DIGITS = 11

/** Mirrors kyc.MinAge — client-side pre-check only, server remains authoritative. */
export const KYC_MIN_AGE_YEARS = 18

/** Mirrors kyc.MaxDocumentBytes (5 MiB) so the UI rejects oversized files early. */
export const MAX_DOCUMENT_BYTES = 5 * 1024 * 1024

/** Mirrors kyc.allowedContentTypes — every Enhanced document is now a static photo or PDF, no video. */
export const ID_DOCUMENT_ACCEPTED_TYPES = ['image/jpeg', 'image/png', 'image/heic', 'application/pdf'] as const

/** Content types ID_DOCUMENT_ACCEPTED_TYPES allows that a browser <img>/next/image can actually decode inline. */
export const ID_DOCUMENT_PREVIEWABLE_TYPES = ['image/jpeg', 'image/png'] as const

/** Mirrors kyc.RequiredDocTypes — SubmitEnhanced is rejected until every one is uploaded. */
export const REQUIRED_DOC_TYPES = ['id_front', 'id_back', 'selfie_with_document'] as const

/** Mirrors kyc.OTPLength. */
export const OTP_CODE_LENGTH = 6

/** Mirrors kyc.OTPResendCooldown (seconds) — drives the resend button's countdown. */
export const OTP_RESEND_COOLDOWN_SECONDS = 60

/** Same contact used on /privacy and /terms — one address for user-facing support asks. */
export const SUPPORT_EMAIL = 'dpo@aoctech.app'
