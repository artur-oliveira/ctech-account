// Shared literals — see ui/CLAUDE.md "Constants — no magic strings".

// sessionStorage keys carrying the MFA challenge between /login and /login/mfa.
export const MFA_TOKEN_KEY = 'mfa_token'
export const MFA_METHODS_KEY = 'mfa_methods'
export const CONTINUE_URL_KEY = 'continue_url'

// RFC 7807 `type` suffix the API answers when mfa_token is dead (expired,
// already consumed, or invalidated after too many wrong TOTP attempts) —
// distinct from a merely wrong code, which answers "unauthorized" instead.
export const MFA_INVALID_TOKEN_PROBLEM = 'invalid-token'

export const MFA_METHOD_TOTP = 'totp'

// Passkey login currently returns the RFC 8176 AMR value (`otp`) while
// password login returns the challenge label (`totp`). Accept both until the
// API response is normalized so an otherwise valid challenge is never lost.
export const MFA_METHOD_OTP_AMR = 'otp'

export function isTOTPMFAMethod(method: string): boolean {
  return method === MFA_METHOD_TOTP || method === MFA_METHOD_OTP_AMR
}

// KYC — must stay in step with api/internal/domain/kyc/model.go.
export const CPF_DIGITS = 11

// Brazilian tax identifiers. CNPJ's first 12 positions may be alphanumeric;
// the two check digits keep the canonical document at 14 characters.
export const TAX_ID_CPF_LENGTH = 11
export const TAX_ID_CNPJ_LENGTH = 14
export const TAX_ID_FORMATTED_MAX_LENGTH = 18

/** Public, credential-free company-register API called directly by the SPA. */
export const CNPJA_API_BASE_URL = 'https://open.cnpja.com'

/** Company tools become useful before the picker turns into a scanning chore. */
export const COMPANY_PICKER_SEARCH_THRESHOLD = 6

/** Prevents accidental oversized Basic KYC submissions; the API remains authoritative. */
export const KYC_LEGAL_NAME_MAX_LENGTH = 255

/** Mirrors kyc.MinAge — client-side pre-check only, server remains authoritative. */
export const KYC_MIN_AGE_YEARS = 18

/** Basic KYC address bounds mirror api/internal/handler/kyc.go. */
export const KYC_ADDRESS_LIMITS = {
  zipCode: 8,
  street: 200,
  number: 20,
  complement: 100,
  district: 100,
  city: 100,
  state: 2,
} as const

/** Public Brazilian postal-code service used only to assist address entry. */
export const VIACEP_API_BASE_URL = 'https://viacep.com.br/ws'

/** Mirrors kyc.MaxDocumentBytes (5 MiB) so the UI rejects oversized files early. */
export const MAX_DOCUMENT_BYTES = 5 * 1024 * 1024

/** Mirrors kyc.allowedContentTypes — every Enhanced document is now a static photo or PDF, no video. */
export const ID_DOCUMENT_ACCEPTED_TYPES = ['image/jpeg', 'image/png', 'image/heic', 'application/pdf'] as const

/** Content types ID_DOCUMENT_ACCEPTED_TYPES allows that a browser <img>/next/image can actually decode inline. */
export const ID_DOCUMENT_PREVIEWABLE_TYPES = ['image/jpeg', 'image/png'] as const

/** Mirrors kyc.RequiredDocTypes — SubmitEnhanced is rejected until every one is uploaded. */
export const REQUIRED_DOC_TYPES = ['id_front', 'id_back', 'selfie_with_document'] as const

/** Same contact used on /privacy and /terms — one address for user-facing support asks. */
export const SUPPORT_EMAIL = 'dpo@aoctech.app'
