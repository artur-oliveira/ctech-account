import { api, isAxiosError } from './axios'
import type { User, Session, APIKey, Passkey, OAuthClient, ConsentGrant, ScopeService, ActivityPage, KYCStatus } from './types'

export async function fetchProfile(): Promise<User> {
  const { data } = await api.get<User>('/v1.0/account/profile')
  return data
}

export async function fetchSessions(): Promise<Session[]> {
  const { data } = await api.get<{ sessions: Session[] }>('/v1.0/account/sessions')
  return data.sessions ?? []
}

export async function fetchAPIKeys(): Promise<APIKey[]> {
  const { data } = await api.get<{ api_keys: APIKey[] }>('/v1.0/account/api-keys')
  return data.api_keys ?? []
}

export async function fetchOAuthClients(): Promise<OAuthClient[]> {
  const { data } = await api.get<{ oauth_clients: OAuthClient[] }>('/v1.0/account/oauth-clients')
  return data.oauth_clients ?? []
}

export async function fetchScopeCatalog(): Promise<ScopeService[]> {
  const { data } = await api.get<{ services: ScopeService[] }>('/v1.0/scopes')
  return data.services ?? []
}

export async function fetchConsents(): Promise<ConsentGrant[]> {
  const { data } = await api.get<{ consents: ConsentGrant[] }>('/v1.0/account/consents')
  return data.consents ?? []
}

export async function fetchPasskeys(): Promise<Passkey[]> {
  const { data } = await api.get<{ passkeys: Passkey[] }>('/v1.0/account/mfa/passkeys')
  return data.passkeys ?? []
}

export async function fetchTOTPStatus(): Promise<{ enabled: boolean }> {
  const { data } = await api.get<{ enabled: boolean }>('/v1.0/account/mfa/totp')
  return data
}

export async function fetchTOTPSetup(): Promise<{ provisioning_uri: string } | null> {
  try {
    const { data } = await api.get<{ provisioning_uri: string }>('/v1.0/account/mfa/totp/setup')
    return data
  } catch (error) {
    // 409 = already configured; return null so the page shows "already set up"
    if (isAxiosError(error) && error.response?.status === 409) return null
    throw error
  }
}

const ACTIVITY_PAGE_SIZE = 25

export async function fetchActivity(cursor: string): Promise<ActivityPage> {
  const params = new URLSearchParams({ limit: String(ACTIVITY_PAGE_SIZE) })
  if (cursor) params.set('cursor', cursor)
  const { data } = await api.get<ActivityPage>(`/v1.0/account/activity?${params}`)
  return { events: data.events ?? [], next_cursor: data.next_cursor ?? '' }
}

export async function fetchKYC(): Promise<KYCStatus> {
  const { data } = await api.get<KYCStatus>('/v1.0/account/kyc')
  return data
}
