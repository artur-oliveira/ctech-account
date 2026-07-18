import { oauthClient } from './oauth-client'
import { USE_MOCK, MOCK_ACCESS_TOKEN } from './mock'
import { useAuthStore } from '@/store/auth'

export async function startOAuthFlow(continueURL: string = '/account'): Promise<void> {
  // No Go API to redirect to — hand the SPA a token directly, same effect as
  // a completed OAuth round-trip, so the account pages render for critique.
  if (USE_MOCK) {
    useAuthStore.getState().setAccessToken(MOCK_ACCESS_TOKEN)
    window.location.href = continueURL
    return
  }

  await oauthClient.startOAuthFlow(continueURL)
}
