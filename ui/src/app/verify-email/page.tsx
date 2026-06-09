'use client'

import { Suspense, useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'next/navigation'
import Link from 'next/link'
import { useTranslation } from 'react-i18next'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { verifyEmailAPI } from '@/lib/mutations'

function VerifyEmailInner() {
  const { t } = useTranslation()
  const params = useSearchParams()
  const token = params.get('token')
  const [state, setState] = useState<'pending' | 'success' | 'error'>(token ? 'pending' : 'error')
  const ran = useRef(false)

  useEffect(() => {
    if (ran.current) return
    ran.current = true
    if (!token) {
      return;
    }
    void verifyEmailAPI(token)
      .then(() => setState('success'))
      .catch(() => setState('error'))
  }, [token])

  if (state === 'pending') {
    return <p className="text-muted-foreground text-sm animate-pulse">{t('common.loading')}</p>
  }

  return (
    <>
      {state === 'success' ? (
        <Alert>
          <AlertDescription>{t('verifyEmail.success')}</AlertDescription>
        </Alert>
      ) : (
        <Alert variant="destructive">
          <AlertDescription>{t('verifyEmail.error')}</AlertDescription>
        </Alert>
      )}
      <p className="text-center text-sm text-muted-foreground mt-4">
        <Link href="/account" className="underline underline-offset-4 hover:text-foreground">
          {t('verifyEmail.goToAccount')}
        </Link>
      </p>
    </>
  )
}

export default function VerifyEmailPage() {
  const { t } = useTranslation()
  return (
    <div className="min-h-screen flex items-center justify-center bg-muted/40 p-4">
      <div className="w-full max-w-md">
        <Card>
          <CardHeader>
            <CardTitle>{t('verifyEmail.title')}</CardTitle>
          </CardHeader>
          <CardContent>
            <Suspense fallback={<p className="text-muted-foreground text-sm animate-pulse">{t('common.loading')}</p>}>
              <VerifyEmailInner />
            </Suspense>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
