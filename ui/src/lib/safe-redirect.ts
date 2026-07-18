/**
 * Default destination for any authenticated user or invalid `continue` value.
 */
export const DEFAULT_REDIRECT = '/account'

/**
 * Matches a safe internal path: exactly one leading `/` followed by a character
 * that is neither `/` nor `\`. This rejects protocol-relative (`//evil.com`)
 * and backslash (`/\evil.com`) open-redirect payloads that would otherwise pass
 * a naive `startsWith('/')` check.
 */
const SAFE_PATH_PATTERN = /^\/[^/\\]/

/**
 * Decodes a raw `continue` value into its underlying path+query. The authorize
 * endpoint base64url-encodes it (never starts with `/`); other callers pass a
 * plain path. Returns null if base64 decoding fails.
 */
function decodeContinue(raw: string): string | null {
  if (raw.startsWith('/')) return raw
  try {
    return atob(raw.replace(/-/g, '+').replace(/_/g, '/'))
  } catch {
    return null
  }
}

/**
 * Normalises and validates a raw `continue` query parameter into a safe
 * same-origin path. Anything that is not a safe internal path (missing,
 * external, protocol-relative, or backslash-prefixed) collapses to
 * {@link DEFAULT_REDIRECT}.
 */
export function sanitizeContinue(raw: string | null | undefined): string {
  if (!raw) return DEFAULT_REDIRECT
  const candidate = decodeContinue(raw)
  if (candidate === null) return DEFAULT_REDIRECT
  return SAFE_PATH_PATTERN.test(candidate) ? candidate : DEFAULT_REDIRECT
}

/**
 * Reports whether the `continue` target itself demands a forced
 * re-authentication (`/v1.0/authorize?...&max_age=0`) — e.g. a downstream
 * app's step-up flow. When true, the public auth pages must NOT silently
 * bounce an already-authenticated browser past the login form: that would
 * defeat the whole point of forcing a fresh login/MFA proof (see
 * useRedirectIfAuthenticated).
 */
export function requestsForcedReauth(raw: string | null | undefined): boolean {
  if (!raw) return false
  const candidate = decodeContinue(raw)
  if (candidate === null) return false
  const qIndex = candidate.indexOf('?')
  if (qIndex === -1) return false
  return new URLSearchParams(candidate.slice(qIndex + 1)).get('max_age') === '0'
}
