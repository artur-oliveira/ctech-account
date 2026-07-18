'use client'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { useAuthStore } from '@/store/auth'
import { oauthClient, hasAuthHint, clearAuthHint } from '@/lib/oauth-client'
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

    // oauthClient.refresh() is guarded + single-flight and safe to call at boot
    // (see its doc comment) — this used to hand-roll the same /v1.0/token POST.
    oauthClient.refresh().then((result) => {
      if (result) {
        store.setAccessToken(result.accessToken)
        store.setInitialized()
        return
      }
      store.clearAuth()
      clearAuthHint()
      const pathname = window.location.pathname
      // An SSO session may exist without a SPA refresh token yet (e.g. right
      // after social login, or after a login initiated by another app). Let the
      // /authorize flow bootstrap the tokens — never from auth pages, so a dead
      // session can't cause a redirect loop.
      if (!isAuthPage(pathname)) {
        void startOAuthFlow(pathname + window.location.search)
        return
      }
      store.setInitialized()
    })
  }, [])

  return null
}
