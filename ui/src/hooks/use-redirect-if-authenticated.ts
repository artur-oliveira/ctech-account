'use client'

import { useEffect } from 'react'
import { useRouter } from 'next/navigation'
import { useAuthStore } from '@/store/auth'
import { API_URL } from '@/lib/axios'
import { requestsForcedReauth, sanitizeContinue } from '@/lib/safe-redirect'

const API_PATH_PREFIX = '/v1.0/'

/**
 * Client-side guard for public auth pages (login, register, forgot/reset
 * password). Once auth state has settled (`isInitialized`) and an access token
 * is present, it redirects away from the page — to the sanitized `continue`
 * target when provided, otherwise to the default account destination.
 *
 * Exception: if `continue` itself demands a forced re-authentication
 * (`max_age=0`, e.g. a downstream app's step-up flow), this guard must NOT
 * bounce past the login form using a stale in-memory access token — that
 * would silently skip the fresh login/MFA proof the caller is asking for.
 *
 * This is the inverse of the guard in `app/account/layout.tsx`.
 */
export function useRedirectIfAuthenticated(rawContinue?: string | null) {
  const router = useRouter()
  const { accessToken, isInitialized } = useAuthStore()

  useEffect(() => {
    if (!isInitialized || !accessToken) return
    if (requestsForcedReauth(rawContinue)) return
    const target = sanitizeContinue(rawContinue)
    if (target.startsWith(API_PATH_PREFIX)) {
      window.location.href = `${API_URL}${target}`
    } else {
      router.replace(target)
    }
  }, [isInitialized, accessToken, rawContinue, router])
}
