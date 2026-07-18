import axios from 'axios'
import { api } from './axios'
import type { KYCDocumentType, KYCStatus, KYCSubmission, OAuthClient, PresignedUpload, TermsPending } from './types'

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

/**
 * Token-authenticated acceptance, used by the interstitial: a Google sign-up
 * that has no session yet, or an account re-gated at /authorize by a version
 * bump. The server recomputes what is actually pending — these flags only
 * confirm what the user was shown.
 */
export async function acceptTermsAPI(token: string, accepted: TermsPending) {
  const { data } = await api.post<{ redirect: string }>('/v1.0/auth/accept-terms', {
    token,
    accept_tos: accepted.tos,
    accept_privacy: accepted.privacy,
  })
  return data
}

/** Bearer-authenticated acceptance, used by the in-app gate on /account. */
export async function acceptPendingTermsAPI(accepted: TermsPending) {
  const { data } = await api.post<{ terms_pending: TermsPending }>('/v1.0/account/terms/accept', {
    accept_tos: accepted.tos,
    accept_privacy: accepted.privacy,
  })
  return data
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

/** Unlinks the bound Google identity. Step-up gated server-side; refused for passwordless accounts. */
export async function unlinkGoogleAPI() {
  await api.delete('/v1.0/account/link/google')
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

export type OAuthClientPayload = {
  name: string
  redirect_uris: string[]
  allowed_scopes: string[]
  audience?: string[]
}

export async function createOAuthClientAPI(body: OAuthClientPayload & { client_type: 'public' | 'confidential' }) {
  const { data } = await api.post<OAuthClient>('/v1.0/account/oauth-clients', body)
  return data
}

export async function updateOAuthClientAPI(clientId: string, body: OAuthClientPayload) {
  const { data } = await api.put<OAuthClient>(`/v1.0/account/oauth-clients/${clientId}`, body)
  return data
}

export async function deleteOAuthClientAPI(clientId: string) {
  await api.delete(`/v1.0/account/oauth-clients/${clientId}`)
}

export async function consentDecisionAPI(req: string, approved: boolean) {
  const { data } = await api.post<{ redirect_to: string }>('/v1.0/authorize/consent', {
    req,
    approved,
  })
  return data
}

export async function revokeConsentAPI(clientId: string) {
  await api.delete(`/v1.0/account/consents/${clientId}`)
}

export async function regenerateOAuthClientSecretAPI(clientId: string) {
  const { data } = await api.post<{ client_secret: string }>(
    `/v1.0/account/oauth-clients/${clientId}/regenerate-secret`,
  )
  return data
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

export async function stepUpTOTPAPI(code: string) {
  await api.post('/v1.0/auth/step-up', { method: 'totp', code })
}

export async function beginStepUpPasskeyAPI(): Promise<{ session_token: string; options: string }> {
  const { data } = await api.post('/v1.0/auth/step-up/passkeys/begin')
  return data
}

export async function completeStepUpPasskeyAPI(sessionToken: string, credential: unknown) {
  await api.post(
    `/v1.0/auth/step-up/passkeys/complete?session_token=${encodeURIComponent(sessionToken)}`,
    credential,
  )
}

export async function submitKYCAPI(payload: KYCSubmission): Promise<KYCStatus> {
  const { data } = await api.post<KYCStatus>('/v1.0/account/kyc', payload)
  return data
}

async function presignKYCDocumentAPI(type: KYCDocumentType, contentType: string): Promise<PresignedUpload> {
  const { data } = await api.post<PresignedUpload>('/v1.0/account/kyc/documents', {
    type,
    content_type: contentType,
  })
  return data
}

async function confirmKYCDocumentAPI(documentID: string, type: KYCDocumentType): Promise<KYCStatus> {
  const { data } = await api.post<KYCStatus>('/v1.0/account/kyc/documents/confirm', {
    document_id: documentID,
    type,
  })
  return data
}

/**
 * Uploads an identity document: presign → PUT straight to S3 → confirm.
 *
 * The PUT deliberately bypasses the `api` instance: its interceptor would
 * attach our bearer token, which S3 rejects as it is not part of the signature.
 */
export async function uploadKYCDocumentAPI(file: File, type: KYCDocumentType): Promise<KYCStatus> {
  const presigned = await presignKYCDocumentAPI(type, file.type)

  await axios.put(presigned.upload_url, file, {
    headers: { 'Content-Type': presigned.content_type },
  })

  return confirmKYCDocumentAPI(presigned.document_id, type)
}
