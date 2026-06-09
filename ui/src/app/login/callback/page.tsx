'use client'

import { Suspense, useEffect, useState, useRef } from 'react'
import { useRouter, useSearchParams } from 'next/navigation'
import { useTranslation } from 'react-i18next'
import axios from 'axios'
import { useAuthStore } from '@/store/auth'
import { API_URL, CLIENT_ID } from '@/lib/axios'

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
      const storedState = sessionStorage.getItem('pkce_state')
      const codeVerifier = sessionStorage.getItem('pkce_verifier')
      const continueURL = sessionStorage.getItem('continue_url') ?? '/account'

      if (!code || !codeVerifier) {
        setError(t('callback.missingCode'))
        return
      }
      if (state !== storedState) {
        setError(t('callback.invalidState'))
        return
      }

      sessionStorage.removeItem('pkce_state')
      sessionStorage.removeItem('pkce_verifier')
      sessionStorage.removeItem('continue_url')

      const redirectURI = `${window.location.origin}/login/callback`
      const body = new URLSearchParams({
        grant_type: 'authorization_code',
        code,
        client_id: CLIENT_ID,
        redirect_uri: redirectURI,
        code_verifier: codeVerifier,
      })

      try {
        const { data } = await axios.post(`${API_URL}/v1.0/token`, body, {
          withCredentials: true,
          headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        })
        useAuthStore.getState().setAccessToken(data.access_token)
        if (continueURL.startsWith('/v1.0/')) {
          window.location.href = `${API_URL}${continueURL}`
        } else {
          router.replace(continueURL)
        }
      } catch {
        setError(t('callback.tokenError'))
      }
    })()
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  if (error) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-muted/40 p-4">
        <div className="text-center space-y-4">
          <p className="text-destructive text-sm">{error}</p>
          <a href="/login" className="text-sm underline text-foreground">{t('callback.backToSignIn')}</a>
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
