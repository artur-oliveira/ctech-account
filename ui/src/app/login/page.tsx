'use client'

import { Suspense, useState, FormEvent } from 'react'
import { useSearchParams } from 'next/navigation'
import Link from 'next/link'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Separator } from '@/components/ui/separator'
import { api, isAxiosError, API_URL } from '@/lib/axios'
import { startOAuthFlow } from '@/lib/auth-flow'
import { beginPasskeyAuthAPI, completePasskeyAuthAPI } from '@/lib/mutations'
import { buildAssertionCredential } from '@/lib/webauthn'
import { Fingerprint } from 'lucide-react'

function LoginForm() {
  const { t } = useTranslation()
  const params = useSearchParams()
  const continueURL = params.get('continue') ?? '/account'
  const [loading, setLoading] = useState(false)
  const [passkeyLoading, setPasskeyLoading] = useState(false)
  const [error, setError] = useState('')

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    setError('')
    setLoading(true)
    const fd = new FormData(e.currentTarget)

    try {
      const { data } = await api.post<{
        requires_mfa: boolean
        mfa_token?: string
        mfa_methods?: string[]
      }>('/v1.0/auth/login', {
        email: fd.get('email'),
        password: fd.get('password'),
      })

      if (data.requires_mfa && data.mfa_token) {
        sessionStorage.setItem('mfa_token', data.mfa_token)
        sessionStorage.setItem('continue_url', continueURL)
        window.location.href = `/login/mfa?continue=${encodeURIComponent(continueURL)}`
        return
      }

      await startOAuthFlow(continueURL)
    } catch (err) {
      if (isAxiosError(err)) {
        setError(err.response?.data?.detail ?? t('errors.loginFailed'))
      } else {
        setError(t('errors.network'))
      }
      setLoading(false)
    }
  }

  async function handlePasskeyLogin() {
    setError('')
    setPasskeyLoading(true)
    try {
      const { session_token, options } = await beginPasskeyAuthAPI()
      const credential = await buildAssertionCredential(options)
      await completePasskeyAuthAPI(session_token, credential)
      await startOAuthFlow(continueURL)
    } catch (err) {
      if (isAxiosError(err)) {
        setError(err.response?.data?.detail ?? t('errors.loginFailed'))
      } else {
        setError(t('errors.network'))
      }
      setPasskeyLoading(false)
    }
  }

  return (
    <div className="space-y-4">
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      <form onSubmit={handleSubmit} className="space-y-4">
        <div className="space-y-1.5">
          <Label htmlFor="email">{t('common.email')}</Label>
          <Input
            id="email"
            name="email"
            type="email"
            autoComplete="email"
            required
            placeholder={t('login.emailPlaceholder')}
          />
        </div>

        <div className="space-y-1.5">
          <div className="flex items-center justify-between">
            <Label htmlFor="password">{t('common.password')}</Label>
            <Link href="/forgot-password" className="text-xs text-muted-foreground hover:text-foreground underline underline-offset-4">
              {t('login.forgotPassword')}
            </Link>
          </div>
          <Input
            id="password"
            name="password"
            type="password"
            autoComplete="current-password"
            required
          />
        </div>

        <Button type="submit" className="w-full" disabled={loading || passkeyLoading}>
          {loading ? t('login.submitting') : t('login.submit')}
        </Button>
      </form>

      <div className="relative">
        <Separator />
        <span className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 bg-card px-2 text-xs text-muted-foreground">
          {t('login.or')}
        </span>
      </div>

      <Button
        type="button"
        variant="outline"
        className="w-full"
        onClick={handlePasskeyLogin}
        disabled={loading || passkeyLoading}
      >
        <Fingerprint className="size-4" />
        {passkeyLoading ? t('login.passkeyPending') : t('login.passkey')}
      </Button>

      <Button
        type="button"
        variant="outline"
        className="w-full"
        onClick={() => { window.location.href = `${API_URL}/v1.0/auth/google?continue=${encodeURIComponent(continueURL)}` }}
        disabled={loading || passkeyLoading}
      >
        <svg className="size-4" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z" fill="#4285F4"/>
          <path d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" fill="#34A853"/>
          <path d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l3.66-2.84z" fill="#FBBC05"/>
          <path d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" fill="#EA4335"/>
        </svg>
        {t('login.google')}
      </Button>
    </div>
  )
}

export default function LoginPage() {
  const { t } = useTranslation()
  return (
    <div className="min-h-screen flex items-center justify-center bg-muted/40 p-4">
      <div className="w-full max-w-md space-y-6">
        <div className="text-center">
          <h1 className="text-2xl font-bold tracking-tight">{t('app.name')}</h1>
          <p className="text-muted-foreground text-sm mt-1">{t('app.tagline')}</p>
        </div>

        <Card>
          <CardHeader>
            <CardTitle>{t('login.title')}</CardTitle>
            <CardDescription>{t('login.description')}</CardDescription>
          </CardHeader>
          <CardContent>
            <Suspense fallback={<div className="h-40 animate-pulse bg-muted rounded" />}>
              <LoginForm />
            </Suspense>

            <p className="mt-4 text-center text-sm text-muted-foreground">
              {t('login.noAccount')}{' '}
              <Link
                href="/register"
                className="text-foreground underline underline-offset-4 hover:text-primary"
              >
                {t('login.createOne')}
              </Link>
            </p>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
