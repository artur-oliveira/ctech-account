'use client'

import { Suspense, useState } from 'react'
import { useSearchParams } from 'next/navigation'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { LegalConsent, NOTHING_ACCEPTED, isConsentComplete } from '@/components/legal-consent'
import { isAxiosError } from '@/lib/axios'
import { acceptTermsAPI } from '@/lib/mutations'
import { sanitizeContinue } from '@/lib/safe-redirect'
import type { TermsPending } from '@/lib/types'

/** Query flag marking a document as pending. Cosmetic — the server recomputes it. */
const PENDING_FLAG = '1'

/**
 * Which documents to render. A Google sign-up carries no flags and owes both;
 * a re-acceptance carries exactly the ones whose version moved.
 */
function pendingFromParams(params: URLSearchParams): TermsPending {
  const tos = params.get('tos') === PENDING_FLAG
  const privacy = params.get('privacy') === PENDING_FLAG
  if (!tos && !privacy) return { tos: true, privacy: true }
  return { tos, privacy }
}

function AcceptTermsForm() {
  const { t } = useTranslation()
  const params = useSearchParams()
  const token = params.get('token')
  const pending = pendingFromParams(params)
  const restartURL = sanitizeContinue(params.get('continue'))

  const [accepted, setAccepted] = useState<TermsPending>(NOTHING_ACCEPTED)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  async function handleSubmit() {
    if (!token) return

    setError('')
    setLoading(true)
    try {
      const { redirect } = await acceptTermsAPI(token, accepted)
      window.location.href = redirect
    } catch (err) {
      if (isAxiosError(err)) {
        setError(err.response?.data?.detail ?? t('errors.network'))
      } else {
        setError(t('errors.network'))
      }
      setLoading(false)
    }
  }

  // The token expires after 10 minutes. Rather than dead-ending, send the user
  // back through the flow — /authorize will mint a fresh one.
  if (!token) {
    return (
      <div className="space-y-4">
        <Alert variant="destructive">
          <AlertDescription>{t('acceptTerms.missingToken')}</AlertDescription>
        </Alert>
        <Button type="button" className="w-full" onClick={() => (window.location.href = restartURL)}>
          {t('acceptTerms.restart')}
        </Button>
      </div>
    )
  }

  return (
    <div className="space-y-4">
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      <LegalConsent pending={pending} accepted={accepted} onChange={setAccepted} disabled={loading} />

      <Button
        type="button"
        className="w-full"
        disabled={loading || !isConsentComplete(pending, accepted)}
        onClick={handleSubmit}
      >
        {loading ? t('acceptTerms.submitting') : t('acceptTerms.submit')}
      </Button>
    </div>
  )
}

export default function AcceptTermsPage() {
  const { t } = useTranslation()
  return (
    <div className="min-h-screen flex items-center justify-center bg-muted/40 p-4">
      <div className="w-full max-w-md space-y-6">
        <div className="text-center space-y-1">
          <p className="text-sm font-medium text-muted-foreground">{t('app.name')}</p>
          <h1 className="text-2xl font-semibold tracking-tight">{t('acceptTerms.title')}</h1>
          <p className="text-muted-foreground text-sm">{t('acceptTerms.description')}</p>
        </div>

        <Card>
          <CardContent>
            <Suspense fallback={<div className="h-32 animate-pulse bg-muted rounded" />}>
              <AcceptTermsForm />
            </Suspense>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
