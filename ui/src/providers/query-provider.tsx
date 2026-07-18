'use client'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import axios from 'axios'
import { useAuthStore } from '@/store/auth'
import { API_URL, CLIENT_ID } from '@/lib/env'
import { hasAuthHint, clearAuthHint } from '@/lib/oauth-client'
import { startOAuthFlow } from '@/lib/auth-flow'

/** Pages where a failed silent refresh must never auto-start an OAuth redirect. */
const AUTH_PAGES = ['/login', '/register', '/forgot-password', '/reset-password', '/verify-email', '/consent', '/accept-terms']

function isAuthPage(pathname: string): boolean {
  return AUTH_PAGES.some((p) => pathname === p || pathname.startsWith(`${p}/`))
}

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
})

export function QueryProvider({ children }: { children: React.ReactNode }) {
  return (
    <QueryClientProvider client={queryClient}>
      <AuthInitializer />
      {children}
    </QueryClientProvider>
  )
}

function AuthInitializer() {
  const initialized = useRef(false)

  useEffect(() => {
    if (initialized.current) return
    initialized.current = true

    const store = useAuthStore.getState()

    // Without the hint cookie there is no session to refresh — skip the request
    // entirely instead of burning a guaranteed failure against the /token rate limit.
    if (!hasAuthHint()) {
      store.clearAuth()
      store.setInitialized()
      return
    }

    const params = new URLSearchParams({ grant_type: 'refresh_token', client_id: CLIENT_ID })

    axios
      .post(`${API_URL}/v1.0/token`, params, {
        withCredentials: true,
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      })
      .then(({ data }) => {
        store.setAccessToken(data.access_token)
        store.setInitialized()
      })
      .catch((err: unknown) => {
        store.clearAuth()
        const status = axios.isAxiosError(err) ? err.response?.status : undefined
        const pathname = window.location.pathname
        // An SSO session may exist without a SPA refresh token yet (e.g. right
        // after social login, or after a login initiated by another app). Let the
        // /authorize flow bootstrap the tokens — never from auth pages, so a dead
        // session can't cause a redirect loop.
        if ((status === 400 || status === 401) && !isAuthPage(pathname)) {
          void startOAuthFlow(pathname + window.location.search)
          return
        }
        if (status === 400 || status === 401) clearAuthHint()
        store.setInitialized()
      })
  }, [])

  return null
}
