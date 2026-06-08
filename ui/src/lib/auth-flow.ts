import { generatePKCE, generateState } from './pkce'
import { API_URL, CLIENT_ID } from './axios'

export async function startOAuthFlow(continueURL: string = '/account'): Promise<void> {
  const { codeVerifier, codeChallenge } = await generatePKCE()
  const state = generateState()
  const redirectURI = `${window.location.origin}/login/callback`

  sessionStorage.setItem('pkce_verifier', codeVerifier)
  sessionStorage.setItem('pkce_state', state)
  sessionStorage.setItem('continue_url', continueURL)

  const url = new URL(`${API_URL}/v1.0/authorize`)
  url.searchParams.set('client_id', CLIENT_ID)
  url.searchParams.set('redirect_uri', redirectURI)
  url.searchParams.set('response_type', 'code')
  url.searchParams.set('scope', 'openid profile email')
  url.searchParams.set('state', state)
  url.searchParams.set('code_challenge', codeChallenge)
  url.searchParams.set('code_challenge_method', 'S256')

  window.location.href = url.toString()
}
