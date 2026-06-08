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
import { api, isAxiosError } from '@/lib/axios'
import { startOAuthFlow } from '@/lib/auth-flow'

function LoginForm() {
  const { t } = useTranslation()
  const params = useSearchParams()
  const continueURL = params.get('continue') ?? '/account'
  const [loading, setLoading] = useState(false)
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

  return (
    <form onSubmit={handleSubmit} className="space-y-4">
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

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
        <Label htmlFor="password">{t('common.password')}</Label>
        <Input
          id="password"
          name="password"
          type="password"
          autoComplete="current-password"
          required
        />
      </div>

      <Button type="submit" className="w-full" disabled={loading}>
        {loading ? t('login.submitting') : t('login.submit')}
      </Button>
    </form>
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
