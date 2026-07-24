'use client'

import { Suspense, useState } from 'react'
import { useSearchParams } from 'next/navigation'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { consentDecisionAPI } from '@/lib/mutations'
import { isAxiosError } from '@/lib/axios'
import { describeScope } from '@/lib/scope-description'
import { DEFAULT_REDIRECT } from '@/lib/safe-redirect'
import { ShieldQuestion, Check } from 'lucide-react'

function ConsentForm() {
  const { t } = useTranslation()
  const params = useSearchParams()
  const req = params.get('req')
  const clientName = params.get('client_name') || t('consent.unknownApp')
  const scopes = (params.get('scope') ?? '').split(' ').filter(Boolean)

  const [pending, setPending] = useState<'approve' | 'deny' | null>(null)
  const [error, setError] = useState('')

  async function decide(approved: boolean) {
    if (!req) return
    setError('')
    setPending(approved ? 'approve' : 'deny')
    try {
      const { redirect_to } = await consentDecisionAPI(req, approved)
      window.location.href = redirect_to
    } catch (err) {
      if (isAxiosError(err)) {
        setError(err.response?.data?.detail ?? t('consent.error'))
      } else {
        setError(t('errors.network'))
      }
      setPending(null)
    }
  }

  if (!req || scopes.length === 0) {
    return (
      <div className="space-y-4">
        <Alert variant="destructive">
          <AlertDescription>{t('consent.invalidRequest')}</AlertDescription>
        </Alert>
        <Button type="button" className="w-full" onClick={() => (window.location.href = DEFAULT_REDIRECT)}>
          {t('consent.goToAccount')}
        </Button>
      </div>
    )
  }

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center gap-2">
          <ShieldQuestion className="size-5 text-muted-foreground" />
          <CardTitle>{t('consent.title', { app: clientName })}</CardTitle>
        </div>
        <CardDescription>{t('consent.description')}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        <ul className="space-y-2">
          {scopes.map((scope) => (
            <li key={scope} className="flex items-start gap-2 text-sm">
              <Check className="size-4 mt-0.5 shrink-0 text-muted-foreground" />
              <div>
                <p>{describeScope(scope, t)}</p>
                <code className="text-xs text-muted-foreground font-mono">{scope}</code>
              </div>
            </li>
          ))}
        </ul>

        <p className="text-xs text-muted-foreground">{t('consent.revocable')}</p>

        <div className="flex gap-2 justify-end">
          <Button
            variant="outline"
            onClick={() => decide(false)}
            disabled={pending !== null}
          >
            {pending === 'deny' ? t('consent.denying') : t('consent.deny')}
          </Button>
          <Button onClick={() => decide(true)} disabled={pending !== null}>
            {pending === 'approve' ? t('consent.authorizing') : t('consent.authorize')}
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

export default function ConsentPage() {
  const { t } = useTranslation()
  return (
    <div className="min-h-screen flex items-center justify-center bg-muted/40 p-4">
      <div className="w-full max-w-md space-y-6">
        <div className="text-center">
          <h1 className="text-2xl font-semibold tracking-tight">{t('app.name')}</h1>
        </div>
        <Suspense fallback={<div className="h-64 animate-pulse bg-muted rounded-lg" />}>
          <ConsentForm />
        </Suspense>
      </div>
    </div>
  )
}
