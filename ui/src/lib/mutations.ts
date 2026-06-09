import { api } from './axios'

export async function loginAPI(email: string, password: string) {
  const { data } = await api.post<{
    requires_mfa: boolean
    mfa_token?: string
    mfa_methods?: string[]
  }>('/v1.0/auth/login', { email, password })
  return data
}

export async function mfaChallengeAPI(mfaToken: string, code: string) {
  await api.post('/v1.0/auth/mfa/challenge', { mfa_token: mfaToken, code })
}

export async function registerAPI(body: {
  email: string
  password: string
  first_name: string
  last_name: string
}) {
  const { data } = await api.post('/v1.0/auth/register', body)
  return data
}

export async function logoutAPI() {
  await api.post('/v1.0/auth/logout')
}

export async function updateProfileAPI(body: {
  first_name: string
  last_name: string
  display_name?: string
}) {
  const { data } = await api.put('/v1.0/account/profile', body)
  return data
}

export async function changePasswordAPI(body: {
  current_password: string
  new_password: string
}) {
  await api.put('/v1.0/account/password', body)
}

export async function revokeSessionAPI(sessionId: string) {
  await api.delete(`/v1.0/account/sessions/${sessionId}`)
}

export async function revokeAllSessionsAPI() {
  await api.delete('/v1.0/account/sessions')
}

export async function createAPIKeyAPI(body: {
  name: string
  scopes: string[]
  expires_in_days: number
}) {
  const { data } = await api.post<{ raw_key?: string; key?: string }>('/v1.0/account/api-keys', body)
  return data
}

export async function revokeAPIKeyAPI(keyId: string) {
  await api.delete(`/v1.0/account/api-keys/${keyId}`)
}

export async function confirmTOTPAPI(code: string) {
  const { data } = await api.post<{ backup_codes: string[] }>('/v1.0/account/mfa/totp/confirm', { code })
  return data
}

export async function removeTOTPAPI() {
  await api.delete('/v1.0/account/mfa/totp')
}

export async function regenerateBackupCodesAPI() {
  const { data } = await api.post<{ backup_codes: string[] }>('/v1.0/account/mfa/totp/backup-codes')
  return data
}

export async function removePasskeyAPI(passkeyId: string) {
  await api.delete(`/v1.0/account/mfa/passkeys/${passkeyId}`)
}

export async function beginPasskeyRegistrationAPI(name: string) {
  const { data } = await api.post<{ session_token: string; name: string; options: string }>(
    '/v1.0/account/mfa/passkeys/register/begin',
    { name },
  )
  return data
}

export async function completePasskeyRegistrationAPI(
  sessionToken: string,
  name: string,
  credential: unknown,
) {
  await api.post(
    `/v1.0/account/mfa/passkeys/register/complete?session_token=${encodeURIComponent(sessionToken)}&name=${encodeURIComponent(name)}`,
    credential,
  )
}

export async function forgotPasswordAPI(email: string) {
  await api.post('/v1.0/auth/forgot-password', { email })
}

export async function resetPasswordAPI(token: string, newPassword: string) {
  await api.post('/v1.0/auth/reset-password', { token, new_password: newPassword })
}

export async function verifyEmailAPI(token: string) {
  await api.post('/v1.0/auth/verify-email', { token })
}

export async function resendVerificationAPI(email: string) {
  await api.post('/v1.0/auth/resend-verification', { email })
}

export async function beginPasskeyAuthAPI() {
  const { data } = await api.post<{ session_token: string; options: string }>(
    '/v1.0/auth/passkeys/authenticate/begin',
  )
  return data
}

export async function completePasskeyAuthAPI(sessionToken: string, credential: unknown) {
  const { data } = await api.post(
    `/v1.0/auth/passkeys/authenticate/complete?session_token=${encodeURIComponent(sessionToken)}`,
    credential,
  )
  return data
}

export async function beginMFAPasskeyAPI(mfaToken: string) {
  const { data } = await api.post<{ session_token: string; options: string }>(
    '/v1.0/auth/mfa/passkey/begin',
    { mfa_token: mfaToken },
  )
  return data
}

export async function completeMFAPasskeyAPI(mfaToken: string, sessionToken: string, credential: unknown) {
  await api.post(
    `/v1.0/auth/mfa/passkey/complete?mfa_token=${encodeURIComponent(mfaToken)}&session_token=${encodeURIComponent(sessionToken)}`,
    credential,
  )
}
