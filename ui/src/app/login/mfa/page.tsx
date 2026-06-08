'use client'

import { Suspense, useState, FormEvent } from 'react'
import { useSearchParams } from 'next/navigation'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { api, isAxiosError } from '@/lib/axios'
import { startOAuthFlow } from '@/lib/auth-flow'

function MFAForm() {
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

    const mfaToken = sessionStorage.getItem('mfa_token')
    if (!mfaToken) {
      setError(t('errors.sessionExpired'))
      setLoading(false)
      return
    }

    try {
      await api.post('/v1.0/auth/mfa/challenge', {
        mfa_token: mfaToken,
        code: fd.get('code'),
      })
      sessionStorage.removeItem('mfa_token')
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
    <form onSubmit={handleSubmit} className="space-y-4">
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

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

      <Button type="submit" className="w-full" disabled={loading}>
        {loading ? t('mfa.submitting') : t('mfa.submit')}
      </Button>
    </form>
  )
}

export default function MFAPage() {
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
            <CardTitle>{t('mfa.title')}</CardTitle>
            <CardDescription>{t('mfa.description')}</CardDescription>
          </CardHeader>
          <CardContent>
            <Suspense fallback={<div className="h-32 animate-pulse bg-muted rounded" />}>
              <MFAForm />
            </Suspense>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
