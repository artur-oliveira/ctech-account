import { OAuthClient } from '@aoctech/auth-client'
import { USE_MOCK } from './mock'
import { API_URL, CLIENT_ID } from './env'

export const oauthClient = new OAuthClient({
  baseUrl: API_URL,
  clientId: CLIENT_ID,
  redirectUri: typeof window !== 'undefined' ? `${window.location.origin}/login/callback` : '',
  scope: 'openid profile email',
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
