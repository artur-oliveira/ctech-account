import { api, isAxiosError } from './axios'
import type { User, Session, APIKey, Passkey } from './types'

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

export async function fetchPasskeys(): Promise<Passkey[]> {
  const { data } = await api.get<{ passkeys: Passkey[] }>('/v1.0/account/mfa/passkeys')
  return data.passkeys ?? []
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
