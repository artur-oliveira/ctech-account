'use client'

import {Suspense, useState, useEffect, SyntheticEvent} from 'react'
import {useSearchParams} from 'next/navigation'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {Input} from '@/components/ui/input'
import {Label} from '@/components/ui/label'
import {Card, CardContent, CardDescription, CardHeader, CardTitle} from '@/components/ui/card'
import {Alert, AlertDescription} from '@/components/ui/alert'
import {Separator} from '@/components/ui/separator'
import {isAxiosError} from '@/lib/axios'
import {startOAuthFlow} from '@/lib/auth-flow'
import {beginMFAPasskeyAPI, completeMFAPasskeyAPI, mfaChallengeAPI} from '@/lib/mutations'
import {buildAssertionCredential} from '@/lib/webauthn'
import {Fingerprint} from 'lucide-react'

function MFAForm() {
  const {t} = useTranslation()
  const params = useSearchParams()
  const continueURL = params.get('continue') ?? '/account'
  const [loading, setLoading] = useState(false)
  const [passkeyLoading, setPasskeyLoading] = useState(false)
  const [error, setError] = useState('')
  const [methods, setMethods] = useState<string[]>([])

  useEffect(() => {
    try {
      setMethods(JSON.parse(sessionStorage.getItem('mfa_methods') ?? '[]'))
    } catch {
      setMethods([])
    }
  }, [])

  const hasPasskey = methods.includes('passkey')
  const hasTOTP = methods.includes('totp')

  async function handlePasskey() {
    setError('')
    setPasskeyLoading(true)
    const mfaToken = sessionStorage.getItem('mfa_token')
    if (!mfaToken) {
      setError(t('errors.sessionExpired'))
      setPasskeyLoading(false)
      return
    }
    try {
      const {session_token, options} = await beginMFAPasskeyAPI(mfaToken)
      const credential = await buildAssertionCredential(options)
      await completeMFAPasskeyAPI(mfaToken, session_token, credential)
      sessionStorage.removeItem('mfa_token')
      sessionStorage.removeItem('mfa_methods')
      await startOAuthFlow(continueURL)
    } catch (err) {
      if (isAxiosError(err)) {
        setError(err.response?.data?.detail ?? t('errors.mfaFailed'))
      } else {
        setError(t('errors.network'))
      }
      setPasskeyLoading(false)
    }
  }

  async function handleTOTP(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    setError('')
    setLoading(true)
    const fd = new FormData(e.currentTarget)
    const mfaToken = sessionStorage.getItem('mfa_token')
    if (!mfaToken) {
      setError(t('errors.sessionExpired'))
      setLoading(false)
      return
    }
    try {
      await mfaChallengeAPI(mfaToken, fd.get('code') as string)
      sessionStorage.removeItem('mfa_token')
      sessionStorage.removeItem('mfa_methods')
      await startOAuthFlow(continueURL)
    } catch (err) {
      if (isAxiosError(err)) {
        setError(err.response?.data?.detail ?? t('errors.mfaFailed'))
      } else {
        setError(t('errors.network'))
      }
      setLoading(false)
    }
  }

  return (
    <div className="space-y-4">
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {hasPasskey && (
        <Button
          type="button"
          className="w-full"
          onClick={handlePasskey}
          disabled={loading || passkeyLoading}
        >
          <Fingerprint className="size-4"/>
          {passkeyLoading ? t('mfa.passkeyPending') : t('mfa.passkey')}
        </Button>
      )}

      {hasPasskey && hasTOTP && (
        <div className="relative">
          <Separator/>
          <span
            className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 bg-card px-2 text-xs text-muted-foreground">
            {t('login.or')}
          </span>
        </div>
      )}

      {hasTOTP && (
        <form onSubmit={handleTOTP} className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="code">{t('mfa.code')}</Label>
            <Input
              id="code"
              name="code"
              type="text"
              inputMode="numeric"
              pattern="[0-9]{6}"
              maxLength={6}
              autoComplete="one-time-code"
              placeholder="000000"
              required
            />
          </div>
          <Button type="submit" variant={hasPasskey ? 'outline' : 'default'} className="w-full"
                  disabled={loading || passkeyLoading}>
            {loading ? t('mfa.submitting') : t('mfa.submit')}
          </Button>
        </form>
      )}
    </div>
  )
}

export default function MFAPage() {
  const {t} = useTranslation()
  return (
    <div className="min-h-screen flex items-center justify-center bg-muted/40 p-4">
      <div className="w-full max-w-md space-y-6">
        <div className="text-center">
          <h1 className="text-2xl font-bold tracking-tight">{t('app.name')}</h1>
          <p className="text-muted-foreground text-sm mt-1">{t('app.tagline')}</p>
        </div>

        <Card>
          <CardHeader>
            <CardTitle>{t('mfa.title')}</CardTitle>
            <CardDescription>{t('mfa.description')}</CardDescription>
          </CardHeader>
          <CardContent>
            <Suspense fallback={<div className="h-32 animate-pulse bg-muted rounded"/>}>
              <MFAForm/>
            </Suspense>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
