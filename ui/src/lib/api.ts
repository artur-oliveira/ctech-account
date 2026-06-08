import 'server-only'
import { cookies } from 'next/headers'
import { redirect } from 'next/navigation'
import type { User, Session, APIKey, Passkey } from './types'

const API_URL = process.env.API_URL ?? 'http://localhost:8080'

async function fetchAPI(
  path: string,
  init?: RequestInit,
  options?: { noRedirectOn401?: boolean },
): Promise<Response> {
  const cookieStore = await cookies()
  const at = cookieStore.get('ctech_at')?.value

  const res = await fetch(`${API_URL}${path}`, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(at ? { Authorization: `Bearer ${at}` } : {}),
      ...(init?.headers as Record<string, string>),
    },
  })

  if (res.status === 401 && !options?.noRedirectOn401) {
    redirect('/login')
  }

  return res
}

export async function getProfile(): Promise<User | null> {
  const res = await fetchAPI('/v1.0/account/profile')
  if (!res.ok) return null
  return res.json()
}

export async function getSessions(): Promise<Session[]> {
  const res = await fetchAPI('/v1.0/account/sessions')
  if (!res.ok) return []
  const data = await res.json()
  return data.sessions ?? []
}

export async function getAPIKeys(): Promise<APIKey[]> {
  const res = await fetchAPI('/v1.0/account/api-keys')
  if (!res.ok) return []
  const data = await res.json()
  return data.api_keys ?? []
}

export async function getPasskeys(): Promise<Passkey[]> {
  const res = await fetchAPI('/v1.0/account/mfa/passkeys')
  if (!res.ok) return []
  const data = await res.json()
  return data.passkeys ?? []
}

export async function getTOTPSetup(): Promise<{ provisioning_uri: string } | null> {
  const res = await fetchAPI('/v1.0/account/mfa/totp/setup')
  if (!res.ok) return null
  return res.json()
}

export { API_URL }
