'use client'

import { Suspense, useEffect, useState, useRef } from 'react'
import Link from 'next/link'
import { useRouter, useSearchParams } from 'next/navigation'
import { useTranslation } from 'react-i18next'
import { useAuthStore } from '@/store/auth'
import { API_URL } from '@/lib/env'
import { oauthClient } from '@/lib/oauth-client'
import { sanitizeContinue } from '@/lib/safe-redirect'

function CallbackHandler() {
  const { t } = useTranslation()
  const router = useRouter()
  const params = useSearchParams()
  const [error, setError] = useState('')
  const attempted = useRef(false)

  useEffect(() => {
    if (attempted.current) return
    attempted.current = true

    void (async () => {
      const code = params.get('code')
      const state = params.get('state')

      if (!code || !state) {
        setError(t('callback.missingCode'))
        return
      }

      try {
        const { accessToken, returnTo } = await oauthClient.exchangeCode(code, state)
        const continueURL = sanitizeContinue(returnTo)
        useAuthStore.getState().setAccessToken(accessToken)
        if (continueURL.startsWith('/v1.0/')) {
          window.location.href = `${API_URL}${continueURL}`
        } else {
          router.replace(continueURL)
        }
      } catch (err) {
        setError(err instanceof Error && err.message === 'OAuth state mismatch'
          ? t('callback.invalidState')
          : t('callback.tokenError'))
      }
    })()
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  if (error) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-muted/40 p-4">
        <div className="text-center space-y-4">
          <p className="text-destructive text-sm">{error}</p>
          <Link href="/login" className="text-sm underline text-foreground">{t('callback.backToSignIn')}</Link>
        </div>
      </div>
    )
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-muted/40">
      <p className="animate-pulse text-muted-foreground text-sm">{t('callback.completing')}</p>
    </div>
  )
}

export default function CallbackPage() {
  return (
    <Suspense fallback={<div className="min-h-screen flex items-center justify-center" />}>
      <CallbackHandler />
    </Suspense>
  )
}
