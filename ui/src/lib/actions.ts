'use server'

import { cookies } from 'next/headers'
import { revalidatePath } from 'next/cache'
import { redirect } from 'next/navigation'

const API_URL = process.env.API_URL ?? 'http://localhost:8080'

async function bearer(): Promise<string | null> {
  const store = await cookies()
  return store.get('ctech_at')?.value ?? null
}

async function apiFetch(path: string, init?: RequestInit): Promise<Response> {
  const at = await bearer()
  return fetch(`${API_URL}${path}`, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(at ? { Authorization: `Bearer ${at}` } : {}),
      ...(init?.headers as Record<string, string>),
    },
  })
}

export async function updateProfile(_prev: unknown, formData: FormData) {
  const res = await apiFetch('/v1.0/account/profile', {
    method: 'PUT',
    body: JSON.stringify({
      first_name: formData.get('first_name'),
      last_name: formData.get('last_name'),
      display_name: formData.get('display_name'),
    }),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({}))
    return { error: err.detail ?? 'Update failed.' }
  }
  revalidatePath('/account/profile')
  return { success: true }
}

export async function changePassword(_prev: unknown, formData: FormData) {
  const res = await apiFetch('/v1.0/account/password', {
    method: 'PUT',
    body: JSON.stringify({
      current_password: formData.get('current_password'),
      new_password: formData.get('new_password'),
    }),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({}))
    return { error: err.detail ?? 'Password change failed.' }
  }
  return { success: true }
}

export async function revokeSession(sessionId: string) {
  const res = await apiFetch(`/v1.0/account/sessions/${sessionId}`, { method: 'DELETE' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({}))
    return { error: err.detail ?? 'Failed to revoke session.' }
  }
  revalidatePath('/account/sessions')
  return { success: true }
}

export async function revokeAllSessions() {
  const res = await apiFetch('/v1.0/account/sessions', { method: 'DELETE' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({}))
    return { error: err.detail ?? 'Failed to revoke sessions.' }
  }
  revalidatePath('/account/sessions')
  return { success: true }
}

export async function createAPIKey(_prev: unknown, formData: FormData) {
  const scopes = formData.getAll('scopes') as string[]
  const expiresInDays = parseInt(formData.get('expires_in_days') as string) || 0

  const res = await apiFetch('/v1.0/account/api-keys', {
    method: 'POST',
    body: JSON.stringify({
      name: formData.get('name'),
      scopes: scopes.length > 0 ? scopes : ['read'],
      expires_in_days: expiresInDays,
    }),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({}))
    return { error: err.detail ?? 'Failed to create API key.' }
  }
  const data = await res.json()
  revalidatePath('/account/api-keys')
  return { success: true, key: data.raw_key ?? data.key ?? data }
}

export async function revokeAPIKey(keyId: string) {
  const res = await apiFetch(`/v1.0/account/api-keys/${keyId}`, { method: 'DELETE' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({}))
    return { error: err.detail ?? 'Failed to revoke API key.' }
  }
  revalidatePath('/account/api-keys')
  return { success: true }
}

export async function confirmTOTP(_prev: unknown, formData: FormData) {
  const res = await apiFetch('/v1.0/account/mfa/totp/confirm', {
    method: 'POST',
    body: JSON.stringify({ code: formData.get('code') }),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({}))
    return { error: err.detail ?? 'Invalid code.' }
  }
  const data = await res.json()
  revalidatePath('/account/security')
  return { success: true, backup_codes: data.backup_codes ?? [] }
}

export async function removeTOTP() {
  const res = await apiFetch('/v1.0/account/mfa/totp', { method: 'DELETE' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({}))
    return { error: err.detail ?? 'Failed to remove TOTP.' }
  }
  revalidatePath('/account/security')
  return { success: true }
}

export async function regenerateBackupCodes() {
  const res = await apiFetch('/v1.0/account/mfa/totp/backup-codes', { method: 'POST' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({}))
    return { error: err.detail ?? 'Failed to regenerate backup codes.' }
  }
  const data = await res.json()
  return { success: true, backup_codes: data.backup_codes ?? [] }
}

export async function removePasskey(passkeyId: string) {
  const res = await apiFetch(`/v1.0/account/mfa/passkeys/${passkeyId}`, { method: 'DELETE' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({}))
    return { error: err.detail ?? 'Failed to remove passkey.' }
  }
  revalidatePath('/account/security/passkeys')
  return { success: true }
}

export async function beginPasskeyRegistration() {
  const res = await apiFetch('/v1.0/account/mfa/passkeys/register/begin', { method: 'POST' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({}))
    return { error: err.detail ?? 'Failed to begin passkey registration.' }
  }
  return { success: true, options: await res.json() }
}

export async function completePasskeyRegistration(credential: unknown) {
  const res = await apiFetch('/v1.0/account/mfa/passkeys/register/complete', {
    method: 'POST',
    body: JSON.stringify(credential),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({}))
    return { error: err.detail ?? 'Passkey registration failed.' }
  }
  revalidatePath('/account/security/passkeys')
  return { success: true }
}

export async function logoutAction() {
  const store = await cookies()
  store.delete('ctech_at')
  store.delete('ctech_rt')
  redirect('/login')
}
