// Shared literals — see ui/CLAUDE.md "Constants — no magic strings".

// sessionStorage keys carrying the MFA challenge between /login and /login/mfa.
export const MFA_TOKEN_KEY = 'mfa_token'
export const MFA_METHODS_KEY = 'mfa_methods'
export const CONTINUE_URL_KEY = 'continue_url'

export const MFA_METHOD_TOTP = 'totp'
export const MFA_METHOD_PASSKEY = 'passkey'

// KYC — must stay in step with internal/domain/kyc/model.go.
export const CPF_DIGITS = 11
export const ZIP_CODE_DIGITS = 8

/** Mirrors kyc.MinAge — client-side pre-check only, server remains authoritative. */
export const KYC_MIN_AGE_YEARS = 18

/** Mirrors kyc.MaxDocumentBytes (5 MiB) so the UI rejects oversized files early. */
export const MAX_DOCUMENT_BYTES = 5 * 1024 * 1024

/** Mirrors kyc.allowedContentTypes, minus the video types that only apply to selfie clip uploads (see id_front/id_back file picker). */
export const ID_DOCUMENT_ACCEPTED_TYPES = ['image/jpeg', 'image/png', 'image/heic', 'application/pdf'] as const

/** Content types ID_DOCUMENT_ACCEPTED_TYPES allows that a browser <img>/next/image can actually decode inline. */
export const ID_DOCUMENT_PREVIEWABLE_TYPES = ['image/jpeg', 'image/png'] as const

/** Mirrors kyc.RequiredDocTypes — Submit is rejected until every one is uploaded. */
export const REQUIRED_DOC_TYPES = ['id_front', 'id_back', 'selfie_up', 'selfie_down', 'selfie_left', 'selfie_right'] as const

/** Fallback content type for recorded selfie pose clips when no candidate below is supported. */
export const SELFIE_CLIP_CONTENT_TYPE = 'video/webm'

/** Preferred `MediaRecorder` mime types, checked in order via `MediaRecorder.isTypeSupported`. Safari/iOS often lacks VP8/VP9 webm support and needs the mp4 fallback. */
export const SELFIE_CLIP_MIME_CANDIDATES = [
  'video/webm;codecs=vp9', 'video/webm;codecs=vp8', 'video/webm', 'video/mp4',
] as const

/** Same contact used on /privacy and /terms — one address for user-facing support asks. */
export const SUPPORT_EMAIL = 'dpo@aoctech.app'

/** The 27 Brazilian UF codes, in the order the state picker lists them. */
export const BRAZILIAN_STATES = [
  'AC', 'AL', 'AP', 'AM', 'BA', 'CE', 'DF', 'ES', 'GO',
  'MA', 'MT', 'MS', 'MG', 'PA', 'PB', 'PR', 'PE', 'PI',
  'RJ', 'RN', 'RS', 'RO', 'RR', 'SC', 'SP', 'SE', 'TO',
] as const
