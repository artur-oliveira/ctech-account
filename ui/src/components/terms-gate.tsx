'use client'

import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { LegalConsent, NOTHING_ACCEPTED, isConsentComplete } from '@/components/legal-consent'
import { acceptPendingTermsAPI } from '@/lib/mutations'
import { isAxiosError } from '@/lib/axios'
import type { TermsPending } from '@/lib/types'

/**
 * Blocks the account area until the user re-accepts the documents whose version
 * moved. /authorize gates anyone arriving through OAuth, but a session that keeps
 * refreshing its access token never passes through it again — this is that path.
 */
export function TermsGate({ pending }: { pending: TermsPending }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [accepted, setAccepted] = useState<TermsPending>(NOTHING_ACCEPTED)
  const [error, setError] = useState('')

  const { mutate, isPending } = useMutation({
    mutationFn: () => acceptPendingTermsAPI(accepted),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['profile'] })
    },
    onError: (err) => {
      setError(isAxiosError(err) ? (err.response?.data?.detail ?? t('errors.network')) : t('errors.network'))
    },
  })

  const complete = isConsentComplete(pending, accepted)

  return (
    <div className="min-h-screen flex items-center justify-center bg-muted/40 p-4">
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>{t('acceptTerms.updatedTitle')}</CardTitle>
          <CardDescription>{t('acceptTerms.updatedDescription')}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}

          <LegalConsent pending={pending} accepted={accepted} onChange={setAccepted} disabled={isPending} />

          <Button
            type="button"
            className="w-full"
            disabled={!complete || isPending}
            onClick={() => {
              setError('')
              mutate()
            }}
          >
            {isPending ? t('acceptTerms.submitting') : t('acceptTerms.submit')}
          </Button>
        </CardContent>
      </Card>
    </div>
  )
}
