import { describe, expect, it } from 'vitest'
import { DEFAULT_REDIRECT, requestsForcedReauth, sanitizeContinue } from './safe-redirect'

function b64url(s: string): string {
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

describe('sanitizeContinue', () => {
  it('passes through a plain safe path', () => {
    expect(sanitizeContinue('/account')).toBe('/account')
  })

  it('decodes a base64url-encoded authorize URL', () => {
    const encoded = b64url('/v1.0/authorize?client_id=wallet&max_age=0')
    expect(sanitizeContinue(encoded)).toBe('/v1.0/authorize?client_id=wallet&max_age=0')
  })

  it('rejects protocol-relative and backslash payloads', () => {
    expect(sanitizeContinue('//evil.com')).toBe(DEFAULT_REDIRECT)
    expect(sanitizeContinue('/\\evil.com')).toBe(DEFAULT_REDIRECT)
  })

  it('falls back to the default for missing or undecodable input', () => {
    expect(sanitizeContinue(null)).toBe(DEFAULT_REDIRECT)
    expect(sanitizeContinue('not-valid-base64!!')).toBe(DEFAULT_REDIRECT)
  })
})

describe('requestsForcedReauth', () => {
  it('is true for an authorize URL carrying max_age=0', () => {
    const encoded = b64url('/v1.0/authorize?client_id=wallet&max_age=0&scope=openid')
    expect(requestsForcedReauth(encoded)).toBe(true)
  })

  it('is false without max_age, or with a non-zero max_age', () => {
    expect(requestsForcedReauth(b64url('/v1.0/authorize?client_id=wallet'))).toBe(false)
    expect(requestsForcedReauth(b64url('/v1.0/authorize?client_id=wallet&max_age=300'))).toBe(false)
  })

  it('is false for a plain path (no query at all)', () => {
    expect(requestsForcedReauth('/account')).toBe(false)
  })

  it('is false for missing input', () => {
    expect(requestsForcedReauth(null)).toBe(false)
    expect(requestsForcedReauth(undefined)).toBe(false)
  })
})
