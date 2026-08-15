import { OAuthClient } from '@aoctech/auth-client'
import { USE_MOCK } from './mock'
import { API_URL, CLIENT_ID } from './env'

/** Explicit Resource Server permissions used by the trusted Account SPA. */
export const ACCOUNT_USER_SCOPES = [
  'account:profile:read',
  'account:profile:write',
  'account:security:write',
  'account:sessions:read',
  'account:sessions:revoke',
  'account:activity:read',
  'account:api-keys:read',
  'account:api-keys:write',
  'account:oauth-clients:read',
  'account:oauth-clients:write',
  'account:consents:read',
  'account:consents:revoke',
  'account:mfa:read',
  'account:mfa:write',
  'account:kyc:read',
  'account:kyc:write',
  'account:terms:write',
] as const

export const ACCOUNT_OAUTH_SCOPE = ['openid', 'profile', 'email', ...ACCOUNT_USER_SCOPES].join(' ')

export const oauthClient = new OAuthClient({
  baseUrl: API_URL,
  clientId: CLIENT_ID,
  redirectUri: typeof window !== 'undefined' ? `${window.location.origin}/login/callback` : '',
  scope: ACCOUNT_OAUTH_SCOPE,
})

/**
 * Mock-aware wrapper: there is no real ctech_auth cookie in NEXT_PUBLIC_MOCK_API
 * mode, and the mock adapter answers every API call regardless of it, so the
 * hint check must always pass there — same override `lib/auth-hint.ts` used to do.
 */
export function hasAuthHint(): boolean {
  return USE_MOCK || oauthClient.hasAuthHint()
}

export function clearAuthHint(): void {
  oauthClient.clearAuthHint()
}

/** Closes the OAuth client's BroadcastChannel to prevent memory/event-loop leaks in tests. */
export function close(): void {
  oauthClient.close()
}
