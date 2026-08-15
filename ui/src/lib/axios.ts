import axios, {isAxiosError as _isAxiosError} from 'axios'
import {useAuthStore} from '@/store/auth'
import {useStepUpStore} from '@/store/step-up'
import {oauthClient, hasAuthHint, clearAuthHint} from '@/lib/oauth-client'
import {USE_MOCK, mockAdapter} from '@/lib/mock'
import {API_URL, CLIENT_ID} from '@/lib/env'

export {API_URL, CLIENT_ID}

/** RFC 7807 problem type slug the API answers on step-up-protected routes. */
const STEP_UP_PROBLEM = 'step-up-required'
const INSUFFICIENT_SCOPE_PROBLEM = 'insufficient-scope'

export const api = axios.create({
  baseURL: API_URL,
  withCredentials: true,
  headers: {'Content-Type': 'application/json'},
  adapter: USE_MOCK ? mockAdapter : undefined,
})

api.interceptors.request.use((config) => {
  const token = useAuthStore.getState().accessToken
  if (token) config.headers.Authorization = `Bearer ${token}`
  return config
})

function isStepUpRequired(error: unknown): boolean {
  if (!_isAxiosError(error) || error.response?.status !== 403) return false
  const problem = error.response.data as { type?: string } | undefined
  return typeof problem?.type === 'string' && problem.type.endsWith(STEP_UP_PROBLEM)
}

function isInsufficientScope(error: unknown): boolean {
  if (!_isAxiosError(error) || error.response?.status !== 403) return false
  const problem = error.response.data as { type?: string } | undefined
  return typeof problem?.type === 'string' && problem.type.endsWith(INSUFFICIENT_SCOPE_PROBLEM)
}

api.interceptors.response.use(
  (response) => response,
  async (error) => {
    const original = error.config
    const url: string = original?.url ?? ''
    const isAuthEndpoint = url.includes('/auth/') || url.includes('/token')
    // Only attempt a silent refresh when the hint cookie says a session may exist —
    // a doomed refresh both fails the caller and burns the /token rate limit.
    // oauthClient.refresh() is guarded + single-flight, so concurrent 401s here
    // (and a boot-time refresh from AuthInitializer) never fire duplicate requests.
    if (_isAxiosError(error) && error.response?.status === 401 && !original._retry && !isAuthEndpoint && hasAuthHint()) {
      original._retry = true
      const result = await oauthClient.refresh()
      if (result) {
        useAuthStore.getState().setAccessToken(result.accessToken)
        original.headers.Authorization = `Bearer ${result.accessToken}`
        return api(original)
      }
      useAuthStore.getState().clearAuth()
      clearAuthHint()
      // A failed refresh must restart the document with all in-memory auth state cleared.
      if (typeof window !== 'undefined') window.location.href = '/login'
      return Promise.reject(error)
    }

    // Account Resource Server rollout: an old refresh grant may predate the
    // explicit account:* permissions. The trusted self client reconciles those
    // scopes during refresh, so refresh once and retry an insufficient_scope
    // response. Other clients remain clamped to their original grant.
    if (isInsufficientScope(error) && !original._scopeRetry && !isAuthEndpoint && hasAuthHint()) {
      original._scopeRetry = true
      const result = await oauthClient.refresh()
      if (result) {
        useAuthStore.getState().setAccessToken(result.accessToken)
        original.headers.Authorization = `Bearer ${result.accessToken}`
        return api(original)
      }
    }

    // Step-up gate: open the challenge dialog, and after the user proves MFA
    // (dialog already refreshed the token) retry the original request once.
    if (isStepUpRequired(error) && !original._stepUpRetry) {
      original._stepUpRetry = true
      await useStepUpStore.getState().request()
      original.headers.Authorization = `Bearer ${useAuthStore.getState().accessToken}`
      return api(original)
    }

    return Promise.reject(error)
  },
)

export {_isAxiosError as isAxiosError}
