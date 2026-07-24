import { oauthClient } from './oauth-client'
import { USE_MOCK, MOCK_ACCESS_TOKEN } from './mock'
import { API_URL } from './env'
import { useAuthStore } from '@/store/auth'

export async function startOAuthFlow(continueURL: string = '/account'): Promise<void> {
  // A continue target that is itself `/v1.0/authorize` (another client's
  // step-up call, e.g. wallet's withdrawal max_age=0 request) must be
  // followed directly. Routing it through this app's own client_id=accounts
  // OAuth round-trip first burns the caller's max_age window before the
  // browser ever gets back to it, bouncing step-up back to /login.
  if (continueURL.startsWith('/v1.0/')) {
    window.location.href = `${API_URL}${continueURL}`
    return
  }

  // No Go API to redirect to — hand the SPA a token directly, same effect as
  // a completed OAuth round-trip, so the account pages render for critique.
  if (USE_MOCK) {
    useAuthStore.getState().setAccessToken(MOCK_ACCESS_TOKEN)
    window.location.href = continueURL
    return
  }

  await oauthClient.startOAuthFlow(continueURL)
}
