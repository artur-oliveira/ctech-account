'use client'

import { useEffect } from 'react'
import { usePathname } from 'next/navigation'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'

/** Most-specific prefix first — matched top to bottom against the current pathname. */
const ROUTE_TITLES: Array<[string, (t: TFunction) => string]> = [
  ['/account/security/totp', (t) => t('nav.authenticator')],
  ['/account/security/passkeys', (t) => t('nav.passkeys')],
  ['/account/security', (t) => t('nav.security')],
  ['/account/sessions', (t) => t('nav.sessions')],
  ['/account/api-keys', (t) => t('nav.apiKeys')],
  ['/account/oauth-clients', (t) => t('nav.oauthClients')],
  ['/account/connected-apps', (t) => t('nav.connectedApps')],
  ['/account/activity', (t) => t('nav.activity')],
  ['/account/identity', (t) => t('nav.identity')],
  ['/account/profile', (t) => t('nav.profile')],
  ['/account', (t) => t('nav.dashboard')],
  ['/login/mfa', (t) => t('mfa.title')],
  ['/login', (t) => t('login.title')],
  ['/register/verify', (t) => t('verify.title')],
  ['/register', (t) => t('register.title')],
  ['/forgot-password', (t) => t('forgotPassword.title')],
  ['/reset-password', (t) => t('resetPassword.title')],
  ['/accept-terms', (t) => t('acceptTerms.title')],
  ['/verify-email', (t) => t('verifyEmail.title')],
  ['/consent', (t) => t('consent.title', { app: t('consent.unknownApp') })],
  ['/terms', () => 'Termos de Uso'],
  ['/privacy', () => 'Política de Privacidade'],
]

function matchTitle(pathname: string, t: TFunction): string | null {
  for (const [prefix, resolve] of ROUTE_TITLES) {
    if (pathname === prefix || pathname.startsWith(`${prefix}/`)) {
      return resolve(t)
    }
  }
  return null
}

/**
 * Sets `document.title` per route. Static export means every page is a Client
 * Component, so Next's `metadata` export (Server Component only) can't react
 * to route changes — this replicates its `%s | app.name` template manually.
 */
export function RouteTitle() {
  const pathname = usePathname()
  const { t } = useTranslation()

  useEffect(() => {
    const label = matchTitle(pathname, t)
    document.title = label ? `${label} | ${t('app.name')}` : t('app.name')
  }, [pathname, t])

  return null
}
